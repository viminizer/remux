package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/viminizer/remux/internal/config"
	"github.com/viminizer/remux/internal/harness"
	"github.com/viminizer/remux/internal/push"
	"github.com/viminizer/remux/internal/tmux"
)

// The agent harness, server side. remux stays thin here: it reads each loop's
// state from its tmux session, starts and stops loop sessions, and moves
// labels when Kevin answers a stuck agent. The loops do the work.

var scopeRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,19}$`)

// loopExe is the binary a loop session runs. A variable so tests can name one.
var loopExe = os.Executable

func (s *Server) loopRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/loops", s.handleLoops)
	mux.HandleFunc("POST /api/loops", s.handleStartLoops)
	mux.HandleFunc("PATCH /api/loops/{name}", s.handleEditLoop)
	mux.HandleFunc("DELETE /api/loops/{name}", s.handleStopLoop)
	mux.HandleFunc("PUT /api/loops/presets", s.handlePutPresets)
	mux.HandleFunc("GET /api/harness/inbox", s.handleHarnessInbox)
	mux.HandleFunc("POST /api/loops/supervisor", s.handleSupervisor)
	mux.HandleFunc("POST /api/harness/resume", s.handleResume)
}

// ── read ──────────────────────────────────────────────────────────────────

// stateOrder puts problems first: blocked, then working, then idle.
var stateOrder = map[string]int{"blocked": 0, "working": 1, "triaging": 1, "idle": 2}

func sortLoops(loops []tmux.Loop) {
	sort.SliceStable(loops, func(i, j int) bool {
		a, b := stateOrder[loops[i].State], stateOrder[loops[j].State]
		if a != b {
			return a < b
		}
		return loops[i].Name < loops[j].Name
	})
}

func (s *Server) handleLoops(w http.ResponseWriter, r *http.Request) {
	loops, err := s.Tmux.Loops(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sortLoops(loops)
	s.mu.Lock()
	presets, last := s.Cfg.LoopPresets, s.Cfg.LastLoop
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"loops":   orEmpty(loops),
		"presets": orEmpty(presets),
		"last":    last,
		"repos":   orEmpty(s.repoChoices()),
	})
}

func orEmpty[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

// repoChoices is what the Start form offers: repos loops already ran in, then
// the repos panes are open in. Neither touches the filesystem - a read under
// ~/Desktop from a LaunchAgent macOS has not cleared blocks rather than fails
// (see panesByRepo) - so a pane's path is trimmed back to its repo by text.
func (s *Server) repoChoices() []config.HarnessRepo {
	s.mu.Lock()
	out := slices.Clone(s.Cfg.HarnessRepos)
	byRepo := s.paneRepos
	s.mu.Unlock()

	seen := map[string]bool{}
	for _, h := range out {
		seen[h.Slug] = true
	}
	tree := s.Workspace(context.Background(), s.Cfg.TreePoll())
	if tree == nil {
		return out
	}
	var more []config.HarnessRepo
	for slug, panes := range byRepo {
		if seen[slug] {
			continue
		}
		best := ""
		for _, id := range panes {
			if p := tree.Pane(id); p != nil {
				dir, _, _ := strings.Cut(p.Path, "/.claude/worktrees/")
				if best == "" || len(dir) < len(best) {
					best = dir
				}
			}
		}
		if best != "" {
			more = append(more, config.HarnessRepo{Path: best, Slug: slug})
		}
	}
	sort.Slice(more, func(i, j int) bool { return more[i].Slug < more[j].Slug })
	return append(out, more...)
}

// ── start, edit, stop ─────────────────────────────────────────────────────

// loopName is the session for one role in one repo: loop-claude-remux.
func loopName(who, repo string) string {
	proj := strings.ToLower(filepath.Base(repo))
	proj = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(proj, "-")
	return strings.Trim(tmux.LoopPrefix+who+"-"+strings.Trim(proj, "-"), "-")
}

func (s *Server) handleStartLoops(w http.ResponseWriter, r *http.Request) {
	var body config.LoopStart
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	body.Repo = filepath.Clean(expandHome(body.Repo))
	if !filepath.IsAbs(body.Repo) {
		writeErr(w, http.StatusBadRequest, "repo must be a full path")
		return
	}
	if body.Scope == "" {
		body.Scope = "full"
	}
	if !scopeRe.MatchString(body.Scope) {
		writeErr(w, http.StatusBadRequest, "scope is lowercase letters, digits and dashes")
		return
	}
	if body.InstrMode != "replace" {
		body.InstrMode = "add"
	}
	type plan struct{ name, role, agent string }
	var plans []plan
	for _, a := range body.Agents {
		if a != "claude" && a != "codex" {
			writeErr(w, http.StatusBadRequest, "agents are claude and codex")
			return
		}
		plans = append(plans, plan{loopName(a, body.Repo), "build", a})
	}
	if body.Review {
		plans = append(plans, plan{loopName("review", body.Repo), "review", "codex"})
	}
	if body.Supervise {
		if body.SuperviseAgent != "codex" {
			body.SuperviseAgent = "claude"
		}
		plans = append(plans, plan{loopName("supervise", body.Repo), "supervise", body.SuperviseAgent})
	}
	if len(plans) == 0 {
		writeErr(w, http.StatusBadRequest, "pick at least one agent")
		return
	}
	exe, err := loopExe()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	existing, _ := s.Tmux.Loops(r.Context())
	running := map[string]bool{}
	for _, l := range existing {
		running[l.Name] = true
	}
	var started, skipped []string
	for _, p := range plans {
		if !tmux.ValidLoopName(p.name) {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("cannot name a session for %q", body.Repo))
			return
		}
		if running[p.name] {
			skipped = append(skipped, p.name)
			continue
		}
		argv := []string{exe, "loop", "--role", p.role, "--agent", p.agent, "--repo", body.Repo,
			"--session", p.name, "--scope", body.Scope, "--instructions", body.Instructions,
			"--mode", body.InstrMode}
		if err := s.Tmux.NewLoopSession(r.Context(), p.name, body.Repo, argv); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.audit(r, "loop-start", p.name, body.Repo)
		started = append(started, p.name)
	}

	last := body
	s.mu.Lock()
	s.Cfg.LastLoop = &last
	s.mu.Unlock()
	if _, err := config.Update(func(c *config.Config) { c.LastLoop = &last }); err != nil {
		log.Printf("loops: could not save the last start: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"started": orEmpty(started), "skipped": orEmpty(skipped)})
}

func (s *Server) loopByName(ctx context.Context, name string) (*tmux.Loop, error) {
	loops, err := s.Tmux.Loops(ctx)
	if err != nil {
		return nil, err
	}
	for i := range loops {
		if loops[i].Name == name {
			return &loops[i], nil
		}
	}
	return nil, nil
}

// handleEditLoop changes a running loop's instructions. The run in progress
// keeps the old ones; the loop reads these before its next run.
func (s *Server) handleEditLoop(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Scope        *string `json:"scope"`
		Instructions *string `json:"instructions"`
		InstrMode    *string `json:"instrMode"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	l, err := s.loopByName(r.Context(), name)
	if err != nil || l == nil {
		writeErr(w, http.StatusNotFound, "no such loop")
		return
	}
	set := map[string]string{}
	if body.Scope != nil {
		if !scopeRe.MatchString(*body.Scope) {
			writeErr(w, http.StatusBadRequest, "scope is lowercase letters, digits and dashes")
			return
		}
		set["@loop_scope"] = *body.Scope
	}
	if body.Instructions != nil {
		set["@loop_instr"] = *body.Instructions
	}
	if body.InstrMode != nil {
		if *body.InstrMode != "add" && *body.InstrMode != "replace" {
			writeErr(w, http.StatusBadRequest, "instrMode is add or replace")
			return
		}
		set["@loop_mode"] = *body.InstrMode
	}
	for opt, v := range set {
		if err := s.Tmux.SetLoopOption(r.Context(), name, opt, v); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	s.audit(r, "loop-edit", name, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleStopLoop stops a loop now, or after the issue it is on.
//
// "after" is a flag the loop reads between runs. A loop with no run in
// progress has nothing to finish, so it is stopped now either way. "now" kills
// the session, and the claim label it held is taken off here as well as by the
// loop on its way out, so the issue is free for the next loop.
func (s *Server) handleStopLoop(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	l, err := s.loopByName(r.Context(), name)
	if err != nil || l == nil {
		writeErr(w, http.StatusNotFound, "no such loop")
		return
	}
	if r.URL.Query().Get("when") == "after" && (l.State == "working" || l.State == "triaging") {
		if err := s.Tmux.SetLoopOption(r.Context(), name, "@loop_stop", "after"); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.audit(r, "loop-stop", name, "after this issue")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "when": "after"})
		return
	}
	if err := s.Tmux.KillLoop(r.Context(), name); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if l.Issue > 0 && l.Slug != "" {
		claim := "wip:review"
		if l.Role != "review" {
			claim = "wip:" + cmpOr(l.Scope, "full")
		}
		gh := harness.GH{Slug: l.Slug}
		if err := gh.RemoveLabel(context.WithoutCancel(r.Context()), l.Issue, claim); err != nil {
			log.Printf("loops: could not release %s#%d: %v", l.Slug, l.Issue, err)
		}
	}
	s.audit(r, "loop-stop", name, "now")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "when": "now"})
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (s *Server) handlePutPresets(w http.ResponseWriter, r *http.Request) {
	var presets []config.LoopPreset
	if err := readJSON(r, &presets); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.Lock()
	s.Cfg.LoopPresets = presets
	s.mu.Unlock()
	if _, err := config.Update(func(c *config.Config) { c.LoopPresets = presets }); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(presets))
}

