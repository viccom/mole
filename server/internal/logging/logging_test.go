package logging

import (
	"bytes"
	"log/slog"
	"testing"

	"moleAgent_Serv/internal/config"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},        // default
		{"invalid", slog.LevelInfo}, // default
	}

	for _, tc := range tests {
		got := parseLevel(tc.input)
		if got != tc.want {
			t.Errorf("parseLevel(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestInitConsoleOnly(t *testing.T) {
	cfg := config.LoggingConfig{
		Level:  "info",
		Format: "text",
		Console: config.ConsoleLogConfig{
			Enabled: true,
			Color:   false,
		},
		File: config.FileLogConfig{
			Enabled: false,
		},
	}

	logger := Init(cfg)
	if logger == nil {
		t.Fatal("Init returned nil")
	}

	// 验证默认 logger 已设置
	if slog.Default() != logger {
		t.Error("Init should set default logger")
	}
}

func TestMultiHandler(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	h1 := slog.NewTextHandler(&buf1, &slog.HandlerOptions{Level: slog.LevelInfo})
	h2 := slog.NewTextHandler(&buf2, &slog.HandlerOptions{Level: slog.LevelInfo})

	mh := newMultiHandler(h1, h2)

	if !mh.Enabled(nil, slog.LevelInfo) {
		t.Error("multiHandler should be enabled for INFO")
	}
	if mh.Enabled(nil, slog.LevelDebug) {
		t.Error("multiHandler should not be enabled for DEBUG when both handlers are INFO")
	}

	// Handle 应该写入两个 handler
	logger := slog.New(mh)
	logger.Info("test message")

	if buf1.Len() == 0 {
		t.Error("handler 1 should have output")
	}
	if buf2.Len() == 0 {
		t.Error("handler 2 should have output")
	}
}

func TestColorTextHandler(t *testing.T) {
	var buf bytes.Buffer
	h := newColorTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	logger := slog.New(h)
	logger.Info("test message", "key", "value")

	output := buf.String()
	if output == "" {
		t.Error("color handler should produce output")
	}
	// 应该包含颜色代码
	if !bytes.Contains(buf.Bytes(), []byte("\033[")) {
		t.Error("output should contain ANSI color codes")
	}
}

func TestColorTextHandlerLevels(t *testing.T) {
	var buf bytes.Buffer
	h := newColorTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})

	if h.Enabled(nil, slog.LevelDebug) {
		t.Error("should not be enabled for DEBUG")
	}
	if h.Enabled(nil, slog.LevelInfo) {
		t.Error("should not be enabled for INFO")
	}
	if !h.Enabled(nil, slog.LevelWarn) {
		t.Error("should be enabled for WARN")
	}
	if !h.Enabled(nil, slog.LevelError) {
		t.Error("should be enabled for ERROR")
	}
}
