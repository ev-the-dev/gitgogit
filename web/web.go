package web

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"time"

	"gitgogit/status"
)

// Triggerer is the subset of daemon.Daemon needed by the web handlers.
type Triggerer interface {
	TriggerSync(ctx context.Context, repoName string) error
}

type Server struct {
	store   *status.Store
	trigger Triggerer
	logger  *slog.Logger
	httpSrv *http.Server
}

func New(store *status.Store, trigger Triggerer, logger *slog.Logger) *Server {
	return &Server{store: store, trigger: trigger, logger: logger}
}

func (s *Server) Start(listen string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleDashboard)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("POST /sync/{repo}", s.handleSyncTrigger)

	s.httpSrv = &http.Server{
		Addr:              listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listen, err)
	}

	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error("web server error", slog.String("error", err.Error()))
		}
	}()

	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.store.IsHealthy() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unhealthy"}`))
	}
}

func (s *Server) handleSyncTrigger(w http.ResponseWriter, r *http.Request) {
	repoName := r.PathValue("repo")
	// Use a detached context so the sync isn't cancelled when the HTTP
	// request completes (browser redirects away after the POST).
	if err := s.trigger.TriggerSync(context.Background(), repoName); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type dashboardData struct {
	Repos        []status.RepoStatus
	RecentErrors []status.ErrorEntry
	Uptime       string
	Now          time.Time
	AnySyncing   bool
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	repos := s.store.All()
	anySyncing := false
	for _, repo := range repos {
		if repo.Syncing {
			anySyncing = true
			break
		}
	}
	data := dashboardData{
		Repos:        repos,
		RecentErrors: s.store.RecentErrors(),
		Uptime:       time.Since(s.store.StartedAt()).Truncate(time.Second).String(),
		Now:          time.Now(),
		AnySyncing:   anySyncing,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := dashboardTmpl.Execute(w, data); err != nil {
		s.logger.Error("render dashboard", slog.String("error", err.Error()))
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Format("2006-01-02 15:04:05")
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t).Truncate(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh ago", int(d.Hours()))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

var funcMap = template.FuncMap{
	"formatTime": formatTime,
	"timeAgo":    timeAgo,
	"truncate":   truncate,
}

var dashboardTmpl = template.Must(template.New("dashboard").Funcs(funcMap).Parse(dashboardHTML))

const dashboardHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
{{if .AnySyncing}}<meta http-equiv="refresh" content="3">{{else}}<meta http-equiv="refresh" content="30">{{end}}
<title>gitgogit</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: monospace; background: #1a1a2e; color: #e0e0e0; padding: 2rem; }
  h1 { color: #e94560; margin-bottom: 0.5rem; }
  .meta { color: #888; margin-bottom: 1.5rem; font-size: 0.9rem; }
  table { width: 100%; border-collapse: collapse; margin-bottom: 2rem; }
  th { text-align: left; padding: 0.5rem; border-bottom: 2px solid #333; color: #e94560; }
  td { padding: 0.5rem; border-bottom: 1px solid #2a2a3e; }
  tr:hover { background: #16213e; }
  .ok { color: #4ecca3; }
  .err { color: #e94560; }
  .pending { color: #888; }
  .syncing { color: #f0a500; }
  .error-msg { font-size: 0.8rem; color: #e94560; max-width: 400px; word-break: break-all; }
  button { background: #16213e; color: #e0e0e0; border: 1px solid #333; padding: 0.3rem 0.8rem;
           cursor: pointer; font-family: monospace; font-size: 0.85rem; }
  button:hover { background: #0f3460; border-color: #e94560; }
  h2 { color: #e94560; margin-bottom: 0.5rem; }
  .errors-section { margin-top: 1rem; }
  .error-entry { padding: 0.4rem 0; border-bottom: 1px solid #2a2a3e; font-size: 0.85rem; }
  .error-time { color: #888; }
</style>
</head>
<body>
<h1>gitgogit</h1>
<div class="meta">uptime: {{.Uptime}}</div>

<table>
<tr>
  <th>Repo</th>
  <th>Source</th>
  <th>Mirror</th>
  <th>Last Sync</th>
  <th>Status</th>
  <th>Error</th>
  <th>Action</th>
</tr>
{{range .Repos}}
{{$repo := .Name}}
{{$source := .SourceURL}}
{{$syncing := .Syncing}}
{{range .Mirrors}}
<tr>
  <td>{{$repo}}</td>
  <td>{{$source}}</td>
  <td>{{.URL}}</td>
  <td>{{formatTime .LastSync}}{{if not .LastSync.IsZero}} <span class="meta">({{timeAgo .LastSync}})</span>{{end}}</td>
  <td>{{if .LastSync.IsZero}}<span class="pending">pending</span>{{else if .Success}}<span class="ok">ok</span>{{else}}<span class="err">error</span>{{end}}</td>
  <td>{{if .Error}}<span class="error-msg">{{truncate .Error 120}}</span>{{end}}</td>
  <td>{{if $syncing}}<span class="syncing">syncing...</span>{{else}}<form method="POST" action="/sync/{{$repo}}" style="display:inline"><button type="submit">sync</button></form>{{end}}</td>
</tr>
{{end}}
{{end}}
</table>

{{if .RecentErrors}}
<div class="errors-section">
<h2>Recent Errors</h2>
{{range .RecentErrors}}
<div class="error-entry">
  <span class="error-time">{{formatTime .Time}}</span>
  <strong>{{.RepoName}}</strong> &rarr; {{.MirrorURL}}: {{truncate .Error 200}}
</div>
{{end}}
</div>
{{end}}

</body>
</html>`
