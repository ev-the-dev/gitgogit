package status

import (
	"sort"
	"sync"
	"time"

	"gitgogit/config"
	"gitgogit/mirror"
)

const maxRecentErrors = 50

type MirrorStatus struct {
	URL      string
	LastSync time.Time
	Success  bool
	Error    string
}

type RepoStatus struct {
	Name      string
	SourceURL string
	Mirrors   []MirrorStatus
	Syncing   bool
}

type ErrorEntry struct {
	RepoName  string
	MirrorURL string
	Error     string
	Time      time.Time
}

type Store struct {
	mu           sync.RWMutex
	repos        map[string]*RepoStatus
	recentErrors []ErrorEntry
	syncMu       map[string]*sync.Mutex
	startedAt    time.Time
	hasSuccess   bool
}

func NewStore() *Store {
	return &Store{
		repos:     make(map[string]*RepoStatus),
		syncMu:    make(map[string]*sync.Mutex),
		startedAt: time.Now(),
	}
}

// EnsureRepos seeds the store with repo entries so the dashboard shows
// repos before their first sync completes.
func (s *Store) EnsureRepos(repos []config.RepoConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range repos {
		if _, ok := s.repos[r.Name]; ok {
			continue
		}
		mirrors := make([]MirrorStatus, len(r.Mirrors))
		for i, m := range r.Mirrors {
			mirrors[i] = MirrorStatus{URL: m.URL}
		}
		s.repos[r.Name] = &RepoStatus{
			Name:      r.Name,
			SourceURL: r.Source.URL,
			Mirrors:   mirrors,
		}
		if _, ok := s.syncMu[r.Name]; !ok {
			s.syncMu[r.Name] = &sync.Mutex{}
		}
	}
}

// MarkSyncing sets the syncing flag for a repo so the dashboard can show it.
func (s *Store) MarkSyncing(repoName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rs, ok := s.repos[repoName]; ok {
		rs.Syncing = true
	}
}

// Record updates mirror statuses from sync results and clears the syncing flag.
func (s *Store) Record(repoName string, results []mirror.SyncResult) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rs, ok := s.repos[repoName]
	if !ok {
		rs = &RepoStatus{Name: repoName}
		s.repos[repoName] = rs
	}
	rs.Syncing = false

	now := time.Now()
	for _, r := range results {
		found := false
		for i := range rs.Mirrors {
			if rs.Mirrors[i].URL == r.MirrorURL {
				rs.Mirrors[i].LastSync = now
				rs.Mirrors[i].Success = r.Err == nil
				if r.Err != nil {
					rs.Mirrors[i].Error = r.Err.Error()
				} else {
					rs.Mirrors[i].Error = ""
				}
				found = true
				break
			}
		}
		if !found {
			ms := MirrorStatus{URL: r.MirrorURL, LastSync: now, Success: r.Err == nil}
			if r.Err != nil {
				ms.Error = r.Err.Error()
			}
			rs.Mirrors = append(rs.Mirrors, ms)
		}

		if r.Err != nil {
			s.recentErrors = append(s.recentErrors, ErrorEntry{
				RepoName:  repoName,
				MirrorURL: r.MirrorURL,
				Error:     r.Err.Error(),
				Time:      now,
			})
			if len(s.recentErrors) > maxRecentErrors {
				s.recentErrors = s.recentErrors[len(s.recentErrors)-maxRecentErrors:]
			}
		} else {
			s.hasSuccess = true
		}
	}
}

// All returns a snapshot of all repo statuses sorted by name.
func (s *Store) All() []RepoStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]RepoStatus, 0, len(s.repos))
	for _, rs := range s.repos {
		cp := RepoStatus{
			Name:      rs.Name,
			SourceURL: rs.SourceURL,
			Mirrors:   make([]MirrorStatus, len(rs.Mirrors)),
			Syncing:   rs.Syncing,
		}
		copy(cp.Mirrors, rs.Mirrors)
		result = append(result, cp)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// RecentErrors returns up to the last maxRecentErrors errors.
func (s *Store) RecentErrors() []ErrorEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make([]ErrorEntry, len(s.recentErrors))
	copy(cp, s.recentErrors)
	return cp
}

// IsHealthy returns true if at least one sync has succeeded.
func (s *Store) IsHealthy() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasSuccess
}

// StartedAt returns when the store was created.
func (s *Store) StartedAt() time.Time {
	return s.startedAt
}

// TryLockRepo attempts to acquire a per-repo lock for manual sync.
// Returns an unlock function and true if acquired, or nil and false if already held.
func (s *Store) TryLockRepo(name string) (unlock func(), ok bool) {
	s.mu.RLock()
	mu, exists := s.syncMu[name]
	s.mu.RUnlock()
	if !exists {
		return nil, false
	}
	if !mu.TryLock() {
		return nil, false
	}
	return mu.Unlock, true
}
