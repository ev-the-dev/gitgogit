package log

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		input string
		want  slog.Level
		err   bool
	}{
		{"debug", slog.LevelDebug, false},
		{"DEBUG", slog.LevelDebug, false},
		{"info", slog.LevelInfo, false},
		{"INFO", slog.LevelInfo, false},
		{"", slog.LevelInfo, false},
		{"warn", slog.LevelWarn, false},
		{"warning", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"ERROR", slog.LevelError, false},
		{"unknown", slog.LevelInfo, true},
		{"verbose", slog.LevelInfo, true},
	}
	for _, c := range cases {
		got, err := ParseLevel(c.input)
		if (err != nil) != c.err {
			t.Errorf("ParseLevel(%q) error = %v, wantErr %v", c.input, err, c.err)
		}
		if !c.err && got != c.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestSetup_NoLogFile(t *testing.T) {
	logger, closeLog, err := Setup("info", "", os.Stdout)
	if err != nil {
		t.Fatalf("Setup() error: %v", err)
	}
	defer func() { _ = closeLog() }()
	if logger == nil {
		t.Fatal("Setup() returned nil logger")
	}
}

func TestSetup_WithLogFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "subdir", "app.log")

	logger, closeLog, err := Setup("debug", logPath, io.Discard)
	if err != nil {
		t.Fatalf("Setup() error: %v", err)
	}
	defer func() { _ = closeLog() }()
	if logger == nil {
		t.Fatal("Setup() returned nil logger")
	}

	logger.Info("test message", "key", "value")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if len(data) == 0 {
		t.Error("log file is empty after writing")
	}
}

func TestMultiHandler_DeliversToBoth(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")

	logger, closeLog, err := Setup("info", logPath, io.Discard)
	if err != nil {
		t.Fatalf("Setup() error: %v", err)
	}
	defer func() { _ = closeLog() }()

	logger.Info("hello world")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if len(data) == 0 {
		t.Error("nothing written to log file")
	}
}
