// Package api exposes the tmux workspace over HTTP and WebSocket.
package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/viminizer/remux/internal/agent"
	"github.com/viminizer/remux/internal/config"
	gh "github.com/viminizer/remux/internal/github"
	"github.com/viminizer/remux/internal/push"
	"github.com/viminizer/remux/internal/tmux"
)

// Server carries everything the handlers need.
type Server struct {
	Cfg  *config.Config
	Tmux *tmux.Client
	Push *push.Store
	// GH is the shared GitHub poller, GHClient the on-demand reads, and
	// Match the pane/repo link. All three are nil when the GitHub screen is
	// switched off, and every handler checks.
	GH       *gh.Poller
	GHClient *gh.Client
	Match    *gh.Matcher
	Auth     *Auth        // nil under --local
	Web      http.Handler // embedded UI
	Audit    *AuditLog

	startedAt time.Time
	mu        sync.Mutex
	// paneRepos is the repo each pane is checked out in, kept fresh by
	// RefreshPaneRepos so no request has to touch the filesystem.
	paneRepos map[string][]string
}

func NewServer(cfg *config.Config, tm *tmux.Client, web http.Handler) *Server {
	return &Server{Cfg: cfg, Tmux: tm, Web: web, startedAt: time.Now()}
}

// Handler builds the full route table.
//
// Every mutating route runs through requireAuth (a no-op under --local, where
// the listener is loopback-only) and is written to the audit log.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/tree", s.handleTree)
	mux.HandleFunc("GET /api/panes/{id}/capture", s.handleCapture)

	mux.HandleFunc("POST /api/panes/{id}/text", s.handleText)
	mux.HandleFunc("POST /api/panes/{id}/keys", s.handleKeys)
	mux.HandleFunc("POST /api/panes/{id}/interrupt", s.handleInterrupt)
	mux.HandleFunc("POST /api/panes/{id}/focus", s.handleFocus)
	mux.HandleFunc("PATCH /api/panes/{id}", s.handleRenamePane)
	mux.HandleFunc("DELETE /api/panes/{id}", s.handleKillPane)

	mux.HandleFunc("POST /api/panes", s.handleNewPane)
	mux.HandleFunc("POST /api/sessions", s.handleNewSession)
	mux.HandleFunc("PATCH /api/sessions/{id}", s.handleRenameSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleKillSession)

	mux.HandleFunc("POST /api/windows", s.handleNewWindow)
	mux.HandleFunc("PATCH /api/windows/{id}", s.handleRenameWindow)
	mux.HandleFunc("DELETE /api/windows/{id}", s.handleKillWindow)

	if s.GH != nil {
		mux.HandleFunc("GET /api/github", s.handleGitHub)
		mux.HandleFunc("POST /api/github/refresh", s.handleGitHubRefresh)
		mux.HandleFunc("GET /api/github/picker", s.handleGitHubPicker)
		mux.HandleFunc("POST /api/github/watch", s.handleGitHubWatch)
		mux.HandleFunc("DELETE /api/github/watch/{owner}/{name}", s.handleGitHubUnwatch)
		mux.HandleFunc("GET /api/github/repos/{owner}/{name}/issues", s.handleGitHubIssues)
		mux.HandleFunc("GET /api/github/repos/{owner}/{name}/issues/{number}", s.handleGitHubIssue)
		mux.HandleFunc("GET /api/github/repos/{owner}/{name}/prs", s.handleGitHubPRs)
		mux.HandleFunc("GET /api/github/repos/{owner}/{name}/prs/{number}", s.handleGitHubPR)
	}

	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handlePutSettings)

	mux.HandleFunc("GET /api/push/key", s.handlePushKey)
	mux.HandleFunc("POST /api/push/subscribe", s.handlePushSubscribe)
	mux.HandleFunc("POST /api/push/unsubscribe", s.handlePushUnsubscribe)

	mux.HandleFunc("/ws", s.handleWS)

	if s.Web != nil {
		mux.Handle("/", s.Web)
	}
	return logRequests(s.withAuth(mux))
}

