package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"gitgogit/config"
	"gitgogit/mirror"
	"gitgogit/status"
)

const (
	hotReloadInterval  = 5 * time.Second
	maxConcurrentSyncs = 10
	shutdownTimeout    = 30 * time.Second
)

// withRetry calls fn up to attempts times with exponential backoff.
// Backoff sequence: base, 2*base, 4*base, … capped at 5 minutes.
// Returns nil on first success, or the last error wrapped with attempt count.
func withRetry(ctx context.Context, attempts int, base time.Duration, fn func() error) error {
	const maxBackoff = 5 * time.Minute
	var err error
	for i := range attempts {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err = fn()
		if err == nil {
			return nil
		}

		if i < attempts-1 {
			backoff := min(base*(1<<i), maxBackoff)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	return fmt.Errorf("failed after %d attempt(s): %w", attempts, err)
}

// syncer is satisfied by mirror.Runner and can be replaced in tests.
type syncer interface {
	Sync(ctx context.Context) []mirror.SyncResult
}

// Daemon holds process-wide configuration and orchestrates repo syncing.
type Daemon struct {
	cfg       *config.Config
	mu        sync.RWMutex // guards cfg
	logger    *slog.Logger
	wg        sync.WaitGroup
	Status    *status.Store                                         // nil when web UI is disabled
	newRunner func(config.RepoConfig, *slog.Logger) (syncer, error) // nil → mirror.NewRunner
}

func New(cfg *config.Config, logger *slog.Logger, store *status.Store) *Daemon {
	return &Daemon{cfg: cfg, logger: logger, Status: store}
}

// SyncRepo mirrors one repo with retry. It constructs a Runner, calls Sync inside withRetry, and returns the results from the final attempt.
func (d *Daemon) SyncRepo(ctx context.Context, repo config.RepoConfig) []mirror.SyncResult {
	runner, err := d.makeRunner(repo)
	if err != nil {
		var results []mirror.SyncResult
		for _, m := range repo.Mirrors {
			results = append(results, mirror.SyncResult{
				Repo:      repo.Name,
				MirrorURL: m.URL,
				Err:       err,
			})
		}
		return results
	}

	var results []mirror.SyncResult

	d.mu.RLock()
	attempts := d.cfg.Daemon.RetryAttempts
	backoff := d.cfg.Daemon.RetryBackoff.Duration
	d.mu.RUnlock()

	retryErr := withRetry(ctx, attempts, backoff, func() error {
		results = runner.Sync(ctx)
		for _, r := range results {
			if r.Err != nil {
				return r.Err
			}
		}
		return nil
	})

	if retryErr != nil && len(results) == 0 {
		for _, m := range repo.Mirrors {
			results = append(results, mirror.SyncResult{
				Repo:      repo.Name,
				MirrorURL: m.URL,
				Err:       retryErr,
			})
		}
	}

	return results
}

// TriggerSync starts a sync for a single repo by name. Returns an error if the
// repo is not found or a sync is already in progress. The sync runs in the
// background and results are recorded in the status store.
func (d *Daemon) TriggerSync(ctx context.Context, repoName string) error {
	d.mu.RLock()
	var repo config.RepoConfig
	found := false
	for _, r := range d.cfg.Repos {
		if r.Name == repoName {
			repo = r
			found = true
			break
		}
	}
	d.mu.RUnlock()

	if !found {
		return fmt.Errorf("repo %q not found", repoName)
	}

	if d.Status == nil {
		return fmt.Errorf("status store not available")
	}

	unlock, ok := d.Status.TryLockRepo(repoName)
	if !ok {
		return fmt.Errorf("sync already in progress for %q", repoName)
	}

	d.Status.MarkSyncing(repoName)
	d.wg.Go(func() {
		defer unlock()
		results := d.SyncRepo(ctx, repo)
		d.Status.Record(repo.Name, results)
		for _, r := range results {
			if r.Err != nil {
				d.logger.Error("sync failed",
					slog.String("repo", r.Repo),
					slog.String("mirror", r.MirrorURL),
					slog.String("error", r.Err.Error()),
				)
			} else {
				d.logger.Info("synced",
					slog.String("repo", r.Repo),
					slog.String("mirror", r.MirrorURL),
				)
			}
		}
	})

	return nil
}

// Run starts the polling loop. It syncs all repos immediately, then ticks at the configured interval.
// If configPath is non-empty, a goroutine watches the file for changes and hot-reloads on modification.
// Blocks until ctx is cancelled, then waits for all in-flight syncs to complete before returning.
func (d *Daemon) Run(ctx context.Context, configPath string) {
	if d.Status != nil {
		d.mu.RLock()
		d.Status.EnsureRepos(d.cfg.Repos)
		d.mu.RUnlock()
	}

	if configPath != "" {
		var lastMod time.Time
		if info, err := os.Stat(configPath); err == nil {
			lastMod = info.ModTime()
		}
		go func() {
			for {
				newMod, err := config.Poll(ctx, configPath, lastMod, hotReloadInterval)
				if err != nil {
					return // ctx cancelled
				}
				lastMod = newMod
				if err := d.reloadConfig(configPath); err != nil {
					d.logger.Warn("config reload failed", slog.String("error", err.Error()))
				} else {
					d.logger.Info("config reloaded", slog.String("path", configPath))
					if d.Status != nil {
						d.mu.RLock()
						d.Status.EnsureRepos(d.cfg.Repos)
						d.mu.RUnlock()
					}
				}
			}
		}()
	}

	d.mu.RLock()
	interval := d.cfg.Daemon.Interval.Duration
	d.mu.RUnlock()

	d.runOnce(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			done := make(chan struct{})
			go func() {
				d.wg.Wait()
				close(done)
			}()
			select {
			case <-done:
				d.logger.Info("all syncs completed, shutting down")
			case <-time.After(shutdownTimeout):
				d.logger.Warn("shutdown timeout exceeded, forcing exit")
			}
			return
		case <-ticker.C:
			d.mu.RLock()
			interval = d.cfg.Daemon.Interval.Duration
			d.mu.RUnlock()
			ticker.Reset(interval)
			d.runOnce(ctx)
		}
	}
}

// reloadConfig loads the config from path and swaps it in under the write lock.
func (d *Daemon) reloadConfig(path string) error {
	newCfg, err := config.Load(path)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.cfg = newCfg
	d.mu.Unlock()
	return nil
}

// makeRunner returns a syncer for repo, using the test-injectable factory if set.
func (d *Daemon) makeRunner(repo config.RepoConfig) (syncer, error) {
	if d.newRunner != nil {
		return d.newRunner(repo, d.logger)
	}
	return mirror.NewRunner(repo, d.logger)
}

// runOnce syncs every configured repo, each in its own goroutine.
// Concurrency is capped by maxConcurrentSyncs.
func (d *Daemon) runOnce(ctx context.Context) {
	d.mu.RLock()
	repos := make([]config.RepoConfig, len(d.cfg.Repos))
	copy(repos, d.cfg.Repos)
	d.mu.RUnlock()

	sem := make(chan struct{}, maxConcurrentSyncs)
	for _, repo := range repos {
		d.wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			results := d.SyncRepo(ctx, repo)
			if d.Status != nil {
				d.Status.Record(repo.Name, results)
			}
			for _, r := range results {
				if r.Err != nil {
					d.logger.Error("sync failed",
						slog.String("repo", r.Repo),
						slog.String("mirror", r.MirrorURL),
						slog.String("error", r.Err.Error()),
					)
				} else {
					d.logger.Info("synced",
						slog.String("repo", r.Repo),
						slog.String("mirror", r.MirrorURL),
					)
				}
			}
		})
	}
}
