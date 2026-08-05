package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

const agentSudoersPath = "/etc/sudoers.d/arenatech-agent"

// privilegedToolScripts are tools/ scripts the non-root agent may run via sudo -n.
var privilegedToolScripts = []string{
	"wifi-watchdog.sh",
	"wifi-apply.sh",
	"init-eth0.sh",
	"install-deps-rpi.sh",
	"init-system-pi2.sh",
	"bluetooth-gamepad.sh",
}

func validSudoersUser(user string) bool {
	if user == "" || len(user) > 32 {
		return false
	}
	for i, r := range user {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		if r == '_' || r == '-' {
			if i == 0 {
				return false
			}
			continue
		}
		return false
	}
	return true
}

func resolvePrivilegedToolPaths() ([]string, error) {
	paths := make([]string, 0, len(privilegedToolScripts))
	for _, name := range privilegedToolScripts {
		p, err := resolveAgentToolScript(name)
		if err != nil {
			return nil, err
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		paths = append(paths, abs)
	}
	return paths, nil
}

func securePrivilegedToolScripts(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	toolsDir := filepath.Dir(paths[0])
	if err := os.Chown(toolsDir, 0, 0); err != nil {
		return fmt.Errorf("chown %s: %w", toolsDir, err)
	}
	if err := os.Chmod(toolsDir, 0755); err != nil {
		return fmt.Errorf("chmod %s: %w", toolsDir, err)
	}
	for _, p := range paths {
		if err := os.Chown(p, 0, 0); err != nil {
			return fmt.Errorf("chown %s: %w", p, err)
		}
		if err := os.Chmod(p, 0755); err != nil {
			return fmt.Errorf("chmod %s: %w", p, err)
		}
	}
	return nil
}

func buildAgentSudoersBody(user string, toolPaths []string, hostnamectl, tee string) string {
	var b strings.Builder
	b.WriteString("# Managed by ota-agent install-systemd — do not edit by hand.\n")
	b.WriteString("# Allows non-root agent to run network/system setup scripts.\n")
	b.WriteString("Defaults:")
	b.WriteString(user)
	b.WriteString(" !requiretty\n")
	b.WriteString(user)
	b.WriteString(" ALL=(root) NOPASSWD: ")
	cmds := make([]string, 0, len(toolPaths)+2)
	for _, p := range toolPaths {
		cmds = append(cmds, p)
	}
	if hostnamectl != "" {
		cmds = append(cmds, hostnamectl+" set-hostname *")
	}
	if tee != "" {
		cmds = append(cmds, tee+" "+raspberryFirmwareUserData)
	}
	b.WriteString(strings.Join(cmds, ", "))
	b.WriteString("\n")
	return b.String()
}

func validateSudoersFile(path string) error {
	cmd := exec.Command("visudo", "-cf", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("visudo -cf: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// installAgentSudoers writes /etc/sudoers.d/arenatech-agent for a non-root service user.
// When user is empty or root, removes any existing drop-in (agent runs as root).
func installAgentSudoers(user string) error {
	user = strings.TrimSpace(user)
	if user == "" || user == "root" {
		if err := os.Remove(agentSudoersPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if !validSudoersUser(user) {
		return fmt.Errorf("invalid sudoers user %q", user)
	}

	toolPaths, err := resolvePrivilegedToolPaths()
	if err != nil {
		return err
	}
	if err := securePrivilegedToolScripts(toolPaths); err != nil {
		return fmt.Errorf("secure tool scripts: %w", err)
	}

	hostnamectl, err := exec.LookPath("hostnamectl")
	if err != nil {
		return fmt.Errorf("hostnamectl: %w", err)
	}
	tee, err := exec.LookPath("tee")
	if err != nil {
		return fmt.Errorf("tee: %w", err)
	}

	body := buildAgentSudoersBody(user, toolPaths, hostnamectl, tee)
	tmp := agentSudoersPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0440); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := validateSudoersFile(tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, agentSudoersPath); err != nil {
		return err
	}
	if err := os.Chmod(agentSudoersPath, 0440); err != nil {
		return err
	}
	if err := os.Chown(agentSudoersPath, 0, 0); err != nil {
		return err
	}
	return nil
}

func removeAgentSudoers() error {
	if err := os.Remove(agentSudoersPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
