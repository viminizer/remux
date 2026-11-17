package api

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/viminizer/remux/internal/push"
	"github.com/viminizer/remux/internal/sv"
)

// The supervisor, server side. Like the loops, remux stays thin: it creates
// the task folder, starts the driver in its own tmux session, and writes
// Kevin's chat messages and decisions into the folder. The driver and the
// supervisor agent do the rest.

func (s *Server) svRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sv", s.handleSVList)
	mux.HandleFunc("POST /api/sv", s.handleSVCreate)
	mux.HandleFunc("GET /api/sv/{id}", s.handleSVTask)
	mux.HandleFunc("POST /api/sv/{id}/chat", s.handleSVChat)
	mux.HandleFunc("POST /api/sv/{id}/plan", s.handleSVPhase(sv.Discussing, sv.Planning))
	mux.HandleFunc("POST /api/sv/{id}/approve", s.handleSVPhase(sv.Review, sv.Queued))
	mux.HandleFunc("POST /api/sv/{id}/back", s.handleSVBack)
	mux.HandleFunc("POST /api/sv/{id}/stop", s.handleSVStop)
}

func (s *Server) handleSVList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"tasks":  orEmpty(sv.List()),
		"repos":  orEmpty(s.repoChoices()),
		"limits": sv.CurrentLimits(),
	})
}

