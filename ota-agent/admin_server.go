package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookieName = "ota_admin_session"
	sessionMaxAge     = 86400 * time.Second
)

type sessionStore struct {
	mu    sync.Mutex
	token map[string]time.Time // token -> expiry
}

func (s *sessionStore) newSession() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	}
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == nil {
		s.token = make(map[string]time.Time)
	}
	s.token[tok] = time.Now().Add(sessionMaxAge)
	return tok
}

func (s *sessionStore) valid(tok string) bool {
	if tok == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.token[tok]
	if !ok || time.Now().After(exp) {
		delete(s.token, tok)
		return false
	}
	return true
}

func (s *sessionStore) revoke(tok string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.token, tok)
}

func (s *sessionStore) sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for t, exp := range s.token {
		if now.After(exp) {
			delete(s.token, t)
		}
	}
}

type adminRuntime struct {
	mu       sync.RWMutex
	cfg      *AgentConfig
	path     string
	registry *processRegistry
	logger   *Logger
}

func (a *adminRuntime) get() *AgentConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.cfg == nil {
		return &AgentConfig{}
	}
	cp := *a.cfg
	cp.Processes = append([]ManagedProcessConfig(nil), a.cfg.Processes...)
	return &cp
}

func (a *adminRuntime) set(cfg *AgentConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cfg == nil {
		a.cfg = &AgentConfig{}
		return
	}
	cp := *cfg
	cp.Processes = append([]ManagedProcessConfig(nil), cfg.Processes...)
	a.cfg = &cp
}

func (a *adminRuntime) saveAndSync(cfg *AgentConfig) error {
	if err := validateAgentConfig(cfg); err != nil {
		return err
	}
	old := a.get()
	if err := saveAgentConfigAtomic(a.path, cfg); err != nil {
		return err
	}
	a.set(cfg)
	if runtime.GOOS == "linux" && wifiWatchdogParamsChanged(old.Network, cfg.Network) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := syncWiFiWatchdogSystemd(ctx, cfg); err != nil {
			if a.logger != nil {
				a.logger.Error("wifi systemd sync: %v", err)
			}
			return fmt.Errorf("wifi systemd sync: %w", err)
		}
		if a.logger != nil {
			a.logger.Info("wifi watchdog systemd unit updated (install-timer)")
		}
	}
	if a.registry != nil {
		if err := a.registry.Sync(cfg); err != nil {
			return err
		}
	}
	return nil
}

type adminServer struct {
	addr     string
	runtime  *adminRuntime
	sessions *sessionStore
	srv      *http.Server
	static   fs.FS
	logger   *Logger
}

func newAdminServer(addr string, rt *adminRuntime, static fs.FS, logger *Logger) *adminServer {
	if strings.TrimSpace(addr) == "" {
		return nil
	}
	return &adminServer{
		addr:     addr,
		runtime:  rt,
		sessions: &sessionStore{},
		static:   static,
		logger:   logger,
	}
}

func (s *adminServer) Start() error {
	if s == nil {
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/login", s.handleLoginPage)
	mux.HandleFunc("/app", s.handleAppPage)
	mux.HandleFunc("/api/login", s.handleAPILogin)
	mux.HandleFunc("/api/logout", s.handleAPILogout)
	mux.HandleFunc("/api/session", s.handleAPISession)
	mux.HandleFunc("/api/processes", s.handleAPIProcessesCollection)
	mux.HandleFunc("/api/processes/", s.handleAPIProcessItem)
	mux.HandleFunc("/api/network", s.handleAPINetwork)
	mux.HandleFunc("/api/network/status", s.handleAPINetworkStatus)
	mux.HandleFunc("/api/network/hostname", s.handleAPIHostname)
	mux.HandleFunc("/api/wifi/run-watchdog", s.handleAPIWiFiRun)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(s.static))))

	s.srv = &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		s.logger.Info("admin HTTP listening on %s", s.addr)
		if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("admin HTTP: %v", err)
		}
	}()
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for range t.C {
			s.sessions.sweep()
		}
	}()
	return nil
}

