package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
)

// needsSudo reports whether privileged ops should go through sudo -n.
func needsSudo() bool {
	return runtime.GOOS == "linux" && os.Geteuid() != 0
}

// privilegedCommandContext runs name/args as root: directly when euid==0, else sudo -n --.
func privilegedCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	if !needsSudo() {
		return exec.CommandContext(ctx, name, args...)
	}
	sudoArgs := make([]string, 0, 2+len(args))
	sudoArgs = append(sudoArgs, "-n", "--", name)
	sudoArgs = append(sudoArgs, args...)
	return exec.CommandContext(ctx, "sudo", sudoArgs...)
}

// runPrivilegedCombined runs a privileged command and returns combined stdout/stderr.
func runPrivilegedCombined(ctx context.Context, name string, args ...string) (string, error) {
	cmd := privilegedCommandContext(ctx, name, args...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// writeFilePrivileged writes data to path as root (os.WriteFile when root, else sudo tee).
func writeFilePrivileged(ctx context.Context, path string, data []byte) error {
	if !needsSudo() {
		return os.WriteFile(path, data, 0644)
	}
	tee, err := exec.LookPath("tee")
	if err != nil {
		return fmt.Errorf("tee: %w", err)
	}
	cmd := privilegedCommandContext(ctx, tee, path)
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = io.Discard
	cmd.Stderr = &bytes.Buffer{}
	if err := cmd.Run(); err != nil {
		stderr := ""
		if buf, ok := cmd.Stderr.(*bytes.Buffer); ok {
			stderr = buf.String()
		}
		if stderr != "" {
			return fmt.Errorf("sudo tee %s: %w: %s", path, err, stderr)
		}
		return fmt.Errorf("sudo tee %s: %w", path, err)
	}
	return nil
}
