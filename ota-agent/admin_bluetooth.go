package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var btMACRe = regexp.MustCompile(`(?i)^([0-9A-F]{2}:){5}[0-9A-F]{2}$`)

type btDevice struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}

func bluetoothGamepadScriptPath() (string, error) {
	return resolveAgentToolScript("bluetooth-gamepad.sh")
}

func runBluetoothGamepad(ctx context.Context, logger *Logger, args ...string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("bluetooth only supported on linux")
	}
	script, err := bluetoothGamepadScriptPath()
	if err != nil {
		return "", err
	}
	out, err := runPrivilegedCombined(ctx, script, args...)
	if err != nil {
		if logger != nil {
			logger.Error("bluetooth-gamepad: %v\n%s", err, strings.TrimSpace(out))
		}
		return out, err
	}
	return out, nil
}

func parseBTDevicesJSON(out string) ([]btDevice, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return []btDevice{}, nil
	}
	// CombinedOutput 会把 stderr 日志拼到 JSON 后；只解码第一个 JSON 值。
	if i := strings.Index(out, "["); i >= 0 {
		out = out[i:]
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var devices []btDevice
	if err := dec.Decode(&devices); err != nil {
		return nil, fmt.Errorf("parse devices json: %w (raw=%q)", err, truncateForErr(out, 200))
	}
	if devices == nil {
		devices = []btDevice{}
	}
	return devices, nil
}

func truncateForErr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func normalizeBTMAC(addr string) (string, error) {
	addr = strings.TrimSpace(strings.ToUpper(addr))
	if !btMACRe.MatchString(addr) {
		return "", fmt.Errorf("invalid bluetooth address %q", addr)
	}
	return addr, nil
}

func (s *adminServer) handleAPIBluetoothPaired(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAuth(w, r) {
		return
	}
	if runtime.GOOS != "linux" {
		writeJSONError(w, http.StatusBadRequest, "unsupported platform")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := runBluetoothGamepad(ctx, s.logger, "list-paired")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error()+": "+strings.TrimSpace(out))
		return
	}
	devices, err := parseBTDevicesJSON(out)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"devices": devices})
}

func (s *adminServer) handleAPIBluetoothScan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAuth(w, r) {
		return
	}
	if runtime.GOOS != "linux" {
		writeJSONError(w, http.StatusBadRequest, "unsupported platform")
		return
	}
	sec := 10
	if q := strings.TrimSpace(r.URL.Query().Get("seconds")); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 3 || n > 60 {
			writeJSONError(w, http.StatusBadRequest, "seconds must be 3–60")
			return
		}
		sec = n
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(sec+20)*time.Second)
	defer cancel()
	out, err := runBluetoothGamepad(ctx, s.logger, "scan", strconv.Itoa(sec))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error()+": "+strings.TrimSpace(out))
		return
	}
	devices, err := parseBTDevicesJSON(out)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"devices": devices, "scan_seconds": sec})
}

func (s *adminServer) handleAPIBluetoothPair(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAuth(w, r) {
		return
	}
	if runtime.GOOS != "linux" {
		writeJSONError(w, http.StatusBadRequest, "unsupported platform")
		return
	}
	var body struct {
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	addr, err := normalizeBTMAC(body.Address)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()
	out, err := runBluetoothGamepad(ctx, s.logger, "pair", addr)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error()+": "+strings.TrimSpace(out))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "address": addr, "output": strings.TrimSpace(out)})
}

func (s *adminServer) handleAPIBluetoothRemove(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAuth(w, r) {
		return
	}
	if runtime.GOOS != "linux" {
		writeJSONError(w, http.StatusBadRequest, "unsupported platform")
		return
	}
	var body struct {
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	addr, err := normalizeBTMAC(body.Address)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := runBluetoothGamepad(ctx, s.logger, "remove", addr)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error()+": "+strings.TrimSpace(out))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "address": addr, "output": strings.TrimSpace(out)})
}
