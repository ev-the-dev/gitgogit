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
	cmd := exec.Command("git", "init", "--bare", path)
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

	// Verify the mirror has refs (push --mirror overwrites the bare repo's HEAD).
	cmd := exec.Command("git", "-C", mirrorDir, "rev-parse", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse HEAD on mirror: %v\n%s", err, out)
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
