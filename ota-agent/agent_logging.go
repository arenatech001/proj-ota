package main

import (
	"fmt"
	"path/filepath"
	"strings"

	agentlog "ota-agent/internal/logger"

	"github.com/rs/zerolog/log"
)

// Logger wraps zerolog with printf-style helpers used across ota-agent.
type Logger struct {
	core *agentlog.Logger
}

func setupLogger(cfg *AgentConfig) (*Logger, error) {
	lc := agentLoggingConfig(cfg)
	if err := agentlog.ValidateConfig(lc); err != nil {
		return nil, fmt.Errorf("invalid logging config: %w", err)
	}
	core, err := agentlog.NewLogger(lc)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}
	core.SetGlobalLogger()
	log.Info().
		Str("level", lc.Level).
		Str("format", lc.Format).
		Str("output", lc.Output).
		Str("file_path", lc.FilePath).
		Msg("Logging configured")
	return &Logger{core: core}, nil
}

func (l *Logger) Close() error {
	if l == nil || l.core == nil {
		return nil
	}
	return l.core.Close()
}

func (l *Logger) Info(format string, v ...interface{}) {
	log.Info().Msgf(format, v...)
}

func (l *Logger) Warn(format string, v ...interface{}) {
	log.Warn().Msgf(format, v...)
}

func (l *Logger) Error(format string, v ...interface{}) {
	log.Error().Msgf(format, v...)
}

func agentLoggingConfig(cfg *AgentConfig) *agentlog.LoggingConfig {
	def := agentlog.GetDefaultConfig()
	def.Level = "info"
	def.Format = "json"
	def.Output = "both"
	def.MaxSize = 10
	def.MaxBackups = 10
	def.MaxAge = 28
	def.Compress = false

	root, err := agentInstallRoot()
	if err == nil {
		def.FilePath = filepath.Join(root, "logs", "agent.log")
	}

	if cfg == nil {
		return def
	}

	l := cfg.Logging
	if strings.TrimSpace(l.Level) != "" {
		def.Level = strings.TrimSpace(l.Level)
	}
	if strings.TrimSpace(l.Format) != "" {
		def.Format = strings.TrimSpace(l.Format)
	}
	if strings.TrimSpace(l.Output) != "" {
		def.Output = strings.TrimSpace(l.Output)
	}
	if strings.TrimSpace(l.FilePath) != "" {
		def.FilePath = strings.TrimSpace(l.FilePath)
	}
	if l.MaxSize > 0 {
		def.MaxSize = l.MaxSize
	}
	if l.MaxBackups > 0 {
		def.MaxBackups = l.MaxBackups
	}
	if l.MaxAge > 0 {
		def.MaxAge = l.MaxAge
	}
	def.Compress = l.Compress

	normalizeAgentLoggingPaths(def)
	return def
}

func normalizeAgentLoggingPaths(l *agentlog.LoggingConfig) {
	if l == nil {
		return
	}
	root, err := agentInstallRoot()
	if err != nil {
		return
	}

	resolveRel := func(p string) string {
		p = strings.TrimSpace(p)
		if p == "" {
			return p
		}
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		if strings.HasPrefix(p, "../") || strings.HasPrefix(p, "./") {
			if exeDir, err := getExecutableDir(); err == nil {
				return filepath.Clean(filepath.Join(exeDir, p))
			}
		}
		return filepath.Clean(filepath.Join(root, p))
	}

	defaultLog := filepath.Join(root, "logs", "agent.log")
	l.FilePath = resolveRel(l.FilePath)
	if strings.TrimSpace(l.FilePath) == "" {
		l.FilePath = defaultLog
	}
}
