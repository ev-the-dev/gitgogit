package mirror

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gitgogit/auth"
	"gitgogit/config"
)

// SyncResult captures the outcome of one sync attempt for one mirror target.
type SyncResult struct {
	Repo      string
	MirrorURL string
	Err       error
}

// Runner executes git subprocesses for one RepoConfig.
type Runner struct {
	Repo     config.RepoConfig
	CacheDir string // ~/.local/share/gitgogit/repos/{name}.git
	Logger   *slog.Logger
}

// NewRunner constructs a Runner with the default cache directory.
func NewRunner(repo config.RepoConfig, logger *slog.Logger) *Runner {
	home, _ := os.UserHomeDir()
	cacheDir := filepath.Join(home, ".local", "share", "gitgogit", "repos", repo.Name+".git")
	return &Runner{
		Repo:     repo,
		CacheDir: cacheDir,
		Logger:   logger,
	}
}

// EnsureCloned clones the source as a bare mirror if the cache dir doesn't exist.
// If the directory exists but appears incomplete (no HEAD file), it removes and re-clones.
func (r *Runner) EnsureCloned(ctx context.Context) error {
	headPath := filepath.Join(r.CacheDir, "HEAD")
	if _, err := os.Stat(headPath); err == nil {
		return nil // already cloned
	}

	// Remove any partial clone directory before attempting a fresh clone.
	if _, err := os.Stat(r.CacheDir); err == nil {
		if err := os.RemoveAll(r.CacheDir); err != nil {
			return fmt.Errorf("remove partial clone: %w", err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(r.CacheDir), 0o750); err != nil {
		return fmt.Errorf("create cache parent dir: %w", err)
	}

	provider, err := auth.Resolve(r.Repo.Source.Auth)
	if err != nil {
		return err
	}
	resolvedURL, extraEnv, err := provider.Prepare(r.Repo.Source.URL, r.Repo.Source.Auth)
	if err != nil {
		return err
	}

	r.Logger.Info("cloning source repo", "repo", r.Repo.Name, "url", redactURL(resolvedURL))
	return r.runGit(ctx, extraEnv, "clone", "--mirror", resolvedURL, r.CacheDir)
}

// Fetch fetches all refs from origin into the bare clone.
func (r *Runner) Fetch(ctx context.Context) error {
	provider, err := auth.Resolve(r.Repo.Source.Auth)
	if err != nil {
		return err
	}
	_, extraEnv, err := provider.Prepare(r.Repo.Source.URL, r.Repo.Source.Auth)
	if err != nil {
		return err
	}
	r.Logger.Info("fetching", "repo", r.Repo.Name)
	return r.runGit(ctx, extraEnv, "-C", r.CacheDir, "fetch", "--prune", "origin")
}

// Push pushes all refs to one mirror target.
func (r *Runner) Push(ctx context.Context, target config.MirrorTarget) error {
	provider, err := auth.Resolve(target.Auth)
	if err != nil {
		return err
	}
	resolvedURL, extraEnv, err := provider.Prepare(target.URL, target.Auth)
	if err != nil {
		return err
	}
	r.Logger.Info("pushing", "repo", r.Repo.Name, "mirror", redactURL(resolvedURL))
	return r.runGit(ctx, extraEnv, "-C", r.CacheDir, "push", "--mirror", resolvedURL)
}

// Sync clones if needed, fetches, then pushes to all configured mirrors. Returns one SyncResult per mirror target.
func (r *Runner) Sync(ctx context.Context) []SyncResult {
	var results []SyncResult

	if err := r.EnsureCloned(ctx); err != nil {
		for _, m := range r.Repo.Mirrors {
			results = append(results, SyncResult{
				Repo:      r.Repo.Name,
				MirrorURL: m.URL,
				Err:       fmt.Errorf("clone: %w", err),
			})
		}
		return results
	}

	if err := r.Fetch(ctx); err != nil {
		for _, m := range r.Repo.Mirrors {
			results = append(results, SyncResult{
				Repo:      r.Repo.Name,
				MirrorURL: m.URL,
				Err:       fmt.Errorf("fetch: %w", err),
			})
		}
		return results
	}

	for _, m := range r.Repo.Mirrors {
		err := r.Push(ctx, m)
		results = append(results, SyncResult{
			Repo:      r.Repo.Name,
			MirrorURL: m.URL,
			Err:       err,
		})
	}
	return results
}

// runGit runs git with the given arguments and extra environment variables.
// It uses git -C <dir> rather than os.Chdir to avoid process-wide directory changes.
// Output is captured and included in the error on failure.
func (r *Runner) runGit(ctx context.Context, extraEnv []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, buf.String())
	}
	return nil
}

// redactURL replaces userinfo in a URL with [REDACTED] for safe logging.
func redactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return rawURL
	}
	return u.Redacted()
}
