package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"

	"moleAgent_Serv/internal/config"
)

// Init 初始化日志系统，返回 *slog.Logger
func Init(cfg config.LoggingConfig) *slog.Logger {
	level := parseLevel(cfg.Level)

	var handlers []slog.Handler

	if cfg.Console.Enabled {
		consoleOpts := &slog.HandlerOptions{Level: level}
		if cfg.Console.Color {
			handlers = append(handlers, newColorTextHandler(os.Stdout, consoleOpts))
		} else if cfg.Format == "json" {
			handlers = append(handlers, slog.NewJSONHandler(os.Stdout, consoleOpts))
		} else {
			handlers = append(handlers, slog.NewTextHandler(os.Stdout, consoleOpts))
		}
	}

	if cfg.File.Enabled {
		fileWriter := &lumberjack.Logger{
			Filename:   cfg.File.Path,
			MaxSize:    cfg.File.MaxSizeMB,
			MaxBackups: cfg.File.MaxBackups,
			MaxAge:     cfg.File.MaxAgeDays,
			Compress:   cfg.File.Compress,
		}
		fileOpts := &slog.HandlerOptions{Level: level}
		if cfg.Format == "json" {
			handlers = append(handlers, slog.NewJSONHandler(fileWriter, fileOpts))
		} else {
			handlers = append(handlers, slog.NewTextHandler(fileWriter, fileOpts))
		}
	}

	var handler slog.Handler
	if len(handlers) == 1 {
		handler = handlers[0]
	} else {
		handler = newMultiHandler(handlers...)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// multiHandler 同时写入多个 handler
type multiHandler struct {
	handlers []slog.Handler
}

func newMultiHandler(handlers ...slog.Handler) *multiHandler {
	return &multiHandler{handlers: handlers}
}

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r.Clone()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithAttrs(attrs)
	}
	return newMultiHandler(handlers...)
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithGroup(name)
	}
	return newMultiHandler(handlers...)
}

// colorTextHandler 带颜色的文本日志 handler
type colorTextHandler struct {
	w      io.Writer
	opts   *slog.HandlerOptions
	attrs  []slog.Attr
	group  string
}

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorGray   = "\033[90m"
)

func newColorTextHandler(w io.Writer, opts *slog.HandlerOptions) *colorTextHandler {
	return &colorTextHandler{w: w, opts: opts}
}

func (h *colorTextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.opts.Level.Level()
}

func (h *colorTextHandler) Handle(ctx context.Context, r slog.Record) error {
	var color string
	switch r.Level {
	case slog.LevelDebug:
		color = colorGray
	case slog.LevelInfo:
		color = colorBlue
	case slog.LevelWarn:
		color = colorYellow
	case slog.LevelError:
		color = colorRed
	default:
		color = colorReset
	}

	levelStr := r.Level.String()
	if len(levelStr) < 5 {
		levelStr += " "
	}

	// 时间 + 级别 + 消息
	ts := r.Time.Format("2006-01-02T15:04:05.000-0700")
	line := color + ts + "  " + levelStr + "  " + colorReset + r.Message

	// 属性
	attrs := make([]slog.Attr, 0, len(h.attrs)+r.NumAttrs())
	attrs = append(attrs, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})

	if len(attrs) > 0 {
		line += "  {"
		for i, a := range attrs {
			if i > 0 {
				line += ", "
			}
			line += colorGray + a.Key + colorReset + "=" + a.Value.String()
		}
		line += "}"
	}

	line += "\n"
	_, err := h.w.Write([]byte(line))
	return err
}

func (h *colorTextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	copy(newAttrs[len(h.attrs):], attrs)
	return &colorTextHandler{w: h.w, opts: h.opts, attrs: newAttrs, group: h.group}
}

func (h *colorTextHandler) WithGroup(name string) slog.Handler {
	return &colorTextHandler{w: h.w, opts: h.opts, attrs: h.attrs, group: name}
}
