package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"gitgogit/config"
	glog "gitgogit/log"
	"gitgogit/mirror"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "sync":
		runSync(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `gitgogit - Git repository mirroring daemon

Usage:
  gitgogit <command> [flags]

Commands:
  sync    Perform a one-shot mirror sync and exit

Flags:
  --config      Path to config file (default: ~/.config/gitgogit/config.yaml)
  --interval    Poll interval override (e.g. 30s, 5m)
  --log-level   Log level: debug, info, warn, error
  --repo        Sync only this repo (by name)
`)
}

func runSync(args []string) {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	configPath := fs.String("config", config.DefaultPath(), "path to config file")
	interval := fs.String("interval", "", "poll interval override")
	logLevel := fs.String("log-level", "", "log level override")
	repo := fs.String("repo", "", "sync only this repo by name")
	fs.Parse(args)

	cfg, err := loadConfig(*configPath, config.CLIOverrides{
		Interval: *interval,
		LogLevel: *logLevel,
		Repo:     *repo,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	logger, err := glog.Setup(cfg.Daemon.LogLevel, cfg.Daemon.LogFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "log setup: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	exitCode := 0

	for _, r := range cfg.Repos {
		if *repo != "" && r.Name != *repo {
			continue
		}
		runner := mirror.NewRunner(r, logger)
		results := runner.Sync(ctx)
		for _, res := range results {
			if res.Err != nil {
				logger.Error("sync failed",
					slog.String("repo", res.Repo),
					slog.String("mirror", res.MirrorURL),
					slog.String("err", res.Err.Error()),
				)
				exitCode = 1
			} else {
				logger.Info("synced",
					slog.String("repo", res.Repo),
					slog.String("mirror", res.MirrorURL),
				)
			}
		}
	}

	os.Exit(exitCode)
}

// loadConfig loads the config file and merges CLI overrides.
func loadConfig(path string, overrides config.CLIOverrides) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if err := cfg.Merge(overrides); err != nil {
		return nil, fmt.Errorf("apply overrides: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}
