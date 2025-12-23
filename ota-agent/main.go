package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// FileUpdate represents a single file update
type FileUpdate struct {
	Name    string `yaml:"name"`    // file name/identifier
	URL     string `yaml:"url"`     // download URL
	SHA256  string `yaml:"sha256"`  // file sha256 hex
	Target  string `yaml:"target"`  // target path to replace
	Version string `yaml:"version"` // file version (optional, defaults to config version)
}

// Config represents the structure of version.yaml on the server
type Config struct {
	Version    string       `yaml:"version"`     // e.g. "1.2.0"
	Files      []FileUpdate `yaml:"files"`       // list of files to update
	RestartCmd string       `yaml:"restart_cmd"` // optional: global restart command after all updates
}

// Logger wraps log functions for structured logging
type Logger struct {
	info  *log.Logger
	warn  *log.Logger
	error *log.Logger
}

func newLogger() *Logger {
	return &Logger{
		info:  log.New(os.Stdout, "[INFO] ", log.LstdFlags),
		warn:  log.New(os.Stderr, "[WARN] ", log.LstdFlags),
		error: log.New(os.Stderr, "[ERROR] ", log.LstdFlags),
	}
}

func (l *Logger) Info(format string, v ...interface{}) {
	l.info.Printf(format, v...)
}

func (l *Logger) Warn(format string, v ...interface{}) {
	l.warn.Printf(format, v...)
}

func (l *Logger) Error(format string, v ...interface{}) {
	l.error.Printf(format, v...)
}

// retryHTTPRequest executes an HTTP request with retry logic
func retryHTTPRequest(maxRetries int, delay time.Duration, fn func() (*http.Response, error)) (*http.Response, error) {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			time.Sleep(delay)
		}
		resp, err := fn()
		if err == nil && resp.StatusCode == 200 {
			return resp, nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		lastErr = err
		if err == nil {
			lastErr = fmt.Errorf("bad status %d", resp.StatusCode)
		}
	}
	return nil, fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}

func fetchConfig(url string, agentID string, timeout time.Duration, maxRetries int, logger *Logger) (*Config, error) {
	client := &http.Client{Timeout: timeout}
	var resp *http.Response
	var err error

	if maxRetries > 1 {
		resp, err = retryHTTPRequest(maxRetries, 2*time.Second, func() (*http.Response, error) {
			req, e := http.NewRequest("GET", url, nil)
			if e != nil {
				return nil, e
			}
			if agentID != "" {
				req.Header.Set("X-Agent-ID", agentID)
			}
			r, e := client.Do(req)
			if e != nil {
				return nil, e
			}
			if r.StatusCode != 200 {
				return r, fmt.Errorf("status %d", r.StatusCode)
			}
			return r, nil
		})
	} else {
		req, e := http.NewRequest("GET", url, nil)
		if e != nil {
			return nil, fmt.Errorf("create request: %w", e)
		}
		if agentID != "" {
			req.Header.Set("X-Agent-ID", agentID)
		}
		resp, err = client.Do(req)
	}

	if err != nil {
		return nil, fmt.Errorf("fetch config: %w", err)
	}
	defer resp.Body.Close()

	var cfg Config
	dec := yaml.NewDecoder(resp.Body)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode yaml: %w", err)
	}
	return &cfg, nil
}

func readLocalVersion(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // treat as no version
		}
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func writeLocalVersion(path, v string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(v+"\n"), 0644)
}

// progressWriter wraps io.Writer to show download progress
type progressWriter struct {
	writer    io.Writer
	total     int64
	written   int64
	lastPrint time.Time
	logger    *Logger
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.writer.Write(p)
	pw.written += int64(n)

	// Print progress every 500ms
	now := time.Now()
	if now.Sub(pw.lastPrint) >= 500*time.Millisecond || pw.written == pw.total {
		if pw.total > 0 {
			percent := float64(pw.written) / float64(pw.total) * 100
			pw.logger.Info("downloaded %d/%d bytes (%.1f%%)", pw.written, pw.total, percent)
		} else {
			pw.logger.Info("downloaded %d bytes", pw.written)
		}
		pw.lastPrint = now
	}
	return n, err
}

