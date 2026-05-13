package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	maxExecutableLen = 4096
	maxArgCount      = 256
	maxArgLen        = 8192
	maxProcessIDLen  = 128
)

// AgentConfig is the single YAML file for ota-agent (only `-config` points here).
type AgentConfig struct {
	ConfigURL       string        `yaml:"config_url"`
	VersionFile     string        `yaml:"version_file"`
	AgentID         string        `yaml:"agent_id"`
	Daemon          *bool         `yaml:"daemon"` // nil = true
	CheckInterval   time.Duration `yaml:"check_interval"`
	HTTPTimeout     time.Duration `yaml:"http_timeout"`
	DownloadTimeout time.Duration `yaml:"download_timeout"`
	MaxRetries      int           `yaml:"max_retries"`

	LogUpload LogUploadConfig `yaml:"log_upload"`

	AdminUsername string `yaml:"admin_username" json:"admin_username"`
	AdminPassword string `yaml:"admin_password" json:"-"`
	AdminListen   string `yaml:"admin_listen" json:"admin_listen"` // e.g. 127.0.0.1:9001, empty = off

	Processes []ManagedProcessConfig `yaml:"processes" json:"processes"`
	Network   AdminNetworkConfig     `yaml:"network" json:"network"`
}

// LogUploadConfig remote log job loop (all from YAML).
type LogUploadConfig struct {
	Enabled        *bool         `yaml:"enabled"` // nil = false
	BaseURL        string        `yaml:"base_url"`
	Location       string        `yaml:"location"`
	ScanDir        string        `yaml:"scan_dir"`
	Glob           string        `yaml:"glob"`
	ServerGlob     string        `yaml:"server_glob"`
	PollInterval   time.Duration `yaml:"poll_interval"`
	UploadTimeout  time.Duration `yaml:"upload_timeout"`
	MaxUploadBytes int64         `yaml:"max_upload_bytes"`
	ReportRetries  int           `yaml:"report_retries"`
}

// AdminNetworkConfig WiFi / hotspot hints for tools script.
type AdminNetworkConfig struct {
	WiFiMode string `yaml:"wifi_mode" json:"wifi_mode"`
	SSID     string `yaml:"wifi_ssid" json:"wifi_ssid"`
	PSK      string `yaml:"wifi_psk" json:"-"`
	Iface    string `yaml:"wifi_iface,omitempty" json:"wifi_iface,omitempty"`
}

// ManagedProcessConfig one supervised child process.
type ManagedProcessConfig struct {
	ID         string   `yaml:"id" json:"id"`
	Executable string   `yaml:"executable" json:"executable"`
	Args       []string `yaml:"args" json:"args"`
	Enabled    bool     `yaml:"enabled" json:"enabled"`
	WorkDir    string   `yaml:"work_dir,omitempty" json:"work_dir,omitempty"`
}

func daemonEffective(c *AgentConfig) bool {
	if c == nil || c.Daemon == nil {
		return true
	}
	return *c.Daemon
}

func logUploadEnabled(c *AgentConfig) bool {
	if c == nil || c.LogUpload.Enabled == nil {
		return false
	}
	return *c.LogUpload.Enabled
}

func defaultConfigPath() (string, error) {
	dir, err := getExecutableDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agent.yaml"), nil
}

func loadAgentConfig(path string) (*AgentConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg AgentConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

func applyAgentDefaults(c *AgentConfig) {
	if c == nil {
		return
	}
	if c.CheckInterval == 0 {
		c.CheckInterval = 5 * time.Minute
	}
	if c.HTTPTimeout == 0 {
		c.HTTPTimeout = 30 * time.Second
	}
	if c.DownloadTimeout == 0 {
		c.DownloadTimeout = 30 * time.Minute
	}
	if c.MaxRetries == 0 {
		c.MaxRetries = 3
	}
	if strings.TrimSpace(c.VersionFile) == "" {
		c.VersionFile = "version"
	}
	if strings.TrimSpace(c.LogUpload.ScanDir) == "" {
		c.LogUpload.ScanDir = "/var/log"
	}
	if strings.TrimSpace(c.LogUpload.Glob) == "" {
		c.LogUpload.Glob = "*.tar.gz"
	}
	if c.LogUpload.PollInterval == 0 {
		c.LogUpload.PollInterval = time.Minute
	}
	if c.LogUpload.UploadTimeout == 0 {
		c.LogUpload.UploadTimeout = 30 * time.Minute
	}
	if c.LogUpload.MaxUploadBytes == 0 {
		c.LogUpload.MaxUploadBytes = 500 * 1024 * 1024
	}
	if c.LogUpload.ReportRetries == 0 {
		c.LogUpload.ReportRetries = 3
	}
}

func resolveVersionFilePath(cfg *AgentConfig) (string, error) {
	v := strings.TrimSpace(cfg.VersionFile)
	if filepath.IsAbs(v) {
		return filepath.Clean(v), nil
	}
	return getExecutableRelativePath(v)
}

func saveAgentConfigAtomic(path string, cfg *AgentConfig) error {
	if err := validateAgentConfig(cfg); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func validateAgentConfig(cfg *AgentConfig) error {
	if cfg == nil {
		return errors.New("nil config")
	}
	if strings.TrimSpace(cfg.ConfigURL) == "" {
		return errors.New("config_url is required")
	}
	ids := map[string]struct{}{}
	for i, p := range cfg.Processes {
		id := strings.TrimSpace(p.ID)
		if id == "" {
			return fmt.Errorf("processes[%d]: id is required", i)
		}
		if len(id) > maxProcessIDLen {
			return fmt.Errorf("processes[%d]: id too long", i)
		}
		if _, dup := ids[id]; dup {
			return fmt.Errorf("duplicate process id: %s", id)
		}
		ids[id] = struct{}{}
		exe := strings.TrimSpace(p.Executable)
		if p.Enabled && exe == "" {
			return fmt.Errorf("processes[%d]: executable required when enabled", i)
		}
		if len(exe) > maxExecutableLen {
			return fmt.Errorf("processes[%d]: executable too long", i)
		}
		if len(p.Args) > maxArgCount {
			return fmt.Errorf("processes[%d]: too many args", i)
		}
		for j, a := range p.Args {
			if len(a) > maxArgLen {
				return fmt.Errorf("processes[%d].args[%d]: too long", i, j)
			}
		}
		if len(p.WorkDir) > maxExecutableLen {
			return fmt.Errorf("processes[%d]: work_dir too long", i)
		}
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Network.WiFiMode))
	if mode != "" && mode != "sta" && mode != "hotspot" {
		return fmt.Errorf("network.wifi_mode must be sta or hotspot, got %q", cfg.Network.WiFiMode)
	}
	if addr := strings.TrimSpace(cfg.AdminListen); addr != "" {
		if strings.TrimSpace(cfg.AdminUsername) == "" {
			return errors.New("admin_listen is set but admin_username is empty")
		}
	}
	return nil
}
