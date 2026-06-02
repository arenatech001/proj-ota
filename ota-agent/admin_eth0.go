package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func initEth0ScriptPath() (string, error) {
	return resolveAgentToolScript("init-eth0.sh")
}

// runInitEth0Script runs tools/init-eth0.sh (NetworkManager static profile for eth0, no default route).
func runInitEth0Script(ctx context.Context, logger *Logger) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("init eth0 only supported on linux")
	}
	script, err := initEth0ScriptPath()
	if err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, script)
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		if logger != nil {
			logger.Error("init-eth0: %v\n%s", err, strings.TrimSpace(buf.String()))
		}
		return buf.String(), err
	}
	return buf.String(), nil
}
