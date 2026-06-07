package mirror

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gitgogit/config"
)

// hasGit skips the test if git is not available on PATH.
func hasGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH")
	}
}

// initBareRepo creates a bare git repository at path.
func initBareRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	// Use -b main so the bare repo's HEAD matches the source branch created by
	// initRepoWithCommit; otherwise the default branch name depends on the host's
	// init.defaultBranch and `git log` on the mirror fails when they differ.
	cmd := exec.Command("git", "init", "--bare", "-b", "main", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init --bare: %v", err)
	}
}

// initRepoWithCommit creates a non-bare repo, makes a commit, and returns its path.
func initRepoWithCommit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "-b", "main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")

	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial commit")
	return dir
}

func TestRunner_Sync_LocalRepos(t *testing.T) {
	hasGit(t)

	sourceDir := initRepoWithCommit(t)
	mirrorDir := t.TempDir()
	initBareRepo(t, mirrorDir)

	cacheDir := filepath.Join(t.TempDir(), "cache.git")

	repo := config.RepoConfig{
		Name: "testrepo",
		Source: config.SourceConfig{
			URL: sourceDir,
		},
		Mirrors: []config.MirrorTarget{
			{URL: mirrorDir},
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runner := &Runner{
		Repo:     repo,
		CacheDir: cacheDir,
		Logger:   logger,
	}

	ctx := context.Background()
	results := runner.Sync(ctx)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err != nil {
		t.Errorf("Sync() error: %v", results[0].Err)
	}

	// Verify the mirror has the commit.
	cmd := exec.Command("git", "-C", mirrorDir, "log", "--oneline")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log on mirror: %v\n%s", err, out)
	}
	if len(out) == 0 {
		t.Error("mirror has no commits after sync")
	}
}

func TestRunner_Sync_Idempotent(t *testing.T) {
	hasGit(t)

	sourceDir := initRepoWithCommit(t)
	mirrorDir := t.TempDir()
	initBareRepo(t, mirrorDir)
	cacheDir := filepath.Join(t.TempDir(), "cache.git")

	repo := config.RepoConfig{
		Name:   "idempotent",
		Source: config.SourceConfig{URL: sourceDir},
		Mirrors: []config.MirrorTarget{
			{URL: mirrorDir},
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runner := &Runner{Repo: repo, CacheDir: cacheDir, Logger: logger}
	ctx := context.Background()

	// First sync.
	results := runner.Sync(ctx)
	for _, r := range results {
		if r.Err != nil {
			t.Fatalf("first Sync() error: %v", r.Err)
		}
	}

	// Second sync should also succeed (idempotent).
	results = runner.Sync(ctx)
	for _, r := range results {
		if r.Err != nil {
			t.Fatalf("second Sync() error: %v", r.Err)
		}
	}
}

func TestRunner_EnsureCloned_InvalidSource(t *testing.T) {
	hasGit(t)

	repo := config.RepoConfig{
		Name:    "bad",
		Source:  config.SourceConfig{URL: "/nonexistent/path/repo.git"},
		Mirrors: []config.MirrorTarget{{URL: "/tmp/mirror"}},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	runner := &Runner{
		Repo:     repo,
		CacheDir: filepath.Join(t.TempDir(), "cache.git"),
		Logger:   logger,
	}

	results := runner.Sync(context.Background())
	if len(results) == 0 {
		t.Fatal("expected results for failed sync")
	}
	for _, r := range results {
		if r.Err == nil {
			t.Error("expected error for invalid source URL")
		}
	}
}

func TestRunner_Sync_ExcludeRefs(t *testing.T) {
	hasGit(t)

	sourceDir := initRepoWithCommit(t)
	mirrorDir := t.TempDir()
	initBareRepo(t, mirrorDir)
	cacheDir := filepath.Join(t.TempDir(), "cache.git")

	// Clone source into cache, then create a fake refs/pull ref to simulate a GitHub PR ref.
	runner := &Runner{
		Repo: config.RepoConfig{
			Name:    "excludetest",
			Source:  config.SourceConfig{URL: sourceDir},
			Mirrors: []config.MirrorTarget{{URL: mirrorDir}},
		},
		CacheDir: cacheDir,
		Logger:   slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}
	ctx := context.Background()
	if err := runner.EnsureCloned(ctx); err != nil {
		t.Fatalf("EnsureCloned: %v", err)
	}

	// Write a fake refs/pull/1/head into the bare cache to simulate what GitHub ships.
	refPath := filepath.Join(cacheDir, "refs", "pull", "1")
	if err := os.MkdirAll(refPath, 0o755); err != nil {
		t.Fatal(err)
	}
	headRef := filepath.Join(cacheDir, "HEAD")
	headData, err := os.ReadFile(headRef)
	if err != nil {
		t.Fatal(err)
	}
	// Resolve HEAD to a SHA so we can write a valid ref.
	cmd := exec.Command("git", "-C", cacheDir, "rev-parse", "HEAD")
	sha, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	if err := os.WriteFile(filepath.Join(refPath, "head"), sha, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = headData

	results := runner.Sync(ctx)
	for _, r := range results {
		if r.Err != nil {
			t.Fatalf("Sync() error: %v", r.Err)
		}
	}

	// refs/pull/1/head must NOT exist in the mirror.
	cmd = exec.Command("git", "-C", mirrorDir, "show-ref", "refs/pull/1/head")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Errorf("refs/pull/1/head was pushed to mirror but should have been excluded: %s", out)
	}
}

func TestRedactURL(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"https://oauth2:secret@gitlab.com/org/repo.git", "https://oauth2:xxxxx@gitlab.com/org/repo.git"},
		{"git@github.com:org/repo.git", "git@github.com:org/repo.git"},
		{"https://gitlab.com/org/repo.git", "https://gitlab.com/org/repo.git"},
		{"not a url", "not a url"},
	}
	for _, c := range cases {
		got := redactURL(c.input)
		if got != c.want {
			t.Errorf("redactURL(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
