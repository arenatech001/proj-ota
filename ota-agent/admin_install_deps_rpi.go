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

func installDepsRpiScriptPath() (string, error) {
	dir, err := getExecutableDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "tools", "install-deps-rpi.sh")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("install-deps-rpi script not found at %s: %w", p, err)
	}
	return p, nil
}

// runInstallDepsRpiScript runs tools/install-deps-rpi.sh (apt SDL2 deps, ALSA, /boot/firmware/config.txt, etc.).
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
	cmd := exec.CommandContext(cctx, script)
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		if logger != nil {
			logger.Error("install-deps-rpi: %v\n%s", err, strings.TrimSpace(buf.String()))
		}
		return buf.String(), err
	}
	return buf.String(), nil
}
