package utils

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewLoggerUsesJSONByDefault(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewLogger(LoggerConfig{Output: &output})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	logger.Info("logger ready", slog.String("component", "test"))

	logLine := output.String()
	if !strings.Contains(logLine, `"msg":"logger ready"`) {
		t.Fatalf("log line = %q, want json message", logLine)
	}
	if !strings.Contains(logLine, `"component":"test"`) {
		t.Fatalf("log line = %q, want structured field", logLine)
	}
}

func TestNewLoggerRejectsInvalidLevel(t *testing.T) {
	_, err := NewLogger(LoggerConfig{Level: "verbose"})
	if err == nil {
		t.Fatal("NewLogger() error = nil, want invalid level error")
	}
}

func TestNewLoggerSupportsTextFormat(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewLogger(LoggerConfig{
		Format: LogFormatText,
		Output: &output,
	})
	if err != nil {
		t.Fatalf("NewLogger(text) error = %v", err)
	}

	logger.Warn("logger text", slog.String("component", "test"))

	logLine := output.String()
	if !strings.Contains(logLine, "level=WARN") {
		t.Fatalf("log line = %q, want warn level", logLine)
	}
	if !strings.Contains(logLine, "component=test") {
		t.Fatalf("log line = %q, want component field", logLine)
	}
}

func TestNewLoggerRejectsInvalidFormat(t *testing.T) {
	_, err := NewLogger(LoggerConfig{Format: "xml"})
	if err == nil {
		t.Fatal("NewLogger() error = nil, want invalid format error")
	}
}

func TestOpenLogFileCreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "app.log")
	file, err := OpenLogFile(path)
	if err != nil {
		t.Fatalf("OpenLogFile() error = %v", err)
	}
	defer file.Close()

	if _, err := file.WriteString("ready\n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "ready") {
		t.Fatalf("log file = %q, want written content", string(data))
	}
}

func TestOpenLogFileRejectsEmptyPath(t *testing.T) {
	if _, err := OpenLogFile(" "); err == nil {
		t.Fatal("OpenLogFile() error = nil, want empty path error")
	}
}

func TestOpenLogFileReturnsOpenError(t *testing.T) {
	if _, err := OpenLogFile(string([]byte{0})); err == nil {
		t.Fatal("OpenLogFile() error = nil, want invalid path error")
	}
}

func TestInitDefaultLoggerSetsSlogDefault(t *testing.T) {
	var output bytes.Buffer
	logger, err := InitDefaultLogger(LoggerConfig{Output: &output})
	if err != nil {
		t.Fatalf("InitDefaultLogger() error = %v", err)
	}

	slog.Info("default logger ready")

	if logger != slog.Default() {
		t.Fatal("slog.Default() was not updated")
	}
	if !strings.Contains(output.String(), `"msg":"default logger ready"`) {
		t.Fatalf("default output = %q, want json message", output.String())
	}
}

func TestInitDefaultLoggerRejectsInvalidConfig(t *testing.T) {
	if _, err := InitDefaultLogger(LoggerConfig{Level: "trace"}); err == nil {
		t.Fatal("InitDefaultLogger() error = nil, want invalid level error")
	}
}

func TestMustInitDefaultLoggerPanicsOnInvalidConfig(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustInitDefaultLogger() did not panic")
		}
	}()

	MustInitDefaultLogger(LoggerConfig{Format: "bad"})
}

func TestMustInitDefaultLoggerReturnsLogger(t *testing.T) {
	var output bytes.Buffer
	logger := MustInitDefaultLogger(LoggerConfig{Output: &output})
	if logger == nil {
		t.Fatal("MustInitDefaultLogger() returned nil")
	}
	logger.Info("must logger ready")
	if !strings.Contains(output.String(), "must logger ready") {
		t.Fatalf("output = %q, want message", output.String())
	}
}

func TestParseLogLevelAcceptsSupportedLevels(t *testing.T) {
	cases := []string{"", "debug", "info", "warn", "warning", "error"}
	for _, value := range cases {
		if _, err := ParseLogLevel(value); err != nil {
			t.Fatalf("ParseLogLevel(%q) error = %v", value, err)
		}
	}
}

func TestLoggerFromEnvUsesEnvironment(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("LOG_ADD_SOURCE", "true")

	logger, err := LoggerFromEnv()
	if err != nil {
		t.Fatalf("LoggerFromEnv() error = %v", err)
	}
	if logger == nil {
		t.Fatal("LoggerFromEnv() returned nil logger")
	}
}

func TestEnsureLoggerReturnsFallback(t *testing.T) {
	if EnsureLogger(nil) == nil {
		t.Fatal("EnsureLogger(nil) returned nil")
	}
}

func TestEnsureLoggerReturnsProvidedLogger(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	if EnsureLogger(logger) != logger {
		t.Fatal("EnsureLogger() did not return provided logger")
	}
}

func TestNormalizeLogFormat(t *testing.T) {
	if got := normalizeLogFormat(" "); got != LogFormatJSON {
		t.Fatalf("normalizeLogFormat(empty) = %q, want json", got)
	}
	if got := normalizeLogFormat("bad"); got != "bad" {
		t.Fatalf("normalizeLogFormat(bad) = %q, want original", got)
	}
}
