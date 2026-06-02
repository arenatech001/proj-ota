package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gitee.com/MM-Q/logrotatex"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// LoggingConfig 日志配置
type LoggingConfig struct {
	Level      string `yaml:"level" json:"level"`
	Format     string `yaml:"format" json:"format"`
	Output     string `yaml:"output" json:"output"`
	FilePath   string `yaml:"file_path" json:"file_path"`
	MaxSize    int    `yaml:"max_size" json:"max_size"`
	MaxBackups int    `yaml:"max_backups" json:"max_backups"`
	MaxAge     int    `yaml:"max_age" json:"max_age"`
	Compress   bool   `yaml:"compress" json:"compress"`
}

// Logger 日志管理器
type Logger struct {
	config     *LoggingConfig
	logger     zerolog.Logger
	fileWriter *logrotatex.LogRotateX
	mu         sync.RWMutex
}

// NewLogger 创建新的日志管理器
func NewLogger(config *LoggingConfig) (*Logger, error) {
	logLevel, err := zerolog.ParseLevel(config.Level)
	if err != nil {
		logLevel = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(logLevel)

	var logger zerolog.Logger
	var fileWriter *logrotatex.LogRotateX

	if config.Format == "console" || logLevel == zerolog.DebugLevel {
		if config.Output == "both" {
			var writers []io.Writer
			consoleWriter := zerolog.ConsoleWriter{
				Out:        os.Stdout,
				TimeFormat: time.RFC3339,
			}
			writers = append(writers, consoleWriter)
			if config.FilePath != "" {
				var err error
				fileWriter, err = createFileWriter(config)
				if err != nil {
					return nil, err
				}
				fileConsoleWriter := zerolog.ConsoleWriter{
					Out:        fileWriter,
					TimeFormat: time.RFC3339,
					NoColor:    true,
				}
				writers = append(writers, fileConsoleWriter)
			}
			var multiWriter io.Writer
			if len(writers) == 1 {
				multiWriter = writers[0]
			} else {
				multiWriter = zerolog.MultiLevelWriter(writers...)
			}
			logger = zerolog.New(multiWriter).With().Timestamp().Logger()
		} else {
			var writers []io.Writer
			switch config.Output {
			case "stdout":
				writers = append(writers, os.Stdout)
			case "stderr":
				writers = append(writers, os.Stderr)
			case "file":
				if config.FilePath != "" {
					var err error
					fileWriter, err = createFileWriter(config)
					if err != nil {
						return nil, err
					}
					writers = append(writers, fileWriter)
				}
			default:
				writers = append(writers, os.Stdout)
			}
			if len(writers) == 0 {
				writers = append(writers, os.Stdout)
			}
			var multiWriter io.Writer
			if len(writers) == 1 {
				multiWriter = writers[0]
			} else {
				multiWriter = zerolog.MultiLevelWriter(writers...)
			}
			consoleWriter := zerolog.ConsoleWriter{
				Out:        multiWriter,
				TimeFormat: time.RFC3339,
			}
			logger = zerolog.New(consoleWriter).With().Timestamp().Logger()
		}
	} else {
		var writers []io.Writer
		switch config.Output {
		case "stdout":
			writers = append(writers, os.Stdout)
		case "stderr":
			writers = append(writers, os.Stderr)
		case "file":
			if config.FilePath != "" {
				var err error
				fileWriter, err = createFileWriter(config)
				if err != nil {
					return nil, err
				}
				writers = append(writers, fileWriter)
			}
		case "both":
			writers = append(writers, os.Stdout)
			if config.FilePath != "" {
				var err error
				fileWriter, err = createFileWriter(config)
				if err != nil {
					return nil, err
				}
				writers = append(writers, fileWriter)
			}
		default:
			writers = append(writers, os.Stdout)
		}
		if len(writers) == 0 {
			writers = append(writers, os.Stdout)
		}
		var multiWriter io.Writer
		if len(writers) == 1 {
			multiWriter = writers[0]
		} else {
			multiWriter = zerolog.MultiLevelWriter(writers...)
		}
		logger = zerolog.New(multiWriter).With().Timestamp().Logger()
	}

	return &Logger{
		config:     config,
		logger:     logger,
		fileWriter: fileWriter,
	}, nil
}

func createFileWriter(config *LoggingConfig) (*logrotatex.LogRotateX, error) {
	if err := os.MkdirAll(filepath.Dir(config.FilePath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}
	fileWriter := logrotatex.NewLogRotateX(config.FilePath)
	fileWriter.MaxSize = config.MaxSize
	fileWriter.MaxFiles = config.MaxBackups
	fileWriter.MaxAge = config.MaxAge
	fileWriter.Compress = config.Compress
	fileWriter.LocalTime = true
	fileWriter.Async = true
	return fileWriter, nil
}

func (l *Logger) GetLogger() zerolog.Logger {
	return l.logger
}

func (l *Logger) SetGlobalLogger() {
	log.Logger = l.logger
}

func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fileWriter != nil {
		if err := l.fileWriter.Sync(); err != nil {
			return err
		}
		return l.fileWriter.Close()
	}
	return nil
}

func (l *Logger) Rotate() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fileWriter != nil {
		return l.fileWriter.Sync()
	}
	return nil
}

func GetDefaultConfig() *LoggingConfig {
	return &LoggingConfig{
		Level:      "info",
		Format:     "json",
		Output:     "both",
		FilePath:   "",
		MaxSize:    10,
		MaxBackups: 10,
		MaxAge:     28,
		Compress:   false,
	}
}

func ValidateConfig(config *LoggingConfig) error {
	validLevels := []string{"trace", "debug", "info", "warn", "error", "fatal", "panic"}
	validLevel := false
	for _, level := range validLevels {
		if config.Level == level {
			validLevel = true
			break
		}
	}
	if !validLevel {
		return fmt.Errorf("invalid log level: %s, valid levels are: %v", config.Level, validLevels)
	}
	if config.Format != "json" && config.Format != "console" {
		return fmt.Errorf("invalid log format: %s, valid formats are: json, console", config.Format)
	}
	validOutputs := []string{"stdout", "stderr", "file", "both"}
	validOutput := false
	for _, output := range validOutputs {
		if config.Output == output {
			validOutput = true
			break
		}
	}
	if !validOutput {
		return fmt.Errorf("invalid log output: %s, valid outputs are: %v", config.Output, validOutputs)
	}
	if config.Output == "file" || config.Output == "both" {
		if config.FilePath == "" {
			return fmt.Errorf("file path is required when output is file or both")
		}
	}
	if config.MaxSize <= 0 {
		return fmt.Errorf("max_size must be greater than 0")
	}
	if config.MaxBackups < 0 {
		return fmt.Errorf("max_backups must be non-negative")
	}
	if config.MaxAge < 0 {
		return fmt.Errorf("max_age must be non-negative")
	}
	return nil
}