// logRequests records every API call: method, path, status, size, duration.
//
// The UI's own assets are skipped, so this is one line per thing the phone
// actually asks the Mac to do - a handful a minute, since the WebSocket
// carries the rest. It exists because a screen that came up empty gave no way
// to tell a failing request from one that was never sent, and those want
// completely different fixes.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		log.Printf("%s %s -> %d %dB %dms",
			r.Method, r.URL.RequestURI(), rec.code, rec.n, time.Since(start).Milliseconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	code int
	n    int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	n, err := s.ResponseWriter.Write(b)
	s.n += n
	return n, err
}

// ── helpers ───────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

// paneID pulls {id} out of the path and validates it before it can reach
// tmux. IDs arrive URL-escaped because they start with '%'.
func paneID(r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !strings.HasPrefix(id, "%") {
		id = "%" + strings.TrimPrefix(id, "%25")
	}
	return id, tmux.ValidPaneID(id)
}

// ── read routes ───────────────────────────────────────────────────────────

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	version, err := s.Tmux.Version(r.Context())
	tmuxOK := err == nil

	tree, terr := s.Tmux.Tree(r.Context())
	panes := 0
	if terr == nil {
		panes = len(tree.Panes())
	}

	host, _ := hostname()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"version":     Version,
		"hostname":    host,
		"tmux":        version,
		"tmuxRunning": tmuxOK,
		"panes":       panes,
		"uptimeSec":   int(time.Since(s.startedAt).Seconds()),
		"login":       s.callerLogin(r),
		"time":        time.Now().UnixMilli(),
	})
}

// handleTree returns the whole hierarchy, with agent status on every pane.
//
// Status needs the pane's screen, so ?preview=1 fetches the last 40 lines of
// every pane in parallel and classifies from those. Without it the tree is one
// exec and no captures.
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	tree, err := s.Tmux.Tree(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if r.URL.Query().Get("preview") != "" {
		s.annotate(r, tree, true)
	}
	writeJSON(w, http.StatusOK, tree)
}

// annotate fills in Status (and optionally Preview) for every pane.
func (s *Server) annotate(r *http.Request, tree *tmux.Tree, withPreview bool) {
	panes := tree.Panes()
	ids := make([]string, 0, len(panes))
	for _, p := range panes {
		ids = append(ids, p.ID)
	}
	screens := s.Tmux.Previews(r.Context(), ids, tmux.PreviewLines)
	for _, p := range panes {
		screen := screens[p.ID]
		p.Status = string(agent.Classify(p.Command, p.Title, screen))
		if withPreview {
			p.Preview = lastLine(screen)
		}
	}
}

// lastLine is the one line of preview the drawer can actually show.
func lastLine(screen string) string {
	lines := strings.Split(strings.TrimRight(agent.StripANSI(screen), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			if len(t) > 120 {
				t = t[:120]
			}
			return t
		}
	}
	return ""
}

func (s *Server) handleCapture(w http.ResponseWriter, r *http.Request) {
	id, ok := paneID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid pane id")
		return
	}
	lines := s.Cfg.Lines
	if n := r.URL.Query().Get("lines"); n != "" {
		if v := atoiClamp(n, 20, 5000); v > 0 {
			lines = v
		}
	}
	text, err := s.Tmux.Capture(r.Context(), id, lines, true)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pane":  id,
		"lines": strings.Split(text, "\n"),
	})
}

// ── write routes ──────────────────────────────────────────────────────────

func (s *Server) handleText(w http.ResponseWriter, r *http.Request) {
	id, ok := paneID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid pane id")
		return
	}
	var body struct {
		Text   string `json:"text"`
		Submit bool   `json:"submit"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Tmux.SendText(r.Context(), id, body.Text, body.Submit); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "text", id, truncate(body.Text, 400))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	id, ok := paneID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid pane id")
		return
	}
	var body struct {
		Keys []string `json:"keys"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Tmux.SendKeys(r.Context(), id, body.Keys); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "keys", id, strings.Join(body.Keys, " "))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleInterrupt(w http.ResponseWriter, r *http.Request) {
	id, ok := paneID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid pane id")
		return
	}
	if err := s.Tmux.Interrupt(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "interrupt", id, "C-c")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleFocus is the only route that moves the laptop's own cursor, and the
