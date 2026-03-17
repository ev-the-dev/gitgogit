package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"gitgogit/config"
	"gitgogit/mirror"
)

var errTest = errors.New("test error")

func TestWithRetry_SucceedsFirstAttempt(t *testing.T) {
	calls := 0
	err := withRetry(context.Background(), 3, time.Millisecond, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestWithRetry_SucceedsOnSecondAttempt(t *testing.T) {
	calls := 0
	err := withRetry(context.Background(), 3, time.Millisecond, func() error {
		calls++
		if calls < 2 {
			return errTest
		}
		return nil
	})
	if err != nil {
		t.Errorf("expected nil after second attempt, got %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

func TestWithRetry_ExhaustsAllAttempts(t *testing.T) {
	calls := 0
	err := withRetry(context.Background(), 3, time.Millisecond, func() error {
		calls++
		return errTest
	})
	if err == nil {
		t.Error("expected error after all attempts")
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
	if !errors.Is(err, errTest) {
		t.Errorf("expected wrapped errTest, got %v", err)
	}
}

func TestWithRetry_ExponentialBackoff(t *testing.T) {
	base := 10 * time.Millisecond
	calls := 0
	timestamps := []time.Time{}

	_ = withRetry(context.Background(), 4, base, func() error {
		timestamps = append(timestamps, time.Now())
		calls++
		return errTest
	})

	if calls != 4 {
		t.Fatalf("expected 4 calls, got %d", calls)
	}

	// Gap between attempt 1→2 should be ~base (10ms)
	// Gap between attempt 2→3 should be ~2*base (20ms)
	// Gap between attempt 3→4 should be ~4*base (40ms)
	gaps := []time.Duration{
		timestamps[1].Sub(timestamps[0]),
		timestamps[2].Sub(timestamps[1]),
		timestamps[3].Sub(timestamps[2]),
	}
	expected := []time.Duration{base, 2 * base, 4 * base}

	for i, gap := range gaps {
		// Allow 5x margin for slow CI environments.
		if gap < expected[i] || gap > expected[i]*5 {
			t.Errorf("gap[%d] = %v, want ~%v", i, gap, expected[i])
		}
	}
}

func TestWithRetry_ContextCancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	err := withRetry(ctx, 3, time.Millisecond, func() error {
		calls++
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if calls != 0 {
		t.Errorf("expected 0 calls, got %d", calls)
	}
}

func TestWithRetry_ContextCancelledDuringSleep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	calls := 0
	// Use a long backoff so the cancel fires during the sleep.
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	err := withRetry(ctx, 5, time.Second, func() error {
		calls++
		return errTest
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	// Should have made exactly 1 call before being cancelled during sleep.
	if calls != 1 {
		t.Errorf("expected 1 call before cancel, got %d", calls)
	}
}

// minimalConfig returns a *config.Config suitable for daemon unit tests.
// The repos slice is empty by default; callers can append to it.
func minimalConfig(interval time.Duration) *config.Config {
	return &config.Config{
		Daemon: config.DaemonConfig{
			Interval:      config.Duration{Duration: interval},
			RetryAttempts: 1,
			RetryBackoff:  config.Duration{Duration: time.Millisecond},
			LogLevel:      "info",
		},
	}
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestDaemon_Run_StopsOnContextCancel(t *testing.T) {
	cfg := minimalConfig(10 * time.Millisecond)
	d := New(cfg, silentLogger(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.Run(ctx, "")
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// Run returned as expected.
	case <-time.After(2 * time.Second):
		t.Error("Run did not return after context cancel")
	}
}

func TestDaemon_Run_CallsRunOnce(t *testing.T) {
	var syncCount atomic.Int32

	cfg := minimalConfig(10 * time.Millisecond)
	cfg.Repos = []config.RepoConfig{
		{
			Name:    "test",
			Source:  config.SourceConfig{URL: "/nonexistent"},
			Mirrors: []config.MirrorTarget{{URL: "/nonexistent-mirror"}},
		},
	}

	d := New(cfg, silentLogger(), nil)
	// Override the runner so we can count calls without hitting git.
	d.newRunner = func(repo config.RepoConfig, logger *slog.Logger) (syncer, error) {
		return &fakeSyncer{count: &syncCount}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	go d.Run(ctx, "")

	// Wait for at least 2 sync calls (initial + first tick).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if syncCount.Load() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	if syncCount.Load() < 2 {
		t.Errorf("expected at least 2 sync calls, got %d", syncCount.Load())
	}
}

func TestDaemon_Run_GracefulShutdown(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})

	cfg := minimalConfig(time.Hour) // long interval so only the initial runOnce fires
	cfg.Repos = []config.RepoConfig{
		{
			Name:    "slow",
			Source:  config.SourceConfig{URL: "/nonexistent"},
			Mirrors: []config.MirrorTarget{{URL: "/nonexistent-mirror"}},
		},
	}

	d := New(cfg, silentLogger(), nil)
	d.newRunner = func(repo config.RepoConfig, logger *slog.Logger) (syncer, error) {
		return &blockingSyncer{started: started, finished: finished}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		d.Run(ctx, "")
		close(runDone)
	}()

	// Wait for the goroutine to start its sync, then cancel.
	<-started
	cancel()

	// Run should not return until the in-flight sync completes.
	select {
	case <-runDone:
		t.Error("Run returned before in-flight sync finished")
	case <-time.After(50 * time.Millisecond):
		// Good — Run is still waiting.
	}

	// Unblock the syncer and verify Run now returns.
	close(finished)
	select {
	case <-runDone:
		// Run returned after sync completed.
	case <-time.After(2 * time.Second):
		t.Error("Run did not return after in-flight sync completed")
	}
}

// fakeSyncer counts how many times Sync is called.
type fakeSyncer struct {
	count *atomic.Int32
}

func (f *fakeSyncer) Sync(_ context.Context) []mirror.SyncResult {
	f.count.Add(1)
	return nil
}

// blockingSyncer signals started, then blocks until finished is closed.
type blockingSyncer struct {
	started  chan struct{}
	finished chan struct{}
}

func (b *blockingSyncer) Sync(_ context.Context) []mirror.SyncResult {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-b.finished
	return nil
}
