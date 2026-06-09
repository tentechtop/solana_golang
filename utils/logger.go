package utils

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

const (
	LogFormatJSON = "json"
	LogFormatText = "text"
)

// LoggerConfig 定义日志配置 + 统一系统日志初始化入口。
type LoggerConfig struct {
	Level     string
	Format    string
	AddSource bool
	Output    io.Writer
}

// NewLogger 创建结构化日志器 + 使用标准库 slog 降低依赖和维护成本。
func NewLogger(config LoggerConfig) (*slog.Logger, error) {
	level, err := ParseLogLevel(config.Level)
	if err != nil {
		return nil, err
	}

	output := config.Output
	if output == nil {
		output = os.Stdout
	}

	options := &slog.HandlerOptions{
		AddSource: config.AddSource,
		Level:     level,
	}

	switch normalizeLogFormat(config.Format) {
	case LogFormatJSON:
		return slog.New(slog.NewJSONHandler(output, options)), nil
	case LogFormatText:
		return slog.New(slog.NewTextHandler(output, options)), nil
	default:
		return nil, fmt.Errorf("utils: unsupported log format %q", config.Format)
	}
}

// InitDefaultLogger 初始化全局日志器 + 兼容直接使用 slog 默认日志的包。
func InitDefaultLogger(config LoggerConfig) (*slog.Logger, error) {
	logger, err := NewLogger(config)
	if err != nil {
		return nil, err
	}
	slog.SetDefault(logger)
	return logger, nil
}

// MustInitDefaultLogger 初始化全局日志器 + 启动阶段配置错误应立即失败。
func MustInitDefaultLogger(config LoggerConfig) *slog.Logger {
	logger, err := InitDefaultLogger(config)
	if err != nil {
		panic(err)
	}
	return logger
}

// ParseLogLevel 解析日志级别 + 拒绝非法输入避免静默降级。
func ParseLogLevel(level string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("utils: unsupported log level %q", level)
	}
}

// LoggerFromEnv 从环境变量创建日志器 + 支持部署环境零代码调整日志策略。
func LoggerFromEnv() (*slog.Logger, error) {
	return NewLogger(LoggerConfig{
		Level:     os.Getenv("LOG_LEVEL"),
		Format:    os.Getenv("LOG_FORMAT"),
		AddSource: strings.EqualFold(os.Getenv("LOG_ADD_SOURCE"), "true"),
	})
}

func normalizeLogFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		return LogFormatJSON
	}
	return format
}

// EnsureLogger 兜底日志实例 + 避免关键路径因空指针丢失日志。
func EnsureLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	if slog.Default() != nil {
		return slog.Default()
	}
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}
