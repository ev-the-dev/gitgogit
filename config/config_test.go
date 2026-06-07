package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func validDaemon() DaemonConfig {
	return DaemonConfig{
		Interval:      Duration{60 * time.Second},
		RetryAttempts: 3,
		RetryBackoff:  Duration{10 * time.Second},
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad_Valid(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "id_test")
	if err := os.WriteFile(keyFile, []byte("fake key"), 0o600); err != nil {
		t.Fatal(err)
	}

	path := writeConfig(t, fmt.Sprintf(`
repos:
  - name: myrepo
    source:
      url: git@github.com:org/repo.git
      auth:
        type: ssh
        key: %s
    mirrors:
      - url: git@codeberg.org:org/repo.git
        auth:
          type: ssh
          key: %s
daemon:
  interval: 30s
  retry_attempts: 5
  retry_backoff: 5s
  log_level: debug
`, keyFile, keyFile))
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(cfg.Repos))
	}
	if cfg.Repos[0].Name != "myrepo" {
		t.Errorf("repo name = %q, want %q", cfg.Repos[0].Name, "myrepo")
	}
	if cfg.Daemon.Interval.Duration != 30*time.Second {
		t.Errorf("interval = %v, want 30s", cfg.Daemon.Interval.Duration)
	}
	if cfg.Daemon.RetryAttempts != 5 {
		t.Errorf("retry_attempts = %d, want 5", cfg.Daemon.RetryAttempts)
	}
	if cfg.Daemon.LogLevel != "debug" {
		t.Errorf("log_level = %q, want %q", cfg.Daemon.LogLevel, "debug")
	}
}

