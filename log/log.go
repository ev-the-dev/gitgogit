package log

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gitgogit/config"
)

// multiHandler fans a single Record out to multiple slog.Handlers.
type multiHandler struct {
	handlers []slog.Handler
}

func (m multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m multiHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			errs = append(errs, h.Handle(ctx, r.Clone()))
		}
	}
	return errors.Join(errs...)
}

func (m multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithAttrs(attrs)
	}
	return multiHandler{handlers: handlers}
}

func (m multiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithGroup(name)
	}
	return multiHandler{handlers: handlers}
}

// ParseLevel converts a level string to slog.Level.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown log level %q", s)
	}
}

// Setup initialises a slog.Logger. `out` controls the text handler destination:
//   - pass os.Stdout for interactive commands (sync)
//   - pass io.Discard for daemon mode (logging goes to the JSON file only)
//
// If logFilePath is non-empty a JSON handler is also added, writing to that
// file (created/appended). If out is io.Discard and logFilePath is empty the
// logger silently discards all output.
func Setup(levelStr, logFilePath string, out io.Writer) (*slog.Logger, func() error, error) {
	level, err := ParseLevel(levelStr)
	if err != nil {
		return nil, nil, err
	}

	opts := &slog.HandlerOptions{Level: level}
	textHandler := slog.NewTextHandler(out, opts)

	if logFilePath == "" {
		return slog.New(textHandler), func() error { return nil }, nil
	}

	if err := os.MkdirAll(filepath.Dir(logFilePath), config.DirPerm); err != nil {
		return nil, nil, fmt.Errorf("create log dir: %w", err)
	}
	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, config.FilePerm)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}
	fileHandler := slog.NewJSONHandler(f, opts)

	return slog.New(multiHandler{handlers: []slog.Handler{textHandler, fileHandler}}), f.Close, nil
}
