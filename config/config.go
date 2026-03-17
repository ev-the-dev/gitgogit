package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// DirPerm grants rwx to owner, r-x to group, nothing to others.
	DirPerm os.FileMode = 0o750
	// FilePerm grants rw- to owner, r-- to group, nothing to others.
	FilePerm os.FileMode = 0o640
)

// Duration wraps time.Duration to support YAML unmarshaling of strings like "60s".
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	dur, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value.Value, err)
	}
	d.Duration = dur
	return nil
}

// AuthConfig holds credentials for a single endpoint.
type AuthConfig struct {
	Type string `yaml:"type"` // "ssh", "token", or "" (no auth)
	Key  string `yaml:"key"`  // SSH private key path (type=ssh)
	Env  string `yaml:"env"`  // env var holding the token (type=token)
}

// SourceConfig describes the upstream repository.
type SourceConfig struct {
	URL  string     `yaml:"url"`
	Auth AuthConfig `yaml:"auth"`
}

// MirrorTarget is one push destination.
type MirrorTarget struct {
	URL          string     `yaml:"url"`
	Auth         AuthConfig `yaml:"auth"`
	PushStrategy string     `yaml:"push_strategy"` // "mirror" (default), "branches+tags"
}

// RepoConfig is one full mirroring job.
type RepoConfig struct {
	Name    string         `yaml:"name"`
	Source  SourceConfig   `yaml:"source"`
	Mirrors []MirrorTarget `yaml:"mirrors"`
}

// WebConfig holds optional web dashboard settings.
type WebConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
}

// DaemonConfig holds daemon-wide settings.
type DaemonConfig struct {
	Interval      Duration  `yaml:"interval"`
	RetryAttempts int       `yaml:"retry_attempts"`
	RetryBackoff  Duration  `yaml:"retry_backoff"`
	LogLevel      string    `yaml:"log_level"`
	LogFile       string    `yaml:"log_file"`
	Web           WebConfig `yaml:"web"`
}

// Config is the root configuration document.
type Config struct {
	Repos  []RepoConfig `yaml:"repos"`
	Daemon DaemonConfig `yaml:"daemon"`
}

// CLIOverrides holds flag values that supersede the file config.
type CLIOverrides struct {
	ConfigPath string
	Interval   string
	LogLevel   string
	Repo       string
}

func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("default config path: %w", err)
	}
	return filepath.Join(home, ".config", "gitgogit", "config.yaml"), nil
}

func DefaultPIDPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("default pid path: %w", err)
	}
	return filepath.Join(home, ".local", "share", "gitgogit", "gitgogit.pid"), nil
}

func DefaultLogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("default log path: %w", err)
	}
	return filepath.Join(home, ".local", "share", "gitgogit", "gitgogit.log"), nil
}

// ExpandPath replaces a leading ~ with the user home directory.
func ExpandPath(p string) (string, error) {
	if !strings.HasPrefix(p, "~") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, p[1:]), nil
}

// Load reads, parses, expands paths, and applies defaults for the config at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Expand ~ in all path fields.
	for i := range cfg.Repos {
		if cfg.Repos[i].Source.Auth.Key != "" {
			expanded, err := ExpandPath(cfg.Repos[i].Source.Auth.Key)
			if err != nil {
				return nil, fmt.Errorf("repo %q source key: %w", cfg.Repos[i].Name, err)
			}
			cfg.Repos[i].Source.Auth.Key = expanded
		}
		for j := range cfg.Repos[i].Mirrors {
			if cfg.Repos[i].Mirrors[j].Auth.Key != "" {
				expanded, err := ExpandPath(cfg.Repos[i].Mirrors[j].Auth.Key)
				if err != nil {
					return nil, fmt.Errorf("repo %q mirror key: %w", cfg.Repos[i].Name, err)
				}
				cfg.Repos[i].Mirrors[j].Auth.Key = expanded
			}
		}
	}
	if cfg.Daemon.LogFile != "" {
		expanded, err := ExpandPath(cfg.Daemon.LogFile)
		if err != nil {
			return nil, fmt.Errorf("log_file: %w", err)
		}
		cfg.Daemon.LogFile = expanded
	}

	// Apply defaults.
	if cfg.Daemon.Interval.Duration == 0 {
		cfg.Daemon.Interval.Duration = 60 * time.Second
	}
	if cfg.Daemon.RetryAttempts == 0 {
		cfg.Daemon.RetryAttempts = 3
	}
	if cfg.Daemon.RetryBackoff.Duration == 0 {
		cfg.Daemon.RetryBackoff.Duration = 10 * time.Second
	}
	if cfg.Daemon.LogLevel == "" {
		cfg.Daemon.LogLevel = "info"
	}
	if cfg.Daemon.Web.Enabled && cfg.Daemon.Web.Listen == "" {
		cfg.Daemon.Web.Listen = ":8080"
	}

	return &cfg, nil
}

