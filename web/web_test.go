package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitgogit/config"
	"gitgogit/mirror"
	"gitgogit/status"
)

type fakeTrigger struct {
	called   string
	returnErr error
}

func (f *fakeTrigger) TriggerSync(_ context.Context, name string) error {
	f.called = name
	return f.returnErr
}

func seedStore() *status.Store {
	s := status.NewStore()
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
	return s
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHealthz_Healthy(t *testing.T) {
	store := seedStore()
	srv := New(store, &fakeTrigger{}, testLogger())

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	srv.handleHealthz(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `"ok"`) {
		t.Errorf("body = %q, want ok", w.Body.String())
	}
}

func TestHealthz_Unhealthy(t *testing.T) {
	store := status.NewStore()
	srv := New(store, &fakeTrigger{}, testLogger())

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	srv.handleHealthz(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestDashboard_Renders(t *testing.T) {
	store := seedStore()
	srv := New(store, &fakeTrigger{}, testLogger())

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.handleDashboard(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "repo1") {
		t.Error("dashboard does not contain repo name")
	}
	if !strings.Contains(body, "https://mirror.com/repo1.git") {
		t.Error("dashboard does not contain mirror URL")
	}
	if !strings.Contains(body, "ok") {
		t.Error("dashboard does not contain ok status")
	}
}

func TestSyncTrigger_Success(t *testing.T) {
	store := seedStore()
	trigger := &fakeTrigger{}
	srv := New(store, trigger, testLogger())

	req := httptest.NewRequest("POST", "/sync/repo1", nil)
	req.SetPathValue("repo", "repo1")
	w := httptest.NewRecorder()
	srv.handleSyncTrigger(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", w.Code, http.StatusSeeOther)
	}
	if trigger.called != "repo1" {
		t.Errorf("trigger called with %q, want %q", trigger.called, "repo1")
	}
}

func TestSyncTrigger_Error(t *testing.T) {
	store := seedStore()
	trigger := &fakeTrigger{returnErr: fmt.Errorf("sync already in progress for %q", "repo1")}
	srv := New(store, trigger, testLogger())

	req := httptest.NewRequest("POST", "/sync/repo1", nil)
	req.SetPathValue("repo", "repo1")
	w := httptest.NewRecorder()
	srv.handleSyncTrigger(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestDashboard_ShowsErrors(t *testing.T) {
	store := status.NewStore()
	store.EnsureRepos([]config.RepoConfig{
		{
			Name:    "repo1",
			Source:  config.SourceConfig{URL: "https://github.com/org/repo1.git"},
			Mirrors: []config.MirrorTarget{{URL: "https://mirror.com/repo1.git"}},
		},
	})
	store.Record("repo1", []mirror.SyncResult{
		{Repo: "repo1", MirrorURL: "https://mirror.com/repo1.git", Err: errors.New("push failed")},
	})

	srv := New(store, &fakeTrigger{}, testLogger())
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.handleDashboard(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "push failed") {
		t.Error("dashboard does not show error message")
	}
	if !strings.Contains(body, "Recent Errors") {
		t.Error("dashboard does not show recent errors section")
	}
}