func (s *Server) handleSVCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Repo  string `json:"repo"`
		Brief string `json:"brief"`
		Agent string `json:"agent"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	body.Repo = filepath.Clean(expandHome(body.Repo))
	if !filepath.IsAbs(body.Repo) {
		writeErr(w, http.StatusBadRequest, "repo must be a full path")
		return
	}
	if fi, err := os.Stat(filepath.Join(body.Repo, ".git")); err != nil || fi == nil {
		writeErr(w, http.StatusBadRequest, body.Repo+" is not a git repo")
		return
	}
	if body.Agent != "codex" {
		body.Agent = "claude"
	}
	if err := sv.CheckModel(body.Agent, ""); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	t, err := sv.Create(body.Repo, body.Agent, body.Brief)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// The brief is the first thing Kevin says; the supervisor answers it.
	sv.AddMessage(t.ID, "kevin", strings.TrimSpace(body.Brief))
	if err := s.startDriver(r.Context(), t.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "sv-create", t.ID, body.Repo)
	writeJSON(w, http.StatusOK, t)
}

// startDriver runs `remux sv run <id>` in the task's own detached session.
func (s *Server) startDriver(ctx context.Context, id string) error {
	exe, err := loopExe()
	if err != nil {
		return err
	}
	t, err := sv.Load(id)
	if err != nil {
		return err
	}
	name := sv.SessionName(id)
	if err := s.Tmux.NewLoopSession(ctx, name, t.Repo, []string{exe, "sv", "run", id}); err != nil {
		return err
	}
	// A role, or the loop watcher takes it for an empty restored shell.
	return s.Tmux.SetLoopOption(ctx, name, "@loop_role", "sv")
}

func (s *Server) handleSVTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := sv.Load(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such task")
		return
	}
	// The panes of its sessions, so the phone can open one to watch.
	panes := map[string]string{}
	if loops, err := s.Tmux.Loops(r.Context()); err == nil {
		for _, l := range loops {
			if l.Name == sv.SessionName(id) || strings.HasPrefix(l.Name, sv.SessionName(id)+"-w") {
				panes[l.Name] = l.Pane
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"panes":   panes,
		"task":    t,
		"chat":    orEmpty(sv.Chat(id)),
		"plan":    sv.ReadFile(id, "plan.md"),
		"retro":   sv.ReadFile(id, "retro.md"),
		"events":  orEmpty(sv.Events(id, 300)),
		"workers": orEmpty(sv.Workers(id)),
	})
}

// handleSVChat is Kevin speaking: in the discussion, as an answer while the
// task is blocked, or - while it runs - a note the supervisor reads on its
// next step.
func (s *Server) handleSVChat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Text) == "" {
		writeErr(w, http.StatusBadRequest, "say something")
		return
	}
	t, err := sv.Load(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such task")
		return
	}
	if sv.Finished(t.Phase) {
		writeErr(w, http.StatusConflict, "the task is "+t.Phase)
		return
	}
	text := strings.TrimSpace(body.Text)
	sv.AddMessage(id, "kevin", text)
	switch t.Phase {
	case sv.Running, sv.Queued, sv.Planning:
		// Not a turn of its own: the supervisor reads it at its next step.
		sv.Send(id, 0, "Kevin", text)
		sv.AddMessage(id, "supervisor", "(Passed to the supervisor. It reads it on its next step.)")
	}
	s.audit(r, "sv-chat", id, truncate(text, 400))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSVPhase(from, to string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		_, err := sv.Update(id, func(t *sv.Task) error {
			if t.Phase != from {
				return errPhase(t.Phase)
			}
			t.Phase = to
			return nil
		})
		if err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		s.audit(r, "sv-"+to, id, "")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// handleSVBack sends the plan back with Kevin's comment.
func (s *Server) handleSVBack(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Text) == "" {
		writeErr(w, http.StatusBadRequest, "say what to change")
		return
	}
	_, err := sv.Update(id, func(t *sv.Task) error {
		if t.Phase != sv.Review {
			return errPhase(t.Phase)
		}
		t.Phase = sv.Planning
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	sv.AddMessage(id, "kevin", strings.TrimSpace(body.Text))
	s.audit(r, "sv-back", id, truncate(body.Text, 400))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSVStop stops a task: its workers and its driver end, the folder
// stays. Worktrees and branches are left as they are, for Kevin to judge.
func (s *Server) handleSVStop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := sv.Update(id, func(t *sv.Task) error {
		if sv.Finished(t.Phase) {
			return errPhase(t.Phase)
		}
		t.Phase = sv.Stopped
		return nil
	}); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	for _, wk := range sv.Workers(id) {
		sv.StopWorker(r.Context(), s.Tmux, id, wk.N)
	}
	s.Tmux.KillLoop(r.Context(), sv.SessionName(id))
	s.audit(r, "sv-stop", id, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type errPhase string

func (e errPhase) Error() string { return "the task is " + string(e) }

// StreamKeep is how long a finished task keeps its raw worker streams.
const StreamKeep = 30 * 24 * time.Hour

// WatchSV runs every 10 seconds. It pushes the three things worth a
// notification - plan ready, blocked, done - and, when restart is set,
// starts the driver again for a task whose session is gone (a reboot, a
// crash) while the task is not finished. It also deletes raw streams of
// tasks that ended more than 30 days ago; the plan, events, reports and
// retro stay.
//
// Only the task id goes through the push servers, never agent output.
func (s *Server) WatchSV(ctx context.Context, store *push.Store, restart bool) {
	seen := map[string]string{}
	gone := map[string]bool{}
	first := true
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		for _, task := range sv.List() {
			if was, ok := seen[task.ID]; !first && store != nil && (!ok || was != task.Phase) {
				if p, ok := svPush(task); ok {
					store.Send(p)
				}
			}
			seen[task.ID] = task.Phase
			if sv.Finished(task.Phase) {
				if !task.Ended.IsZero() && time.Since(task.Ended) > StreamKeep {
					dropStreams(task.ID)
				}
				continue
			}
			if !restart {
				continue
			}
			// Two looks in a row without a session, so a driver that is just
			// starting is not started twice.
			if _, err := s.Tmux.LoopOption(ctx, sv.SessionName(task.ID), "@loop_role"); err != nil {
				if gone[task.ID] {
					log.Printf("sv: restarting the driver of %s", task.ID)
					if err := s.startDriver(ctx, task.ID); err != nil {
						log.Printf("sv: %v", err)
					}
					delete(gone, task.ID)
				} else {
					gone[task.ID] = true
				}
			} else {
				delete(gone, task.ID)
			}
		}
		first = false
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func svPush(t sv.Task) (push.Payload, bool) {
	p := push.Payload{Body: t.ID, Tag: "sv-" + t.ID, Route: "#/sv/" + t.ID}
	switch t.Phase {
	case sv.Review:
		p.Title = "Plan ready for your approval"
	case sv.Blocked:
		p.Title = "The supervisor needs you"
	case sv.Done:
		p.Title = "Task done"
	default:
		return push.Payload{}, false
	}
	return p, true
}

func dropStreams(id string) {
	paths, _ := filepath.Glob(filepath.Join(sv.Dir(id), "workers", "*", "stream.jsonl"))
	paths = append(paths, filepath.Join(sv.Dir(id), "supervisor.jsonl"))
	for _, p := range paths {
		os.Remove(p)
	}
}
