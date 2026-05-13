package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const defaultSystemdUnit = "arenatech-agent"

// quoteSystemdArg quotes a single argv token for ExecStart= when it contains spaces or special chars.
func quoteSystemdArg(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\n\"\\$`") {
		esc := strings.ReplaceAll(s, `\`, `\\`)
		esc = strings.ReplaceAll(esc, `"`, `\"`)
		return `"` + esc + `"`
	}
	return s
}

func buildArenatechAgentServiceUnit(description, user, exePath, configPath string) string {
	execLine := "ExecStart=" + quoteSystemdArg(exePath) + " " + quoteSystemdArg("-config="+configPath)
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=" + description + "\n")
	b.WriteString("After=network.target\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	if strings.TrimSpace(user) != "" {
		b.WriteString("User=" + user + "\n")
	}
	b.WriteString(execLine + "\n")
	b.WriteString("Restart=always\n")
	b.WriteString("RestartSec=10\n\n")
	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=multi-user.target\n")
	return b.String()
}

func resolveExePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func validSystemdUnitName(unit string) bool {
	if unit == "" || len(unit) > 200 {
		return false
	}
	for i, r := range unit {
		alnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if i == 0 {
			if !alnum {
				return false
			}
			continue
		}
		if !alnum && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func runInstallSystemd(args []string) int {
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "install-systemd only supports Linux (systemd)")
		return 1
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "install-systemd requires root (sudo)")
		return 1
	}

	fs := flag.NewFlagSet("install-systemd", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSystemdSubcommandUsage()
		fmt.Fprintln(os.Stderr, "install-systemd flags:")
		fs.PrintDefaults()
	}
	cfgFlag := fs.String("config", "/home/arenatech/agent/agent.yaml", "path to agent YAML (default: <exe-dir>/agent.yaml)")
	unitFlag := fs.String("unit", defaultSystemdUnit, "systemd unit name (without .service)")
	descFlag := fs.String("description", "Arenatech Agent", "unit Description=")
	userFlag := fs.String("user", "root", "Service User= (empty to omit)")
	fs.Parse(args)
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments: %v\n", fs.Args())
		fs.Usage()
		return 1
	}

	cfgPath := strings.TrimSpace(*cfgFlag)
	if cfgPath == "" {
		p, err := defaultConfigPath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "config path: %v\n", err)
			return 1
		}
		cfgPath = p
	}
	cfgPath, err := filepath.Abs(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config path: %v\n", err)
		return 1
	}
	if _, err := os.Stat(cfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "config file: %v\n", err)
		return 1
	}

	exePath, err := resolveExePath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "executable: %v\n", err)
		return 1
	}

	unit := strings.TrimSpace(*unitFlag)
	if unit == "" {
		fmt.Fprintln(os.Stderr, "-unit must not be empty")
		return 1
	}
	if strings.Contains(unit, "/") || strings.Contains(unit, ".") || !validSystemdUnitName(unit) {
		fmt.Fprintln(os.Stderr, "-unit must be a simple name (no path or .service suffix)")
		return 1
	}

	unitFile := filepath.Join("/etc/systemd/system", unit+".service")
	desc := strings.ReplaceAll(strings.TrimSpace(*descFlag), "\n", " ")
	body := buildArenatechAgentServiceUnit(desc, *userFlag, exePath, cfgPath)
	if err := os.WriteFile(unitFile, []byte(body), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", unitFile, err)
		return 1
	}

	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "systemctl daemon-reload: %v\n%s", err, strings.TrimSpace(string(out)))
		return 1
	}
	if out, err := exec.Command("systemctl", "enable", "--now", unit+".service").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "systemctl enable --now: %v\n%s", err, strings.TrimSpace(string(out)))
		return 1
	}

	fmt.Printf("installed %s\n  ExecStart: %s -config=%s\n", unitFile, exePath, cfgPath)
	return 0
}

func runUninstallSystemd(args []string) int {
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "uninstall-systemd only supports Linux (systemd)")
		return 1
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "uninstall-systemd requires root (sudo)")
		return 1
	}

	fs := flag.NewFlagSet("uninstall-systemd", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSystemdSubcommandUsage()
		fmt.Fprintln(os.Stderr, "uninstall-systemd flags:")
		fs.PrintDefaults()
	}
	unitFlag := fs.String("unit", defaultSystemdUnit, "systemd unit name (without .service)")
	fs.Parse(args)
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments: %v\n", fs.Args())
		fs.Usage()
		return 1
	}

	unit := strings.TrimSpace(*unitFlag)
	if unit == "" {
		fmt.Fprintln(os.Stderr, "-unit must not be empty")
		return 1
	}
	if strings.Contains(unit, "/") || strings.Contains(unit, ".") || !validSystemdUnitName(unit) {
		fmt.Fprintln(os.Stderr, "-unit invalid")
		return 1
	}
	svc := unit + ".service"
	_ = exec.Command("systemctl", "disable", "--now", svc).Run()
	unitFile := filepath.Join("/etc/systemd/system", svc)
	if err := os.Remove(unitFile); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "remove %s: %v\n", unitFile, err)
		return 1
	}
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "systemctl daemon-reload: %v\n%s", err, strings.TrimSpace(string(out)))
		return 1
	}
	fmt.Printf("removed %s\n", unitFile)
	return 0
}

func printSystemdSubcommandUsage() {
	fmt.Fprintf(os.Stderr, `Usage:
  %s install-systemd [-config=PATH] [-unit=NAME] [-description=TEXT] [-user=USER]
  %s uninstall-systemd [-unit=NAME]

install-systemd writes /etc/systemd/system/<unit>.service, runs systemctl daemon-reload, and enable --now.
Requires Linux (systemd) and root. Subcommand must be the first argument (not after agent -config).

`, filepath.Base(os.Args[0]), filepath.Base(os.Args[0]))
}