// ── the supervisor to talk to ─────────────────────────────────────────────

// supervisorSession is the one chat supervisor, for every repo.
const supervisorSession = "loop-supervisor"

// handleSupervisor opens the supervisor Kevin talks to: an interactive Claude
// session in its own loop-* session, briefed on the loops. The phone shows it
// as an ordinary pane, so there is no chat screen to build. It is started on
// first use and reused after that.
//
// ?agent=claude or codex picks who answers. An open session with that agent
// is reused; asking for the other one replaces it.
func (s *Server) handleSupervisor(w http.ResponseWriter, r *http.Request) {
	agent := r.URL.Query().Get("agent")
	if agent != "codex" {
		agent = "claude"
	}
	if l, _ := s.loopByName(r.Context(), supervisorSession); l != nil {
		if l.Agent == agent || l.Agent == "" && agent == "claude" {
			writeJSON(w, http.StatusOK, map[string]any{"pane": l.Pane})
			return
		}
		if err := s.Tmux.KillLoop(r.Context(), supervisorSession); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.mu.Lock()
	var repos []string
	for _, h := range s.Cfg.HarnessRepos {
		repos = append(repos, h.Slug+"  "+h.Path)
	}
	s.mu.Unlock()
	home, _ := os.UserHomeDir()
	brief := harness.Briefing(repos)
	argv := []string{"claude", "--dangerously-skip-permissions", "--append-system-prompt", brief}
	if agent == "codex" {
		// Codex has no system-prompt flag; developer_instructions is its
		// config key for the same thing, here as a TOML literal string.
		argv = codexChat(home, brief)
	}
	if err := s.Tmux.NewLoopSession(r.Context(), supervisorSession, home, argv); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Labelled so the Loops screen can tell it from the loops it supervises.
	s.Tmux.SetLoopOption(r.Context(), supervisorSession, "@loop_role", "chat")
	s.Tmux.SetLoopOption(r.Context(), supervisorSession, "@loop_agent", agent)
	s.Tmux.SetLoopOption(r.Context(), supervisorSession, "@loop_state", "idle")
	s.audit(r, "loop-start", supervisorSession, "")
	l, _ := s.loopByName(r.Context(), supervisorSession)
	if l == nil {
		writeErr(w, http.StatusInternalServerError, "the supervisor session exited at once")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pane": l.Pane})
}

// codexChat is the command for an interactive Codex supervisor. Two startup
// prompts would otherwise sit on its screen waiting for a key - "update
// available" and "trust this folder?" - so both are answered here, for this
// session only.
func codexChat(home, brief string) []string {
	tq := strings.Repeat("'", 3)
	return []string{"codex", "--dangerously-bypass-approvals-and-sandbox",
		"-c", "check_for_update_on_startup=false",
		"-c", fmt.Sprintf("projects={%q={trust_level=\"trusted\"}}", home),
		"-c", "developer_instructions=" + tq + strings.ReplaceAll(brief, tq, "' ' '") + tq}
}

// ── inbox ─────────────────────────────────────────────────────────────────

// InboxItem is one thing that needs Kevin: a stuck agent's question, or a
// company pull request now with the supervisor (just so he knows).
type InboxItem struct {
	Slug     string   `json:"slug"`
	Number   int      `json:"number"`
	Kind     string   `json:"kind"` // issue or pr
	Title    string   `json:"title"`
	URL      string   `json:"url"`
	// stuck waits on Kevin; checking is with the supervise loop, which will
	// pass it on only if it needs him; supervisor is a company PR with his
	// human supervisor.
	Why      string   `json:"why"`
	Question string   `json:"question,omitempty"`
	Options  []string `json:"options,omitempty"`
}

type ghThread struct {
	Number   int    `json:"number"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	IsDraft  bool   `json:"isDraft"`
	Head     string `json:"headRefName"`
	Comments []struct {
		Body string `json:"body"`
	} `json:"comments"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (s *Server) handleHarnessInbox(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	repos := slices.Clone(s.Cfg.HarnessRepos)
	s.mu.Unlock()
	supervised := map[string]bool{}
	if loops, err := s.Tmux.Loops(r.Context()); err == nil {
		for _, l := range loops {
			if l.Role == "supervise" {
				supervised[l.Slug] = true
			}
		}
	}

	var (
		mu    sync.Mutex
		items []InboxItem
		errs  []string
		wg    sync.WaitGroup
	)
	for _, repo := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := repoInbox(r.Context(), repo.Slug, supervised[repo.Slug])
			mu.Lock()
			defer mu.Unlock()
			items = append(items, got...)
			if err != nil {
				errs = append(errs, repo.Slug+": "+err.Error())
			}
		}()
	}
	wg.Wait()
	// Stuck first: those are the ones waiting on him.
	rank := map[string]int{"stuck": 0, "checking": 1, "supervisor": 2}
	sort.SliceStable(items, func(i, j int) bool { return rank[items[i].Why] < rank[items[j].Why] })
	writeJSON(w, http.StatusOK, map[string]any{"items": orEmpty(items), "errors": orEmpty(errs)})
}

func repoInbox(ctx context.Context, slug string, supervised bool) ([]InboxItem, error) {
	g := harness.GH{Slug: slug}
	var items []InboxItem
	for _, kind := range []string{"issue", "pr"} {
		var list []ghThread
		if err := g.JSON(ctx, &list, kind, "list", "--state", "open", "--label", "needs-human",
			"--json", "number,title,url,comments,labels"); err != nil {
			return items, err
		}
		for _, t := range list {
			q, opts := parseQuestion(t.Comments)
			why := "stuck"
			if supervised && !hasLabel(t.Labels, "escalated") {
				why = "checking"
			}
			items = append(items, InboxItem{Slug: slug, Number: t.Number, Kind: kind, Title: t.Title,
				URL: t.URL, Why: why, Question: q, Options: opts})
		}
	}
	// A harness pull request that is open, out of draft, and waiting on
	// nothing of ours is with the supervisor: in personal mode the loop
	// merges straight after marking it ready, so only company mode leaves one.
	var prs []ghThread
	if err := g.JSON(ctx, &prs, "pr", "list", "--state", "open",
		"--json", "number,title,url,isDraft,headRefName,labels"); err != nil {
		return items, err
	}
	for _, p := range prs {
		if p.IsDraft || !strings.HasPrefix(p.Head, "issue-") || hasLabel(p.Labels, "needs-review", "needs-human") {
			continue
		}
		items = append(items, InboxItem{Slug: slug, Number: p.Number, Kind: "pr", Title: p.Title,
			URL: p.URL, Why: "supervisor"})
	}
	return items, nil
}

func hasLabel(labels []struct {
	Name string `json:"name"`
}, names ...string) bool {
	for _, l := range labels {
		if slices.Contains(names, l.Name) || strings.HasPrefix(l.Name, "wip:") {
			return true
		}
	}
	return false
}

var optionRe = regexp.MustCompile(`^\s*\d+[.)]\s+(.+)$`)

// parseQuestion finds the newest comment carrying the question marker and
// splits it into the question and its numbered options.
func parseQuestion(comments []struct {
	Body string `json:"body"`
}) (string, []string) {
	for i := len(comments) - 1; i >= 0; i-- {
		body := comments[i].Body
		if !strings.Contains(body, harness.QuestionMarker) {
			continue
		}
		body = strings.Replace(body, harness.QuestionMarker, "", 1)
		var q []string
		var opts []string
		for _, line := range strings.Split(body, "\n") {
			if m := optionRe.FindStringSubmatch(line); m != nil {
				opts = append(opts, strings.TrimSpace(m[1]))
			} else if len(opts) == 0 {
				q = append(q, line)
			}
		}
		return strings.TrimSpace(strings.Join(q, "\n")), opts
	}
	return "", nil
}

// handleResume is "Send and resume": post Kevin's answer and put the item
// back in the queue - an issue to ready for the build loops, a pull request
// to needs-review for the review loop.
func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug   string `json:"slug"`
		Number int    `json:"number"`
		Kind   string `json:"kind"`
		Text   string `json:"text"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.knownSlug(body.Slug) || body.Number <= 0 {
		writeErr(w, http.StatusBadRequest, "not a harness repo")
		return
	}
	g := harness.GH{Slug: body.Slug}
	ctx := r.Context()
	if t := strings.TrimSpace(body.Text); t != "" {
		if err := g.Comment(ctx, body.Number, t); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	next := "ready"
	if body.Kind == "pr" {
		next = "needs-review"
	}
	if err := g.AddLabels(ctx, body.Number, next); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := g.RemoveLabel(ctx, body.Number, "needs-human"); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	g.RemoveLabel(ctx, body.Number, "escalated")
	s.audit(r, "resume", fmt.Sprintf("%s#%d", body.Slug, body.Number), truncate(body.Text, 400))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) knownSlug(slug string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.Cfg.HarnessRepos {
		if h.Slug == slug {
			return true
		}
	}
	return false
}

// ── watcher ───────────────────────────────────────────────────────────────

// WatchLoops reads the loop sessions every few seconds. It remembers each
// repo a loop has run in, so the Inbox keeps finding its stuck items after
// the loop stops, and turns each new loop event into one of the three pushes.
// store is nil under --local, for the same reason the other watchers are off
// there: a development server would double every notification.
func (s *Server) WatchLoops(ctx context.Context, store *push.Store) {
	seen := map[string]string{}
	first := true
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		loops, err := s.Tmux.Loops(ctx)
		if err == nil {
			for _, l := range loops {
				s.rememberRepo(l)
				if l.Event == "" || seen[l.Name] == l.Event {
					continue
				}
				// A restart is a baseline, not news.
				if !first && store != nil {
					if p, ok := loopPush(l); ok {
						store.Send(p)
					}
				}
				seen[l.Name] = l.Event
			}
			first = false
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) rememberRepo(l tmux.Loop) {
	if l.Slug == "" || l.Repo == "" {
		return
	}
	h := config.HarnessRepo{Path: l.Repo, Slug: l.Slug}
	s.mu.Lock()
	known := slices.Contains(s.Cfg.HarnessRepos, h)
	if !known {
		s.Cfg.HarnessRepos = append(s.Cfg.HarnessRepos, h)
	}
	s.mu.Unlock()
	if known {
		return
	}
	if _, err := config.Update(func(c *config.Config) {
		if !slices.Contains(c.HarnessRepos, h) {
			c.HarnessRepos = append(c.HarnessRepos, h)
		}
	}); err != nil {
		log.Printf("loops: could not save %s: %v", l.Slug, err)
	}
}

// loopPush turns "<nanos> <kind> <number> <title>" into a notification. The
// title is the issue's own, which is already public on GitHub; no agent
// output goes through the push servers.
func loopPush(l tmux.Loop) (push.Payload, bool) {
	f := strings.SplitN(l.Event, " ", 4)
	if len(f) < 3 {
		return push.Payload{}, false
	}
	kind, num := f[1], f[2]
	title := ""
	if len(f) == 4 {
		title = f[3]
	}
	body := fmt.Sprintf("%s #%s %s", l.Slug, num, title)
	p := push.Payload{Body: strings.TrimSpace(body), Tag: "loop-" + l.Slug + "-" + num}
	switch kind {
	case "stuck":
		p.Title = "An agent is stuck"
		p.Route = "#/loops/q/" + url.QueryEscape(l.Slug) + "/" + num
	case "merged":
		p.Title = "PR merged"
		p.Route = "#/loops"
	case "sent":
		p.Title = "PR sent to your supervisor"
		p.Route = "#/loops/inbox"
	default:
		return push.Payload{}, false
	}
	return p, true
}
