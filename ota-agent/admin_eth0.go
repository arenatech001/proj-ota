package main

import (
	"context"
	"fmt"
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
	out, err := runPrivilegedCombined(cctx, script)
	if err != nil {
		if logger != nil {
			logger.Error("init-eth0: %v\n%s", err, strings.TrimSpace(out))
		}
		return out, err
	}
	return out, nil
}
