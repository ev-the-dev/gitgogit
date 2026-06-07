package status

import (
	"errors"
	"sync"
	"testing"

	"gitgogit/config"
	"gitgogit/mirror"
)

func TestEnsureRepos_SeedsStore(t *testing.T) {
	s := NewStore()
	s.EnsureRepos([]config.RepoConfig{
		{
			Name:    "repo1",
			Source:  config.SourceConfig{URL: "https://github.com/org/repo1.git"},
			Mirrors: []config.MirrorTarget{{URL: "https://mirror.com/repo1.git"}},
		},
	})

	all := s.All()
	if len(all) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(all))
	}
	if all[0].Name != "repo1" {
		t.Errorf("name = %q, want %q", all[0].Name, "repo1")
	}
	if all[0].Mirrors[0].URL != "https://mirror.com/repo1.git" {
		t.Errorf("mirror URL = %q", all[0].Mirrors[0].URL)
	}
	if !all[0].Mirrors[0].LastSync.IsZero() {
		t.Error("expected zero LastSync before any sync")
	}
}

func TestRecord_UpdatesMirrorStatus(t *testing.T) {
	s := NewStore()
	s.EnsureRepos([]config.RepoConfig{
		{
			Name:    "repo1",
			Source:  config.SourceConfig{URL: "https://github.com/org/repo1.git"},
			Mirrors: []config.MirrorTarget{{URL: "https://mirror.com/repo1.git"}},
		},
	})

	s.Record("repo1", []mirror.SyncResult{
		{Repo: "repo1", MirrorURL: "https://mirror.com/repo1.git", Err: nil},
	})

	all := s.All()
	if !all[0].Mirrors[0].Success {
		t.Error("expected success after recording nil error")
	}
	if all[0].Mirrors[0].LastSync.IsZero() {
		t.Error("expected non-zero LastSync after recording")
	}
}

func TestRecord_TracksErrors(t *testing.T) {
	s := NewStore()
	s.Record("repo1", []mirror.SyncResult{
		{Repo: "repo1", MirrorURL: "https://mirror.com/repo1.git", Err: errors.New("push failed")},
	})

	errs := s.RecentErrors()
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if errs[0].Error != "push failed" {
		t.Errorf("error = %q, want %q", errs[0].Error, "push failed")
	}
}

func TestRecentErrors_RingBuffer(t *testing.T) {
	s := NewStore()
	for i := range 60 {
		s.Record("repo1", []mirror.SyncResult{
			{Repo: "repo1", MirrorURL: "https://mirror.com/repo1.git", Err: errors.New("err")},
		})
		_ = i
	}

	errs := s.RecentErrors()
	if len(errs) != maxRecentErrors {
		t.Errorf("expected %d errors, got %d", maxRecentErrors, len(errs))
	}
}

func TestIsHealthy(t *testing.T) {
	s := NewStore()
	if s.IsHealthy() {
		t.Error("expected unhealthy before any sync")
	}

	s.Record("repo1", []mirror.SyncResult{
		{Repo: "repo1", MirrorURL: "https://mirror.com/repo1.git", Err: nil},
	})

	if !s.IsHealthy() {
		t.Error("expected healthy after successful sync")
	}
}

func TestTryLockRepo_PreventsDuplicates(t *testing.T) {
	s := NewStore()
	s.EnsureRepos([]config.RepoConfig{
		{Name: "repo1", Source: config.SourceConfig{URL: "u"}, Mirrors: []config.MirrorTarget{{URL: "m"}}},
	})

	unlock, ok := s.TryLockRepo("repo1")
	if !ok {
		t.Fatal("expected first lock to succeed")
	}

	_, ok2 := s.TryLockRepo("repo1")
	if ok2 {
		t.Error("expected second lock to fail")
	}

	unlock()

	_, ok3 := s.TryLockRepo("repo1")
	if !ok3 {
		t.Error("expected lock to succeed after unlock")
	}
}

func TestTryLockRepo_UnknownRepo(t *testing.T) {
	s := NewStore()
	_, ok := s.TryLockRepo("nonexistent")
	if ok {
		t.Error("expected lock to fail for unknown repo")
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := NewStore()
	s.EnsureRepos([]config.RepoConfig{
		{Name: "repo1", Source: config.SourceConfig{URL: "u"}, Mirrors: []config.MirrorTarget{{URL: "m"}}},
	})

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.Record("repo1", []mirror.SyncResult{
				{Repo: "repo1", MirrorURL: "m", Err: nil},
			})
		}()
		go func() {
			defer wg.Done()
			_ = s.All()
		}()
	}
	wg.Wait()
}
