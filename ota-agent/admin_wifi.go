package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const wifiInstallTimeout = 5 * time.Minute

func wifiWatchdogScriptPath() (string, error) {
	return resolveAgentToolScript("wifi-watchdog.sh")
}

func wifiApplyScriptPath() (string, error) {
	return resolveAgentToolScript("wifi-apply.sh")
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
		n.PSK = "Arenatech0502"
	}
}

// runWiFiInstall registers systemd timer (wifi-watchdog.sh run). Does not apply WiFi now.
func runWiFiInstall(ctx context.Context, cfgPath string, logger *Logger) (out string, err error) {
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

	cctx, cancel := context.WithTimeout(ctx, wifiInstallTimeout)
	defer cancel()
	out, runErr := runPrivilegedCombined(cctx, script, args...)
	if runErr != nil {
		if logger != nil {
			logger.Error("wifi-watchdog install: %v\n%s", runErr, out)
		}
		return out, fmt.Errorf("%w: %s", runErr, strings.TrimSpace(out))
	}
	return out, nil
}

// runWiFiApply applies network settings from agent.yaml immediately (wifi-apply.sh).
func runWiFiApply(ctx context.Context, cfgPath string, logger *Logger) (out string, err error) {
	if runtime.GOOS != "linux" {
		return "", nil
	}
	script, err := wifiApplyScriptPath()
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

	cctx, cancel := context.WithTimeout(ctx, wifiInstallTimeout)
	defer cancel()
	out, runErr := runPrivilegedCombined(cctx, script, "apply", "--config", cfgPath)
	if runErr != nil {
		if logger != nil {
			logger.Error("wifi-apply: %v\n%s", runErr, out)
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
	out, err := runWiFiInstall(ctx, cfgPath, logger)
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

// applyWiFiOnSave applies WiFi immediately, then ensures the watchdog timer is installed.
func applyWiFiOnSave(ctx context.Context, rt *adminRuntime, logger *Logger) (out string, err error) {
	applyOut, applyErr := runWiFiApply(ctx, rt.path, logger)
	ensureWiFiWatchdogInstalled(ctx, rt.path, logger)
	if applyErr != nil {
		return applyOut, applyErr
	}
	return applyOut, nil
}