func downloadFile(url, dest string, agentID string, timeout time.Duration, maxRetries int, logger *Logger) error {
	client := &http.Client{Timeout: timeout}
	var resp *http.Response
	var err error

	if maxRetries > 1 {
		resp, err = retryHTTPRequest(maxRetries, 2*time.Second, func() (*http.Response, error) {
			req, e := http.NewRequest("GET", url, nil)
			if e != nil {
				return nil, e
			}
			if agentID != "" {
				req.Header.Set("X-Agent-ID", agentID)
			}
			r, e := client.Do(req)
			if e != nil {
				return nil, e
			}
			if r.StatusCode != 200 {
				return r, fmt.Errorf("status %d", r.StatusCode)
			}
			return r, nil
		})
	} else {
		req, e := http.NewRequest("GET", url, nil)
		if e != nil {
			return fmt.Errorf("create request: %w", e)
		}
		if agentID != "" {
			req.Header.Set("X-Agent-ID", agentID)
		}
		resp, err = client.Do(req)
	}

	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	defer f.Close()

	// Create progress writer
	pw := &progressWriter{
		writer:    f,
		total:     resp.ContentLength,
		written:   0,
		lastPrint: time.Now(),
		logger:    logger,
	}

	_, err = io.Copy(pw, resp.Body)
	if err != nil {
		return fmt.Errorf("write dest: %w", err)
	}

	if pw.total > 0 {
		logger.Info("download complete: %d bytes", pw.written)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func runCommand(cmdline string) error {
	parts := strings.Fields(cmdline)
	if len(parts) == 0 {
		return nil
	}
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// checkWritePermission checks if we can write to the target directory
// If the directory doesn't exist, it will be created
func checkWritePermission(targetPath string) error {
	targetDir := filepath.Dir(targetPath)
	// Check if directory exists, create if not
	info, err := os.Stat(targetDir)
	if err != nil {
		if os.IsNotExist(err) {
			// Create directory if it doesn't exist
			if err := os.MkdirAll(targetDir, 0755); err != nil {
				return fmt.Errorf("failed to create target directory %s: %w", targetDir, err)
			}
			// Re-stat to get directory info
			info, err = os.Stat(targetDir)
			if err != nil {
				return fmt.Errorf("cannot access target directory %s: %w", targetDir, err)
			}
		} else {
			return fmt.Errorf("cannot access target directory %s: %w", targetDir, err)
		}
	}
	if !info.IsDir() {
		return fmt.Errorf("target directory %s is not a directory", targetDir)
	}
	// Try to create a test file to verify write permission
	testFile := filepath.Join(targetDir, ".ota-agent-write-test")
	f, err := os.Create(testFile)
	if err != nil {
		return fmt.Errorf("no write permission to %s: %w", targetDir, err)
	}
	f.Close()
	os.Remove(testFile)
	return nil
}

func atomicReplace(newPath, targetPath string, logger *Logger) (backupPath string, err error) {
	// ensure same dir for atomic rename
	targetDir := filepath.Dir(targetPath)
	newDir := filepath.Dir(newPath)
	if targetDir != newDir {
		// move new file into target dir first
		tmp := filepath.Join(targetDir, filepath.Base(newPath))
		if err := os.Rename(newPath, tmp); err != nil {
			return "", fmt.Errorf("move into same dir: %w", err)
		}
		newPath = tmp
	}
	backup := targetPath + ".bak"
	// backup existing file if exists
	if _, err := os.Stat(targetPath); err == nil {
		// remove previous .bak if exists
		_ = os.Remove(backup)
		if err := os.Rename(targetPath, backup); err != nil {
			return "", fmt.Errorf("backup existing: %w", err)
		}
	} else {
		backup = ""
	}
	// atomic replace
	if err := os.Rename(newPath, targetPath); err != nil {
		// try to restore backup if rename failed
		if backup != "" {
			_ = os.Rename(backup, targetPath)
		}
		return backup, fmt.Errorf("rename new->target: %w", err)
	}
	// ensure executable bit
	if err := os.Chmod(targetPath, 0755); err != nil {
		// log but not fatal
		if logger != nil {
			logger.Warn("chmod failed: %v", err)
		}
	}
	return backup, nil
}

// validateConfig validates the configuration for correctness
func validateConfig(cfg *Config) error {
	if cfg.Version == "" {
		return fmt.Errorf("version is required")
	}
	// Version format validation (basic: should not be empty and reasonable length)
	if len(cfg.Version) > 100 {
		return fmt.Errorf("version too long (max 100 chars)")
	}

	// Validate files array
	if len(cfg.Files) == 0 {
		return fmt.Errorf("files array is required and cannot be empty")
	}

	sha256Regex := regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
	for i, file := range cfg.Files {
		if file.Name == "" {
			return fmt.Errorf("files[%d].name is required", i)
		}
		if file.URL == "" {
			return fmt.Errorf("files[%d].url is required", i)
		}
		if file.Target == "" {
			return fmt.Errorf("files[%d].target is required", i)
		}
		if file.SHA256 == "" {
			return fmt.Errorf("files[%d].sha256 is required", i)
		}
		// URL validation
		if _, err := url.Parse(file.URL); err != nil {
			return fmt.Errorf("files[%d].url is invalid: %w", i, err)
		}
		if !strings.HasPrefix(file.URL, "http://") && !strings.HasPrefix(file.URL, "https://") {
			return fmt.Errorf("files[%d].url must be http:// or https://", i)
		}
		// SHA256 format validation
		if !sha256Regex.MatchString(file.SHA256) {
			return fmt.Errorf("files[%d].sha256 must be 64 hex characters", i)
		}
	}

	return nil
}

// updateFile updates a single file
func updateFile(file FileUpdate, fileVersion string, versionFile string, restartCmd string, agentID string, timeout time.Duration, maxRetries int, logger *Logger) (bool, error) {
	logger.Info("updating file %s (target: %s)", file.Name, file.Target)

	// Check write permission
	if err := checkWritePermission(file.Target); err != nil {
		return false, fmt.Errorf("permission check failed for %s: %w", file.Target, err)
	}

	// Download
	tmpDir := filepath.Dir(file.Target)
	tmpFile := filepath.Join(tmpDir, fmt.Sprintf(".tmp-%s-%d", file.Name, time.Now().Unix()))
	logger.Info("downloading %s to %s", file.Name, tmpFile)
	if err := downloadFile(file.URL, tmpFile, agentID, timeout, maxRetries, logger); err != nil {
		_ = os.Remove(tmpFile)
		return false, fmt.Errorf("download error: %w", err)
	}

	// Verify checksum
	logger.Info("verifying checksum for %s...", file.Name)
	sum, err := fileSHA256(tmpFile)
	if err != nil {
		_ = os.Remove(tmpFile)
		return false, fmt.Errorf("checksum error: %w", err)
	}
	if !strings.EqualFold(sum, file.SHA256) {
		_ = os.Remove(tmpFile)
		return false, fmt.Errorf("sha256 mismatch: got=%s want=%s", sum, file.SHA256)
	}
	logger.Info("checksum verified for %s", file.Name)

	// Atomic replace
	logger.Info("replacing %s...", file.Target)
	backup, err := atomicReplace(tmpFile, file.Target, logger)
	if err != nil {
		_ = os.Remove(tmpFile)
		return false, fmt.Errorf("replace error: %w", err)
	}
	if backup != "" {
		logger.Info("replaced %s (backup=%s)", file.Target, backup)
	} else {
		logger.Info("replaced %s (no previous version)", file.Target)
	}

	// Restart if needed
	if restartCmd != "" {
		logger.Info("restarting after %s update...", file.Name)
		if err := runCommand(restartCmd); err != nil {
			logger.Error("restart failed after %s update: %v", file.Name, err)
			// Rollback
			if backup != "" {
				logger.Info("attempting rollback for %s", file.Name)
				if err := os.Rename(backup, file.Target); err != nil {
					logger.Error("rollback failed for %s: %v", file.Name, err)
				} else {
					logger.Info("rollback successful for %s", file.Name)
					_ = runCommand(restartCmd)
				}
			}
			return false, fmt.Errorf("restart failed: %w", err)
		}
	}

	return true, nil
}

// checkUpdate checks for updates and applies them
func checkUpdate(cfgURL string, versionFile string, agentID string, timeout time.Duration, maxRetries int, logger *Logger) error {
	logger.Info("checking for updates from %s", cfgURL)

	remoteCfg, err := fetchConfig(cfgURL, agentID, timeout, maxRetries, logger)
	if err != nil {
		return fmt.Errorf("fetch config: %w", err)
	}

	// Validate configuration
	if err := validateConfig(remoteCfg); err != nil {
		return fmt.Errorf("invalid remote config: %w", err)
	}

	// Read local version
	localVer, err := readLocalVersion(versionFile)
	if err != nil {
		logger.Warn("read local version error: %v, treating as no version", err)
		localVer = ""
	}

	logger.Info("remote version=%s, local version=%s", remoteCfg.Version, localVer)

	// Check if update needed
	if remoteCfg.Version == localVer {
		logger.Info("versions equal, no update needed")
		return nil
	}

	// Get files from config
	files := remoteCfg.Files

	// Update each file
	updated := false
	var lastErr error
	for _, file := range files {
		fileVersion := file.Version
		if fileVersion == "" {
			fileVersion = remoteCfg.Version
		}

		success, err := updateFile(file, fileVersion, versionFile, remoteCfg.RestartCmd, agentID, timeout, maxRetries, logger)
		if err != nil {
			logger.Error("failed to update %s: %v", file.Name, err)
			lastErr = err
			continue
		}
		if success {
			updated = true
		}
	}

	// Global restart command after all updates
	if updated && remoteCfg.RestartCmd != "" {
		logger.Info("running global restart_cmd: %s", remoteCfg.RestartCmd)
		if err := runCommand(remoteCfg.RestartCmd); err != nil {
			logger.Error("global restart failed: %v", err)
			return fmt.Errorf("global restart failed: %w", err)
		}
	}

	// Update main version file
	if updated {
		if err := writeLocalVersion(versionFile, remoteCfg.Version); err != nil {
			logger.Warn("write version file error: %v (non-fatal)", err)
		} else {
			logger.Info("version file updated to %s", remoteCfg.Version)
		}
		logger.Info("update to %s complete", remoteCfg.Version)
	}

	if lastErr != nil {
		return lastErr
	}

	return nil
}

func main() {
	// flags / env
	cfgURL := flag.String("config-url", "", "URL to version.yaml (required)")
	versionFile := flag.String("version-file", "version", "local version file path")
	agentID := flag.String("agent-id", "", "Agent identifier (sent as X-Agent-ID header)")
	timeout := flag.Duration("timeout", 30*time.Second, "http timeout")
	maxRetries := flag.Int("max-retries", 3, "maximum number of retries for HTTP requests")
	checkInterval := flag.Duration("check-interval", 5*time.Minute, "check interval for daemon mode")
	daemon := flag.Bool("daemon", true, "run as daemon (default: true)")
	flag.Parse()

	logger := newLogger()

	if *cfgURL == "" {
		logger.Error("config-url is required")
		os.Exit(2)
	}

	// Ensure version file directory exists
	if err := os.MkdirAll(filepath.Dir(*versionFile), 0755); err != nil {
		logger.Error("failed to create version file directory: %v", err)
		os.Exit(1)
	}

	// Run once or as daemon
	if !*daemon {
		// Run once
		if err := checkUpdate(*cfgURL, *versionFile, *agentID, *timeout, *maxRetries, logger); err != nil {
			logger.Error("update check failed: %v", err)
			os.Exit(1)
		}
		return
	}

	// Daemon mode
	logger.Info("starting OTA agent daemon")
	logger.Info("config URL: %s", *cfgURL)
	logger.Info("agent ID: %s", *agentID)
	logger.Info("check interval: %v", *checkInterval)
	logger.Info("version file: %s", *versionFile)

	// 设置环境变量agentID=server-001
	os.Setenv("AGENT_ID", *agentID)

	// Handle signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Run initial check
	if err := checkUpdate(*cfgURL, *versionFile, *agentID, *timeout, *maxRetries, logger); err != nil {
		logger.Error("initial update check failed: %v", err)
	}

	// Periodic check
	ticker := time.NewTicker(*checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := checkUpdate(*cfgURL, *versionFile, *agentID, *timeout, *maxRetries, logger); err != nil {
				logger.Error("update check failed: %v", err)
			}
		case sig := <-sigChan:
			logger.Info("received signal %v, shutting down...", sig)
			return
		}
	}
}
