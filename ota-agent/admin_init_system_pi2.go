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

func initSystemPi2ScriptPath() (string, error) {
	return resolveAgentToolScript("init-system-pi2.sh")
}

// runInitSystemPi2Script runs tools/init-system-pi2.sh (udev gpio/spi + armbianEnv.txt).
func runInitSystemPi2Script(ctx context.Context, logger *Logger) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("init-system-pi2 only supported on linux")
	}
	script, err := initSystemPi2ScriptPath()
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
			logger.Error("init-system-pi2: %v\n%s", err, strings.TrimSpace(buf.String()))
		}
		return buf.String(), err
	}
	return buf.String(), nil
}
