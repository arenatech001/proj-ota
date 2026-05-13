package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

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

func normalizeWifiNet(n AdminNetworkConfig) AdminNetworkConfig {
	mode := strings.ToLower(strings.TrimSpace(n.WiFiMode))
	if mode == "" {
		mode = "sta"
	}
	return AdminNetworkConfig{
		WiFiMode: mode,
		SSID:     strings.TrimSpace(n.SSID),
		PSK:      n.PSK,
		Iface:    strings.TrimSpace(n.Iface),
	}
}

func wifiWatchdogParamsChanged(a, b AdminNetworkConfig) bool {
	x, y := normalizeWifiNet(a), normalizeWifiNet(b)
	return x != y
}

// runWiFiWatchdogScript runs wifi-watchdog.sh with subcommand "run" or "activate" (activate disconnects first, then connects with cfg).
func runWiFiWatchdogScript(ctx context.Context, cfg *AgentConfig, sub string, logger *Logger) (string, error) {
	if sub != "run" && sub != "activate" {
		return "", fmt.Errorf("invalid wifi watchdog subcommand: %q", sub)
	}
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("wifi watchdog not supported on windows")
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
		sub,
		"--mode", mode,
		"--ssid", strings.TrimSpace(cfg.Network.SSID),
		"--psk", cfg.Network.PSK,
	}
	if iface := strings.TrimSpace(cfg.Network.Iface); iface != "" {
		args = append(args, "--iface", iface)
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, script, args...)
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		logger.Error("wifi watchdog %s: %v\n%s", sub, err, buf.String())
		return buf.String(), err
	}
	return buf.String(), nil
}

// runWiFiWatchdogOnce runs the bundled watchdog once (STA 开放网允许空 PSK；热点需非空密码). Skips if already connected.
func runWiFiWatchdogOnce(ctx context.Context, configPath string, cfg *AgentConfig, logger *Logger) (string, error) {
	_ = configPath
	return runWiFiWatchdogScript(ctx, cfg, "run", logger)
}

// runWiFiActivateOnce disconnects current WiFi then connects using cfg (wifi-watchdog.sh activate).
func runWiFiActivateOnce(ctx context.Context, configPath string, cfg *AgentConfig, logger *Logger) (string, error) {
	_ = configPath
	return runWiFiWatchdogScript(ctx, cfg, "activate", logger)
}

// syncWiFiWatchdogSystemd rewrites wifi-watchdog systemd unit from current YAML (install-timer + daemon-reload).
func syncWiFiWatchdogSystemd(ctx context.Context, cfg *AgentConfig) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	script, err := wifiWatchdogScriptPath()
	if err != nil {
		return err
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Network.WiFiMode))
	if mode == "" {
		mode = "sta"
	}
	args := []string{
		"install-timer",
		"--install-path", script,
		"--mode", mode,
		"--ssid", strings.TrimSpace(cfg.Network.SSID),
		"--psk", cfg.Network.PSK,
	}
	if iface := strings.TrimSpace(cfg.Network.Iface); iface != "" {
		args = append(args, "--iface", iface)
	}
	cmd := exec.CommandContext(ctx, script, args...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
