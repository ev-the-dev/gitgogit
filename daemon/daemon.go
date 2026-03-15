package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gitgogit/config"
	"gitgogit/mirror"
)

// withRetry calls fn up to attempts times with exponential backoff.
// Backoff sequence: base, 2*base, 4*base, … capped at 5 minutes.
// Returns nil on first success, or the last error wrapped with attempt count.
func withRetry(ctx context.Context, attempts int, base time.Duration, fn func() error) error {
	const maxBackoff = 5 * time.Minute
	var err error
	for i := 0; i < attempts; i++ {
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
			backoff := base * (1 << i)
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
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
	logger    *slog.Logger
	wg        sync.WaitGroup
	newRunner func(config.RepoConfig, *slog.Logger) syncer // nil → mirror.NewRunner
}

func New(cfg *config.Config, logger *slog.Logger) *Daemon {
	return &Daemon{cfg: cfg, logger: logger}
}

// SyncRepo mirrors one repo with retry. It constructs a Runner, calls Sync inside withRetry, and returns the results from the final attempt.
func (d *Daemon) SyncRepo(ctx context.Context, repo config.RepoConfig) []mirror.SyncResult {
	runner := d.makeRunner(repo)
	var results []mirror.SyncResult

	withRetry(ctx, d.cfg.Daemon.RetryAttempts, d.cfg.Daemon.RetryBackoff.Duration, func() error {
		results = runner.Sync(ctx)
		for _, r := range results {
			if r.Err != nil {
				return r.Err
			}
		}
		return nil
	})

	return results
}

// Run starts the polling loop. It syncs all repos immediately, then ticks at the configured interval. Blocks until ctx is cancelled, then waits for all in-flight syncs to complete before returning.
func (d *Daemon) Run(ctx context.Context) {
	d.runOnce(ctx)

	ticker := time.NewTicker(d.cfg.Daemon.Interval.Duration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.wg.Wait()
			return
		case <-ticker.C:
			d.runOnce(ctx)
		}
	}
}

// makeRunner returns a syncer for repo, using the test-injectable factory if set.
func (d *Daemon) makeRunner(repo config.RepoConfig) syncer {
	if d.newRunner != nil {
		return d.newRunner(repo, d.logger)
	}
	return mirror.NewRunner(repo, d.logger)
}

// runOnce syncs every configured repo, each in its own goroutine.
func (d *Daemon) runOnce(ctx context.Context) {
	for _, repo := range d.cfg.Repos {
		repo := repo
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			results := d.SyncRepo(ctx, repo)
			for _, r := range results {
				if r.Err != nil {
					d.logger.Error("sync failed",
						slog.String("repo", r.Repo),
						slog.String("mirror", r.MirrorURL),
						slog.String("err", r.Err.Error()),
					)
				} else {
					d.logger.Info("synced",
						slog.String("repo", r.Repo),
						slog.String("mirror", r.MirrorURL),
					)
				}
			}
		}()
	}
}
