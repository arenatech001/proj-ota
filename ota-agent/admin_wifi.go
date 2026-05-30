package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const wifiInstallTimeout = 5 * time.Minute

func wifiWatchdogScriptPath() (string, error) {
	dir, err := getExecutableDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "tools", "wifi-watchdog.sh")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("wifi script not found at %s: %w", p, err)
	}
	return p, nil
}

func shortHostname(h string) string {
	h = strings.TrimSpace(h)
	if h == "" {
		return "ota-device"
	}
	if i := strings.Index(h, "."); i >= 0 {
		return h[:i]
	}
	return h
}

// fillHotspotNetwork fills missing hotspot SSID/PSK (SSID from hostname if empty).
func fillHotspotNetwork(n *AdminNetworkConfig) {
	if n == nil {
		return
	}
	if strings.TrimSpace(n.SSID) == "" {
		h, err := os.Hostname()
		if err != nil || strings.TrimSpace(h) == "" {
			h = "ota-device"
		}
		n.SSID = shortHostname(h)
	}
	if strings.TrimSpace(n.PSK) == "" {
		n.PSK = "AtAdmin0502"
	}
}

// runWiFiInstall registers systemd timer (reads agent.yaml on each run) and optionally applies WiFi now.
// STA 临时回退热点仅在定时 run 中发生，不修改 agent.yaml（重启后仍优先 STA）。
func runWiFiInstall(ctx context.Context, cfgPath string, skipApply bool, logger *Logger) (out string, err error) {
	if runtime.GOOS != "linux" {
		return "", nil
	}
	script, err := wifiWatchdogScriptPath()
	if err != nil {
		return "", err
	}
	cfgPath = strings.TrimSpace(cfgPath)
	if cfgPath == "" {
		return "", fmt.Errorf("config path is empty")
	}
	cfgPath, err = filepath.Abs(cfgPath)
	if err != nil {
		return "", err
	}

	args := []string{
		"install",
		"--config", cfgPath,
		"--install-path", script,
	}
	if skipApply {
		args = append(args, "--no-apply")
	}

	cctx, cancel := context.WithTimeout(ctx, wifiInstallTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, script, args...)
	cmd.Env = os.Environ()
	combined, runErr := cmd.CombinedOutput()
	out = string(combined)
	if runErr != nil {
		if logger != nil {
			logger.Error("wifi install: %v\n%s", runErr, out)
		}
		return out, fmt.Errorf("%w: %s", runErr, strings.TrimSpace(out))
	}
	return out, nil
}

// ensureWiFiWatchdogInstalled idempotently registers wifi-watchdog timer (no immediate apply).
func ensureWiFiWatchdogInstalled(ctx context.Context, cfgPath string, logger *Logger) {
	if runtime.GOOS != "linux" {
		return
	}
	if os.Geteuid() != 0 {
		if logger != nil {
			logger.Info("wifi-watchdog: skip auto-install (not root)")
		}
		return
	}
	out, err := runWiFiInstall(ctx, cfgPath, true, logger)
	if err != nil {
		if logger != nil {
			logger.Warn("wifi-watchdog auto-install: %v", err)
		}
		return
	}
	if logger != nil && strings.TrimSpace(out) != "" {
		logger.Info("wifi-watchdog: %s", strings.TrimSpace(out))
	}
}

// applyWiFiOnSave runs install and applies WiFi immediately from agent.yaml.
func applyWiFiOnSave(ctx context.Context, rt *adminRuntime, logger *Logger) (out string, err error) {
	return runWiFiInstall(ctx, rt.path, false, logger)
}
