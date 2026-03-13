# Document Plan: Git-Go-Git (git mirror tool)

---

## Overview

A daemon that watches a source repository and automatically mirrors pushes to one or more remotes, configured via a YAML file with CLI overrides.

---

## Project Structure

gitgogit/
├── main.go              # Entry point, CLI parsing
├── config/
│   └── config.go        # Config file parsing & validation
├── mirror/
│   └── mirror.go        # Core mirroring logic
├── auth/
│   ├── ssh.go           # SSH key auth
│   └── token.go         # HTTPS token auth
├── daemon/
│   └── daemon.go        # Background watcher/scheduler
├── log/
│   └── log.go           # Structured logging
├── config.example.yaml
├── go.mod
└── README.md

---

## Config

### YAML File Format

```yml
repos:
  - name: {repository_name}
    source:
      url: git@github.com:{org|user}/{repository_name}.git
      auth:
        type: ssh
        key: ~/.ssh/id_*
    mirrors:
      - url: git@codeberg.org:{org|user}/{repository_name}.git
        auth:
          type: ssh
          key: ~/.ssh/id_*
      - url: https://gitlab.com:{org|user}/{repository_name}.git
        auth:
          type: token
          env: GITLAB_TOKEN        # reads from environment variable

daemon:
  interval: 60s                    # how often to poll source for changes
  retry_attempts: 3
  retry_backoff: 10s
  log_level: info
  log_file: ~/.local/share/gitgogit/gitgogit.log
```

### CLI Interface
```sh
gitgogit [command] [flags]

Commands:
  start           Start the daemon
  stop            Stop the daemon
  status          Show daemon status and last sync times
  sync            Manually trigger a sync (one-shot)
  add             Add a repo or mirror interactively
  list            List configured repos and their mirrors

Flags (override config):
  --config        Path to config file (default: ~/.config/gitgogit/config.yaml)
  --interval      Poll interval (e.g. 30s, 5m)
  --log-level     Log level (debug, info, warn, error)
  --repo          Target a specific repo by name
```

---

## Core Components

### 1. Config (internal/config)

- Parse and validate YAML on startup
- Watch config file for changes and hot-reload without restarting
- Merge CLI flag overrides on top of file config

### 2. Auth (internal/auth)

- SSH: load private key from path, construct ssh.ClientConfig, pass via GIT_SSH_COMMAND env var to git subprocess
- HTTPS token: inject into remote URL as https://oauth2:TOKEN@gitlab.com/... or via GIT_ASKPASS
- Never log or expose credentials

### 3. Mirror (internal/mirror)

- Clone source repo into a temp/cache directory as a bare repo (git clone --bare --mirror)
- On each sync cycle: git fetch --prune origin on the bare clone, then git push --mirror <remote> to each target
- Using a bare mirror clone is the cleanest approach — it handles all branches, tags, and deletions automatically without needing to track them manually

### 4. Daemon (internal/daemon)

- Run as a background process, storing a PID file at ~/.local/share/gitgogit/gitgogit.pid
- Ticker-based polling at the configured interval
- Per-repo goroutines so slow mirrors on one repo don't block others
- Retry logic with exponential backoff on failure
- Graceful shutdown on SIGINT/SIGTERM

### 5. Logging (internal/log)

- Structured JSON logs to file, human-readable to stdout
- Per-repo context in each log entry (repo name, mirror URL, result)

---

## Milestones

### Phase 1 — Foundation

- Project scaffold, go.mod, cobra CLI skeleton
- Config parsing and validation
- Basic sync one-shot command (no daemon yet)

### Phase 2 — Core Mirroring

- Bare clone + fetch + push mirror logic
- SSH and token auth working
- Error handling and retry logic

### Phase 3 — Daemon

- Background process with PID file
- Ticker-based polling loop
- start, stop, status commands
- Structured logging

### Phase 4 — Polish

- Config hot-reload
- add and list commands
- Graceful shutdown
- README and example config

---

## Additional Considerations

- Try to create this application without any external libraries, if possible.
- Create unit and integration tests when needed.
