package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxBizYAMLBytes = 2 << 20 // 2 MiB

type bizConfigKind string

const (
	bizKindClient bizConfigKind = "client"
	bizKindServer bizConfigKind = "server"
)

func parseBizConfigKind(s string) (bizConfigKind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "client":
		return bizKindClient, nil
	case "server":
		return bizKindServer, nil
	default:
		return "", fmt.Errorf("kind must be client or server")
	}
}

// resolveBizYAMLPath returns the absolute path for client/server YAML.
// Relative paths are resolved against the agent install root; empty uses defaults
// under <agent-root>/config/ (or beside agent.yaml if root is unknown).
func resolveBizYAMLPath(agentYAMLPath string, cfg *AgentConfig, kind bizConfigKind) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("nil config")
	}
	var rel string
	var defaultName string
	switch kind {
	case bizKindClient:
		rel = strings.TrimSpace(cfg.BizConfig.ClientPath)
		defaultName = "client.yaml"
	case bizKindServer:
		rel = strings.TrimSpace(cfg.BizConfig.ServerPath)
		defaultName = "server.yaml"
	default:
		return "", fmt.Errorf("unknown kind")
	}

	var abs string
	if rel == "" {
		if root, err := agentInstallRoot(); err == nil {
			abs = filepath.Join(root, "config", defaultName)
		} else {
			abs = filepath.Join(filepath.Dir(agentYAMLPath), defaultName)
		}
	} else if filepath.IsAbs(rel) {
		abs = filepath.Clean(rel)
	} else if root, err := agentInstallRoot(); err == nil {
		abs = filepath.Join(root, filepath.Clean(rel))
	} else {
		abs = filepath.Join(filepath.Dir(agentYAMLPath), filepath.Clean(rel))
	}

	abs, err := filepath.Abs(abs)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func bizProcessID(cfg *AgentConfig, kind bizConfigKind) string {
	if cfg == nil {
		return ""
	}
	switch kind {
	case bizKindClient:
		return strings.TrimSpace(cfg.BizConfig.ClientProcessID)
	case bizKindServer:
		return strings.TrimSpace(cfg.BizConfig.ServerProcessID)
	default:
		return ""
	}
}

func validateYAMLSyntax(content []byte) error {
	var node yaml.Node
	if err := yaml.Unmarshal(content, &node); err != nil {
		return fmt.Errorf("invalid yaml: %w", err)
	}
	return nil
}

func writeBytesAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *adminServer) handleAPIBizConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !strings.HasPrefix(r.URL.Path, "/api/biz-config/") {
		http.NotFound(w, r)
		return
	}
	if !s.requireAuth(w, r) {
		return
	}
	kindStr, _ := strings.CutPrefix(r.URL.Path, "/api/biz-config/")
	kindStr = strings.Trim(kindStr, "/")
	if kindStr == "" || strings.Contains(kindStr, "/") {
		http.NotFound(w, r)
		return
	}
	kind, err := parseBizConfigKind(kindStr)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	cfg := s.runtime.get()
	path, err := resolveBizYAMLPath(s.runtime.path, cfg, kind)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	switch r.Method {
	case http.MethodGet:
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"kind":               kind,
					"path":               path,
					"content":            "",
					"exists":             false,
					"process_id":         bizProcessID(cfg, kind),
					"client_process_id":  cfg.BizConfig.ClientProcessID,
					"server_process_id":  cfg.BizConfig.ServerProcessID,
				})
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind":              kind,
			"path":              path,
			"content":           string(b),
			"exists":            true,
			"process_id":        bizProcessID(cfg, kind),
			"client_process_id": cfg.BizConfig.ClientProcessID,
			"server_process_id": cfg.BizConfig.ServerProcessID,
		})
	case http.MethodPut:
		var body struct {
			Content string `json:"content"`
			Restart bool   `json:"restart"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, maxBizYAMLBytes+4096)).Decode(&body); err != nil {
			http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}
		content := []byte(body.Content)
		if len(content) > maxBizYAMLBytes {
			writeJSONError(w, http.StatusBadRequest, "content too large")
			return
		}
		if err := validateYAMLSyntax(content); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := writeBytesAtomic(path, content, 0600); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		resp := map[string]any{
			"ok":      true,
			"kind":    kind,
			"path":    path,
			"saved":   true,
			"restart": body.Restart,
		}
		if body.Restart {
			procID := bizProcessID(cfg, kind)
			if procID == "" {
				writeJSONError(w, http.StatusBadRequest, "config saved but restart skipped: biz_config.*_process_id is empty in agent.yaml")
				return
			}
			reg := s.runtime.registry
			if reg == nil {
				writeJSONError(w, http.StatusServiceUnavailable, "config saved but process registry not available")
				return
			}
			if err := reg.Restart(procID); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "config saved but restart failed: "+err.Error())
				return
			}
			resp["restarted"] = true
			resp["process_id"] = procID
		}
		_ = json.NewEncoder(w).Encode(resp)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}