func TestLoad_Defaults(t *testing.T) {
	path := writeConfig(t, `
repos:
  - name: repo
    source:
      url: git@github.com:org/repo.git
    mirrors:
      - url: git@codeberg.org:org/repo.git
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Daemon.Interval.Duration != 60*time.Second {
		t.Errorf("default interval = %v, want 60s", cfg.Daemon.Interval.Duration)
	}
	if cfg.Daemon.RetryAttempts != 3 {
		t.Errorf("default retry_attempts = %d, want 3", cfg.Daemon.RetryAttempts)
	}
	if cfg.Daemon.RetryBackoff.Duration != 10*time.Second {
		t.Errorf("default retry_backoff = %v, want 10s", cfg.Daemon.RetryBackoff.Duration)
	}
	if cfg.Daemon.LogLevel != "info" {
		t.Errorf("default log_level = %q, want %q", cfg.Daemon.LogLevel, "info")
	}
}

func TestLoad_TildeExpansion(t *testing.T) {
	path := writeConfig(t, `
repos:
  - name: repo
    source:
      url: git@github.com:org/repo.git
      auth:
        type: ssh
        key: ~/.ssh/id_ed25519
    mirrors:
      - url: git@codeberg.org:org/repo.git
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	key := cfg.Repos[0].Source.Auth.Key
	if key == "" || key[0] == '~' {
		t.Errorf("key path not expanded: %q", key)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeConfig(t, `repos: [invalid yaml {{`)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoad_InvalidDuration(t *testing.T) {
	path := writeConfig(t, `
repos: []
daemon:
  interval: notaduration
`)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid duration")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestValidate_Valid(t *testing.T) {
	cfg := &Config{
		Repos: []RepoConfig{
			{
				Name:   "myrepo",
				Source: SourceConfig{URL: "git@github.com:org/repo.git"},
				Mirrors: []MirrorTarget{
					{URL: "git@codeberg.org:org/repo.git"},
				},
			},
		},
		Daemon: validDaemon(),
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidate_MissingName(t *testing.T) {
	cfg := &Config{
		Repos: []RepoConfig{
			{
				Source:  SourceConfig{URL: "git@github.com:org/repo.git"},
				Mirrors: []MirrorTarget{{URL: "git@codeberg.org:org/repo.git"}},
			},
		},
		Daemon: validDaemon(),
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing repo name")
	}
}

func TestValidate_DuplicateName(t *testing.T) {
	repo := RepoConfig{
		Name:    "dup",
		Source:  SourceConfig{URL: "git@github.com:org/repo.git"},
		Mirrors: []MirrorTarget{{URL: "git@codeberg.org:org/repo.git"}},
	}
	cfg := &Config{Repos: []RepoConfig{repo, repo}, Daemon: validDaemon()}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for duplicate repo name")
	}
}

func TestValidate_MissingSourceURL(t *testing.T) {
	cfg := &Config{
		Repos: []RepoConfig{
			{
				Name:    "repo",
				Mirrors: []MirrorTarget{{URL: "git@codeberg.org:org/repo.git"}},
			},
		},
		Daemon: validDaemon(),
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing source URL")
	}
}

func TestValidate_NoMirrors(t *testing.T) {
	cfg := &Config{
		Repos: []RepoConfig{
			{
				Name:   "repo",
				Source: SourceConfig{URL: "git@github.com:org/repo.git"},
			},
		},
		Daemon: validDaemon(),
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for no mirrors")
	}
}

func TestValidate_SSHMissingKey(t *testing.T) {
	cfg := &Config{
		Repos: []RepoConfig{
			{
				Name:    "repo",
				Source:  SourceConfig{URL: "git@github.com:org/repo.git", Auth: AuthConfig{Type: "ssh"}},
				Mirrors: []MirrorTarget{{URL: "git@codeberg.org:org/repo.git"}},
			},
		},
		Daemon: validDaemon(),
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for ssh auth missing key")
	}
}

func TestValidate_DuplicateMirrorURL(t *testing.T) {
	mirror := MirrorTarget{URL: "git@codeberg.org:org/repo.git"}
	cfg := &Config{
		Repos: []RepoConfig{
			{
				Name:    "repo",
				Source:  SourceConfig{URL: "git@github.com:org/repo.git"},
				Mirrors: []MirrorTarget{mirror, mirror},
			},
		},
		Daemon: validDaemon(),
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for duplicate mirror URL")
	}
}

func TestValidate_TokenMissingEnv(t *testing.T) {
	cfg := &Config{
		Repos: []RepoConfig{
			{
				Name:    "repo",
				Source:  SourceConfig{URL: "https://github.com/org/repo.git"},
				Mirrors: []MirrorTarget{{URL: "https://gitlab.com/org/repo.git", Auth: AuthConfig{Type: "token"}}},
			},
		},
		Daemon: validDaemon(),
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for token auth missing env")
	}
}

func TestMerge_OverridesInterval(t *testing.T) {
	cfg := &Config{Daemon: DaemonConfig{Interval: Duration{60 * time.Second}}}
	if err := cfg.Merge(CLIOverrides{Interval: "5m"}); err != nil {
		t.Fatalf("Merge() error: %v", err)
	}
	if cfg.Daemon.Interval.Duration != 5*time.Minute {
		t.Errorf("interval = %v, want 5m", cfg.Daemon.Interval.Duration)
	}
}

func TestMerge_OverridesLogLevel(t *testing.T) {
	cfg := &Config{Daemon: DaemonConfig{LogLevel: "info"}}
	if err := cfg.Merge(CLIOverrides{LogLevel: "debug"}); err != nil {
		t.Fatalf("Merge() error: %v", err)
	}
	if cfg.Daemon.LogLevel != "debug" {
		t.Errorf("log_level = %q, want %q", cfg.Daemon.LogLevel, "debug")
	}
}

func TestMerge_InvalidInterval(t *testing.T) {
	cfg := &Config{}
	if err := cfg.Merge(CLIOverrides{Interval: "notvalid"}); err == nil {
		t.Error("expected error for invalid interval override")
	}
}

func TestExpandPath_Tilde(t *testing.T) {
	expanded, err := ExpandPath("~/foo/bar")
	if err != nil {
		t.Fatalf("ExpandPath() error: %v", err)
	}
	if expanded == "" || expanded[0] == '~' {
		t.Errorf("ExpandPath() = %q, should not start with ~", expanded)
	}
}

func TestExpandPath_NoTilde(t *testing.T) {
	input := "/absolute/path"
	expanded, err := ExpandPath(input)
	if err != nil {
		t.Fatalf("ExpandPath() error: %v", err)
	}
	if expanded != input {
		t.Errorf("ExpandPath(%q) = %q, want %q", input, expanded, input)
	}
}
