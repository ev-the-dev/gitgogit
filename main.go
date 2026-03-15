package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"gitgogit/config"
	"gitgogit/daemon"
	glog "gitgogit/log"
)

func main() {
	// The --daemon-child sentinel is checked before subcommand dispatch so it
	// never appears in help output or shell completions.
	if len(os.Args) >= 2 && os.Args[1] == "--daemon-child" {
		runDaemonChild(os.Args[2:])
		return
	}

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "sync":
		runSync(args)
	case "start":
		runStart(args)
	case "stop":
		runStop(args)
	case "status":
		runStatus(args)
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
  start   Start the background daemon
  stop    Stop the running daemon
  status  Show daemon status
  sync    Perform a one-shot mirror sync and exit

Flags:
  --config      Path to config file (default: ~/.config/gitgogit/config.yaml)
  --interval    Poll interval override (e.g. 30s, 5m)
  --log-level   Log level: debug, info, warn, error
  --repo        Sync only this repo (by name)
`)
}

func runStart(args []string) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	configPath := fs.String("config", config.DefaultConfigPath(), "path to config file")
	fs.Parse(args)

	cfg, err := loadConfig(*configPath, config.CLIOverrides{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	pidPath := config.DefaultPIDPath()
	if _, running, _ := daemon.IsRunning(pidPath); running {
		pid, _, _ := daemon.IsRunning(pidPath)
		fmt.Fprintf(os.Stderr, "daemon is already running (pid %d)\n", pid)
		os.Exit(1)
	}

	logPath := cfg.Daemon.LogFile
	if logPath == "" {
		logPath = config.DefaultLogPath()
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "find executable: %v\n", err)
		os.Exit(1)
	}

	cmd := exec.Command(exe, "--daemon-child", "--config", *configPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "start daemon: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("daemon started (pid %d)\n", cmd.Process.Pid)
	fmt.Printf("logging to %s\n", logPath)
}

func runDaemonChild(args []string) {
	fs := flag.NewFlagSet("daemon-child", flag.ExitOnError)
	configPath := fs.String("config", config.DefaultConfigPath(), "")
	fs.Parse(args)

	cfg, err := loadConfig(*configPath, config.CLIOverrides{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	logger, err := glog.Setup(cfg.Daemon.LogLevel, cfg.Daemon.LogFile, io.Discard)
	if err != nil {
		fmt.Fprintf(os.Stderr, "log setup: %v\n", err)
		os.Exit(1)
	}

	pidPath := config.DefaultPIDPath()
	if err := daemon.WritePID(pidPath); err != nil {
		logger.Error("write pid file", slog.String("err", err.Error()))
		os.Exit(1)
	}
	defer daemon.RemovePID(pidPath)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	logger.Info("daemon started", slog.Int("pid", os.Getpid()), slog.String("config", *configPath))
	daemon.New(cfg, logger).Run(ctx)
	logger.Info("daemon stopped")
}

func runStop(args []string) {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	fs.Parse(args)

	pidPath := config.DefaultPIDPath()
	pid, running, err := daemon.IsRunning(pidPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if !running {
		fmt.Fprintln(os.Stderr, "daemon is not running")
		os.Exit(1)
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		fmt.Fprintf(os.Stderr, "send SIGTERM: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("sent SIGTERM to daemon (pid %d)\n", pid)
}

func runStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	fs.Parse(args)

	pidPath := config.DefaultPIDPath()
	pid, running, err := daemon.IsRunning(pidPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if !running {
		fmt.Println("not running")
		return
	}

	started := "unknown"
	if info, err := os.Stat(pidPath); err == nil {
		started = info.ModTime().Format(time.RFC3339)
	}
	fmt.Printf("running (pid %d, started %s)\n", pid, started)
}

func runSync(args []string) {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	configPath := fs.String("config", config.DefaultConfigPath(), "path to config file")
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

	logger, err := glog.Setup(cfg.Daemon.LogLevel, cfg.Daemon.LogFile, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "log setup: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	exitCode := 0
	d := daemon.New(cfg, logger)

	for _, r := range cfg.Repos {
		if *repo != "" && r.Name != *repo {
			continue
		}
		results := d.SyncRepo(ctx, r)
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
