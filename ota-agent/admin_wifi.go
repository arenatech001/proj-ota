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

// fillHotspotNetwork sets SSID from hostname for YAML persistence (PSK lives in script).
func fillHotspotNetwork(n *AdminNetworkConfig) {
	h, err := os.Hostname()
	if err != nil || strings.TrimSpace(h) == "" {
		h = "ota-device"
	}
	n.SSID = shortHostname(h)
	n.PSK = ""
}

// runWiFiInstallTimer installs systemd timer (clears saved WiFi profiles in script) and optionally applies WiFi now.
// STA 临时回退热点仅在定时 run 中发生，不修改 agent.yaml / systemd（重启后仍优先 STA）。
func runWiFiInstallTimer(ctx context.Context, cfg *AgentConfig, skipApply bool, logger *Logger) (out string, err error) {
	if runtime.GOOS != "linux" {
		return "", nil
	}
	script, err := wifiWatchdogScriptPath()
	if err != nil {
		return "", err
	}

	mode := strings.ToLower(strings.TrimSpace(cfg.Network.WiFiMode))
	if mode == "" {
		mode = "sta"
	}

	args := []string{
		"install-timer",
		"--install-path", script,
		"--mode", mode,
	}
	if mode == "sta" {
		args = append(args, "--ssid", strings.TrimSpace(cfg.Network.SSID), "--psk", cfg.Network.PSK)
	}
	if iface := strings.TrimSpace(cfg.Network.Iface); iface != "" {
		args = append(args, "--iface", iface)
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
			logger.Error("wifi install-timer: %v\n%s", runErr, out)
		}
		return out, fmt.Errorf("%w: %s", runErr, strings.TrimSpace(out))
	}
	return out, nil
}

// applyWiFiOnSave runs install-timer; does not persist timer-run hotspot fallback (reboot keeps STA priority).
func applyWiFiOnSave(ctx context.Context, rt *adminRuntime, logger *Logger) (out string, err error) {
	return runWiFiInstallTimer(ctx, rt.get(), false, logger)
}