// UI labels it as such.
func (s *Server) handleFocus(w http.ResponseWriter, r *http.Request) {
	id, ok := paneID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid pane id")
		return
	}
	if err := s.Tmux.Focus(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "focus", id, "select-window + select-pane + switch-client")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleKillPane(w http.ResponseWriter, r *http.Request) {
	id, ok := paneID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid pane id")
		return
	}
	if err := s.Tmux.KillPane(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "kill-pane", id, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleNewSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.Tmux.NewSession(r.Context(), body.Name, expandHome(body.Path))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "new-session", id, body.Name)
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

// handleNewPane splits an existing pane in two.
//
// Splitting halves the pane it targets, which reflows that program's TUI if
// one is running there. That used to be refused outright; see the note in the
// body for what #23 measured and why the deliberateness moved to a hold on the
// phone instead.
//
// Only the target pane is affected: a shell beside a running Claude Code is
// split without the agent's pane moving at all. That is why splitting is
// allowed to exist beside the forbidden resize commands - it changes one
// pane's geometry, the one that was named, and nothing else in the window.
func (s *Server) handleNewPane(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pane      string `json:"pane"`
		Direction string `json:"direction"` // "right" or "below"
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := tmux.CheckPaneID(body.Pane); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// An agent pane used to be refused here with a 409. #23 measured what that
	// cost: applying the same rule to the live workspace, 13 of 17 windows had
	// no splittable pane at all, because a window is usually opened to run one
	// agent and that pane is then the only one in it. A safety rule that blocks
	// 76% of windows is not preventing a mistake, it is removing the feature.
	//
	// So the refusal is gone and the deliberateness moved to the phone, where
	// splitting an agent pane is behind a press-and-hold. That is the same
	// place Kill pane draws the line, and for a much smaller consequence: both
	// Codex and Claude Code handle SIGWINCH and redraw, so the cost is a
	// narrower screen, not a lost task.
	//
	// The command is still read, but only to record what was reflowed. A pane
	// that vanishes between the lookup and the split is the split's problem to
	// report, not the audit line's, so a failure here does not block it.
	cmd, _ := s.Tmux.CommandOf(r.Context(), body.Pane)
	id, err := s.Tmux.SplitPane(r.Context(), body.Pane, body.Direction == "right")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	note := "split " + body.Pane + " " + body.Direction
	if agent.IsAgent(cmd) {
		note += " (reflowed " + agent.DisplayCommand(cmd) + ")"
	}
	s.audit(r, "new-pane", id, note)
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) handleNewWindow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"sessionId"`
		Name      string `json:"name"`
		Path      string `json:"path"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.Tmux.NewWindow(r.Context(), body.SessionID, body.Name, expandHome(body.Path))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "new-window", id, body.Name)
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) handleRenameSession(w http.ResponseWriter, r *http.Request) {
	s.rename(w, r, s.Tmux.RenameSession)
}

func (s *Server) handleRenameWindow(w http.ResponseWriter, r *http.Request) {
	s.rename(w, r, s.Tmux.RenameWindow)
}

// Naming a pane sets @remux_title, not the real pane title - see SetPaneTitle.
func (s *Server) handleRenamePane(w http.ResponseWriter, r *http.Request) {
	s.rename(w, r, s.Tmux.SetPaneTitle)
}

func (s *Server) rename(w http.ResponseWriter, r *http.Request, do func(context.Context, string, string) error) {
	id := r.PathValue("id")
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := do(r.Context(), id, body.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "rename", id, body.Name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleKillSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Tmux.KillSession(r.Context(), id); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "kill-session", id, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleKillWindow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Tmux.KillWindow(r.Context(), id); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "kill-window", id, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
