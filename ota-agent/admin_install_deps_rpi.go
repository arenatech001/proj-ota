package main

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"
)

func installDepsRpiScriptPath() (string, error) {
	return resolveAgentToolScript("install-deps-rpi.sh")
}

// runInstallDepsRpiScript runs tools/install-deps-rpi.sh (alsa/pulse volume tools, /boot/firmware/config.txt, gpio/spi udev; no SDL2).
func runInstallDepsRpiScript(ctx context.Context, logger *Logger) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("install deps only supported on linux")
	}
	script, err := installDepsRpiScriptPath()
	if err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	out, err := runPrivilegedCombined(cctx, script)
	if err != nil {
		if logger != nil {
			logger.Error("install-deps-rpi: %v\n%s", err, strings.TrimSpace(out))
		}
		return out, err
	}
	return out, nil
}
