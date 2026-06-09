package utils

import (
	"bytes"
	"log/slog"
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