// Merge applies non-zero CLIOverrides onto the config in place.
func (c *Config) Merge(o CLIOverrides) error {
	if o.Interval != "" {
		dur, err := time.ParseDuration(o.Interval)
		if err != nil {
			return fmt.Errorf("invalid --interval %q: %w", o.Interval, err)
		}
		if dur <= 0 {
			return fmt.Errorf("invalid --interval %q: must be positive", o.Interval)
		}
		c.Daemon.Interval.Duration = dur
	}
	if o.LogLevel != "" {
		c.Daemon.LogLevel = o.LogLevel
	}
	return nil
}

// Validate checks required fields and internal consistency.
func (c *Config) Validate() error {
	if c.Daemon.Interval.Duration <= 0 {
		return fmt.Errorf("daemon interval must be positive, got %v", c.Daemon.Interval.Duration)
	}
	if c.Daemon.RetryAttempts <= 0 {
		return fmt.Errorf("daemon retry_attempts must be positive, got %d", c.Daemon.RetryAttempts)
	}
	if c.Daemon.RetryBackoff.Duration <= 0 {
		return fmt.Errorf("daemon retry_backoff must be positive, got %v", c.Daemon.RetryBackoff.Duration)
	}

	names := make(map[string]bool)
	for _, r := range c.Repos {
		if r.Name == "" {
			return fmt.Errorf("a repo is missing a name")
		}
		if names[r.Name] {
			return fmt.Errorf("duplicate repo name %q", r.Name)
		}
		names[r.Name] = true
		if r.Source.URL == "" {
			return fmt.Errorf("repo %q: source URL is required", r.Name)
		}
		if err := validateAuth(r.Name, "source", r.Source.Auth); err != nil {
			return err
		}
		if len(r.Mirrors) == 0 {
			return fmt.Errorf("repo %q: at least one mirror is required", r.Name)
		}
		mirrorURLs := make(map[string]bool)
		for _, m := range r.Mirrors {
			if m.URL == "" {
				return fmt.Errorf("repo %q: a mirror is missing a URL", r.Name)
			}
			if mirrorURLs[m.URL] {
				return fmt.Errorf("repo %q: duplicate mirror URL %q", r.Name, m.URL)
			}
			mirrorURLs[m.URL] = true
			switch m.PushStrategy {
			case "", "mirror", "branches+tags":
				// valid
			default:
				return fmt.Errorf("repo %q mirror %q: unknown push_strategy %q (must be \"mirror\" or \"branches+tags\")", r.Name, m.URL, m.PushStrategy)
			}
			if err := validateAuth(r.Name, "mirror "+m.URL, m.Auth); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateAuth(repo, context string, a AuthConfig) error {
	switch a.Type {
	case "ssh":
		if a.Key == "" {
			return fmt.Errorf("repo %q %s: ssh auth requires key", repo, context)
		}
		if _, err := os.Stat(a.Key); err != nil {
			return fmt.Errorf("repo %q %s: ssh key %q: %w", repo, context, a.Key, err)
		}
	case "token":
		if a.Env == "" {
			return fmt.Errorf("repo %q %s: token auth requires env", repo, context)
		}
	case "":
		// no auth required
	default:
		return fmt.Errorf("repo %q %s: unknown auth type %q", repo, context, a.Type)
	}
	return nil
}

// Poll blocks until the config file's mtime changes relative to lastMod,
// checking every checkInterval. Returns the new mtime or a context error.
func Poll(ctx context.Context, path string, lastMod time.Time, checkInterval time.Duration) (time.Time, error) {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return time.Time{}, ctx.Err()
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if info.ModTime().After(lastMod) {
				return info.ModTime(), nil
			}
		}
	}
}

// Save writes the config back to path in YAML format.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0o640)
}
