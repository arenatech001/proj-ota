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

func printInitUsage() {
	fmt.Fprintf(os.Stderr, `Usage:
  %s init [-config=PATH] [-unit=NAME] [-description=TEXT] [-user=USER]

首次部署：安装 arenatech-agent systemd、运行 install-deps-rpi.sh、init-eth0.sh，
写入热点模式到 agent.yaml，并执行 wifi-watchdog install --config ... --no-apply 注册定时器。

需要 Linux、root，且 -config 指向的 agent.yaml 须已存在（install-systemd 会引用该文件）。

`, filepath.Base(os.Args[0]))
}

func runInit(args []string) int {
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "init only supports Linux")
		return 1
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "init requires root (sudo)")
		return 1
	}

	fs := flag.NewFlagSet("init", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printInitUsage()
		fs.PrintDefaults()
	}
	cfgFlag := fs.String("config", "/home/arenatech/agent/agent.yaml", "path to agent YAML")
	unitFlag := fs.String("unit", defaultSystemdUnit, "systemd unit name for ota-agent (without .service)")
	descFlag := fs.String("description", "Arenatech Agent", "ota-agent unit Description=")
	userFlag := fs.String("user", "root", "ota-agent Service User= (empty to omit)")
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

	logger := newLogger()
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

	fmt.Println("==> [2/4] install-deps-rpi.sh")
	depsCtx, depsCancel := context.WithTimeout(ctx, 30*time.Minute)
	out, err := runInstallDepsRpiScript(depsCtx, logger)
	depsCancel()
	if out != "" {
		fmt.Print(out)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "install-deps-rpi failed: %v\n", err)
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
	wifiOut, err := runWiFiHotspotInit(ctx, cfgPath, logger)
	if wifiOut != "" {
		fmt.Print(wifiOut)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "wifi hotspot init failed: %v\n", err)
		return 1
	}

	fmt.Println("init complete")
	return 0
}

// runWiFiHotspotInit sets network.wifi_mode=hotspot in agent.yaml and runs wifi-watchdog install.
func runWiFiHotspotInit(ctx context.Context, cfgPath string, logger *Logger) (string, error) {
	cfg, err := loadAgentConfig(cfgPath)
	if err != nil {
		return "", err
	}
	applyAgentDefaults(cfg)
	cfg.Network.WiFiMode = "hotspot"
	fillHotspotNetwork(&cfg.Network)
	if err := saveAgentConfigAtomic(cfgPath, cfg); err != nil {
		return "", err
	}
	logger.Info("wifi: saved hotspot mode in %s (ssid=%s)", cfgPath, cfg.Network.SSID)
	return runWiFiInstall(ctx, cfgPath, true, logger)
}
