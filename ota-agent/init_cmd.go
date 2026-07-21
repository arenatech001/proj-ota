package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func printInitRpiUsage() {
	fmt.Fprintf(os.Stderr, `Usage:
  %s init-rpi [-config=PATH] [-unit=NAME] [-description=TEXT] [-user=USER]

树莓派首次部署：install-systemd + install-deps-rpi.sh + init-eth0.sh + wifi-watchdog install (--no-apply)。

需要 Linux、root，且 -config 指向的 agent.yaml 须已存在。

`, filepath.Base(os.Args[0]))
}

func printInitPi2Usage() {
	fmt.Fprintf(os.Stderr, `Usage:
  %s init-pi2 [-config=PATH] [-unit=NAME] [-description=TEXT] [-user=USER]

PI2（Armbian）首次部署：install-systemd + init-system-pi2.sh + init-eth0.sh + wifi-watchdog install (--no-apply)。

需要 Linux、root，且 -config 指向的 agent.yaml 须已存在。

`, filepath.Base(os.Args[0]))
}

func runInitRpi(args []string) int {
	return runInitPlatform("init-rpi", printInitRpiUsage, args, func(ctx context.Context, logger *Logger) (string, error) {
		return runInstallDepsRpiScript(ctx, logger)
	}, "install-deps-rpi.sh")
}

func runInitPi2(args []string) int {
	return runInitPlatform("init-pi2", printInitPi2Usage, args, func(ctx context.Context, logger *Logger) (string, error) {
		return runInitSystemPi2Script(ctx, logger)
	}, "init-system-pi2.sh")
}

func runInitPlatform(
	cmdName string,
	printUsage func(),
	args []string,
	runPlatformDeps func(ctx context.Context, logger *Logger) (string, error),
	depsLabel string,
) int {
	if runtime.GOOS != "linux" {
		fmt.Fprintf(os.Stderr, "%s only supports Linux\n", cmdName)
		return 1
	}
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "%s requires root (sudo)\n", cmdName)
		return 1
	}

	fs := flag.NewFlagSet(cmdName, flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printUsage()
		fs.PrintDefaults()
	}
	cfgFlag := fs.String("config", "", "path to agent YAML")
	unitFlag := fs.String("unit", defaultSystemdUnit, "systemd unit name for ota-agent (without .service)")
	descFlag := fs.String("description", "Arenatech Agent", "ota-agent unit Description=")
	userFlag := fs.String("user", "arenatech", "ota-agent Service User= (empty to omit)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
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
		fmt.Fprintf(os.Stderr, "config file %s: %v\n", cfgPath, err)
		return 1
	}

	agentCfg, err := loadAgentConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config %s: %v\n", cfgPath, err)
		return 1
	}
	applyAgentDefaults(agentCfg)

	logger, err := setupLogger(agentCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logging: %v\n", err)
		return 1
	}
	defer logger.Close()

	ctx := context.Background()

	fmt.Println("==> [1/4] install-systemd")
	systemdArgs := []string{
		"-config=" + cfgPath,
		"-unit=" + strings.TrimSpace(*unitFlag),
		"-description=" + strings.TrimSpace(*descFlag),
		"-user=" + strings.TrimSpace(*userFlag),
	}
	if code := runInstallSystemd(systemdArgs); code != 0 {
		return code
	}

	fmt.Printf("==> [2/4] %s\n", depsLabel)
	depsCtx, depsCancel := context.WithTimeout(ctx, 30*time.Minute)
	out, err := runPlatformDeps(depsCtx, logger)
	depsCancel()
	if out != "" {
		fmt.Print(out)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", depsLabel, err)
		return 1
	}

	fmt.Println("==> [3/4] init-eth0.sh")
	ethCtx, ethCancel := context.WithTimeout(ctx, 2*time.Minute)
	out, err = runInitEth0Script(ethCtx, logger)
	ethCancel()
	if out != "" {
		fmt.Print(out)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "init-eth0 failed: %v\n", err)
		return 1
	}

	fmt.Println("==> [4/4] wifi-watchdog install (--no-apply)")
	wifiOut, err := runWiFiInstall(ctx, cfgPath, true, logger)
	if wifiOut != "" {
		fmt.Print(wifiOut)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "wifi-watchdog install failed: %v\n", err)
		return 1
	}

	fmt.Printf("%s complete\n", cmdName)
	return 0
}