func (s *adminServer) Shutdown(ctx context.Context) error {
	if s == nil || s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

func (s *adminServer) readSession(r *http.Request) string {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	return c.Value
}

func (s *adminServer) requireAuth(w http.ResponseWriter, r *http.Request) bool {
	tok := s.readSession(r)
	if !s.sessions.valid(tok) {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	return true
}

func (s *adminServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if s.readSession(r) != "" && s.sessions.valid(s.readSession(r)) {
		http.Redirect(w, r, "/app", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *adminServer) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b, err := fs.ReadFile(s.static, "login.html")
	if err != nil {
		http.Error(w, "login page missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (s *adminServer) handleAppPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.sessions.valid(s.readSession(r)) {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	b, err := fs.ReadFile(s.static, "app.html")
	if err != nil {
		http.Error(w, "app page missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (s *adminServer) handleAPILogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	ct := r.Header.Get("Content-Type")
	var user, pass string
	if strings.Contains(ct, "application/json") {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}
		user, pass = body.Username, body.Password
	} else {
		if err := r.ParseForm(); err != nil {
			http.Error(w, `{"error":"bad form"}`, http.StatusBadRequest)
			return
		}
		user, pass = r.FormValue("username"), r.FormValue("password")
	}
	cfg := s.runtime.get()
	if strings.TrimSpace(cfg.AdminUsername) == "" {
		http.Error(w, `{"error":"admin not configured"}`, http.StatusServiceUnavailable)
		return
	}
	if user != cfg.AdminUsername || pass != cfg.AdminPassword {
		time.Sleep(200 * time.Millisecond)
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}
	tok := s.sessions.newSession()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    tok,
		Path:     "/",
		MaxAge:   int(sessionMaxAge.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *adminServer) handleAPILogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	tok := s.readSession(r)
	s.sessions.revoke(tok)
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1})
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *adminServer) handleAPISession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	ok := s.sessions.valid(s.readSession(r))
	_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": ok})
}

func (s *adminServer) handleAPIProcessesCollection(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		if !s.requireAuth(w, r) {
			return
		}
		cfg := s.runtime.get()
		st := s.runtime.registry.Status()
		type row struct {
			ManagedProcessConfig
			Running      bool `json:"running"`
			RestartCount int  `json:"restart_count"`
		}
		out := make([]row, 0, len(cfg.Processes))
		for _, p := range cfg.Processes {
			rw := row{ManagedProcessConfig: p}
			if s, ok := st[p.ID]; ok {
				rw.Running = s.Running
				rw.RestartCount = s.RestartCount
			}
			out = append(out, rw)
		}
		_ = json.NewEncoder(w).Encode(out)
	case http.MethodPost:
		if !s.requireAuth(w, r) {
			return
		}
		var p ManagedProcessConfig
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&p); err != nil {
			http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}
		p.ID = strings.TrimSpace(p.ID)
		if p.ID == "" {
			http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
			return
		}
		cfg := s.runtime.get()
		for _, x := range cfg.Processes {
			if x.ID == p.ID {
				http.Error(w, `{"error":"id exists"}`, http.StatusConflict)
				return
			}
		}
		cfg.Processes = append(cfg.Processes, p)
		if err := s.runtime.saveAndSync(cfg); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(p)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *adminServer) handleAPIProcessItem(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !strings.HasPrefix(r.URL.Path, "/api/processes/") {
		http.NotFound(w, r)
		return
	}
	id, _ := strings.CutPrefix(r.URL.Path, "/api/processes/")
	id = strings.Trim(id, "/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	if !s.requireAuth(w, r) {
		return
	}
	cfg := s.runtime.get()
	idx := -1
	for i, p := range cfg.Processes {
		if p.ID == id {
			idx = i
			break
		}
	}
	switch r.Method {
	case http.MethodPut:
		if idx < 0 {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		var p ManagedProcessConfig
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&p); err != nil {
			http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}
		p.ID = id
		cfg.Processes[idx] = p
		if err := s.runtime.saveAndSync(cfg); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(p)
	case http.MethodDelete:
		if idx < 0 {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		cfg.Processes = append(cfg.Processes[:idx], cfg.Processes[idx+1:]...)
		if err := s.runtime.saveAndSync(cfg); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *adminServer) handleAPINetwork(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		if !s.requireAuth(w, r) {
			return
		}
		cfg := s.runtime.get()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"wifi_mode":  cfg.Network.WiFiMode,
			"wifi_ssid":  cfg.Network.SSID,
			"wifi_iface": cfg.Network.Iface,
		})
	case http.MethodPut:
		if !s.requireAuth(w, r) {
			return
		}
		var body struct {
			WiFiMode string `json:"wifi_mode"`
			SSID     string `json:"wifi_ssid"`
			PSK      string `json:"wifi_psk"`
			Iface    string `json:"wifi_iface"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}
		mode := strings.ToLower(strings.TrimSpace(body.WiFiMode))
		if mode != "sta" && mode != "hotspot" {
			http.Error(w, `{"error":"wifi_mode must be sta or hotspot"}`, http.StatusBadRequest)
			return
		}
		cfg := s.runtime.get()
		cfg.Network.WiFiMode = mode
		cfg.Network.SSID = strings.TrimSpace(body.SSID)
		cfg.Network.Iface = strings.TrimSpace(body.Iface)
		if body.PSK != "" {
			cfg.Network.PSK = body.PSK
		}
		if err := s.runtime.saveAndSync(cfg); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *adminServer) handleAPINetworkStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAuth(w, r) {
		return
	}
	host, _ := os.Hostname()
	out := map[string]any{"hostname": host}
	if runtime.GOOS == "linux" {
		if b, err := exec.Command("hostnamectl", "hostname").Output(); err == nil {
			out["hostname_static"] = strings.TrimSpace(string(b))
		}
		if b, err := exec.Command("nmcli", "-t", "-f", "DEVICE,TYPE,STATE", "device").Output(); err == nil {
			out["nmcli_devices"] = strings.TrimSpace(string(b))
		}
	} else {
		out["nmcli_devices"] = ""
	}
	cfg := s.runtime.get()
	out["wifi_mode"] = cfg.Network.WiFiMode
	out["wifi_ssid"] = cfg.Network.SSID
	out["wifi_iface"] = cfg.Network.Iface
	if raspberryUserDataFilePresent() {
		out["raspberry_user_data"] = true
		out["raspberry_user_data_path"] = raspberryFirmwareUserData
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (s *adminServer) handleAPIHostname(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPut {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAuth(w, r) {
		return
	}
	var body struct {
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	h := strings.TrimSpace(body.Hostname)
	if h == "" || len(h) > 253 || strings.ContainsAny(h, " \t\n\r\"") {
		http.Error(w, `{"error":"invalid hostname"}`, http.StatusBadRequest)
		return
	}
	if runtime.GOOS != "linux" {
		http.Error(w, `{"error":"unsupported platform"}`, http.StatusBadRequest)
		return
	}
	userDataUpdated, err := tryUpdateRaspberryUserDataHostname(h)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	cmd := exec.Command("hostnamectl", "set-hostname", h)
	out, hcErr := cmd.CombinedOutput()
	if hcErr != nil && !userDataUpdated {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, strings.TrimSpace(string(out))), http.StatusInternalServerError)
		return
	}
	resp := map[string]any{
		"ok":                          true,
		"raspberry_user_data_updated": userDataUpdated,
		"reboot_required":             true,
		"message":                     "主机名变更须重启 Linux 后方可完全生效（当前已尽量用 hostnamectl 与 user-data 落盘）。",
	}
	if hcErr != nil {
		resp["hostnamectl_error"] = strings.TrimSpace(string(out))
	}
	if userDataUpdated {
		resp["raspberry_user_data_path"] = raspberryFirmwareUserData
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *adminServer) handleAPIWiFiRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAuth(w, r) {
		return
	}
	out, err := runWiFiWatchdogOnce(context.Background(), s.runtime.path, s.runtime.get(), s.logger)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"output": out})
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
