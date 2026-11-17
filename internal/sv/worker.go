package sv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/viminizer/remux/internal/config"
	"github.com/viminizer/remux/internal/tmux"
)

// Worker is workers/<nn>-<role>/worker.json.
type Worker struct {
	ID       string    `json:"id"` // <task>/w<nn>
	Task     string    `json:"task"`
	N        int       `json:"n"`
	Role     string    `json:"role"`
	Agent    string    `json:"agent"`
	Model    string    `json:"model,omitempty"`
	Dir      string    `json:"dir"`
	ReadOnly bool      `json:"readOnly"`
	Why      string    `json:"why,omitempty"`
	State    string    `json:"state"` // running, done, failed, limited, stopped
	Exit     int       `json:"exit"`
	Session  string    `json:"session,omitempty"` // agent session, for resume
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended,omitzero"`
}

// Limits are the three numbers in Settings.
type Limits struct{ PerRepo, Total, Workers int }

// CurrentLimits reads them from the config file: the driver and the CLI are
// separate processes from the server, and Settings writes that file.
func CurrentLimits() Limits {
	c, _ := config.Load()
	if c == nil {
		c = config.Default()
	}
	return Limits{PerRepo: or(c.SVPerRepo, 1), Total: or(c.SVTotal, 3), Workers: or(c.SVWorkers, 8)}
}

func or(v, d int) int {
	if v > 0 {
		return v
	}
	return d
}

var roleRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)
var workerRe = regexp.MustCompile(`^(sv-[a-z0-9-]+)/w(\d{1,3})$`)

// ParseWorker splits "<task>/w03".
func ParseWorker(id string) (task string, n int, ok bool) {
	m := workerRe.FindStringSubmatch(id)
	if m == nil || !ValidID(m[1]) {
		return "", 0, false
	}
	n, _ = strconv.Atoi(m[2])
	return m[1], n, n > 0
}

func workerID(task string, n int) string { return fmt.Sprintf("%s/w%02d", task, n) }

// WorkerSession is the worker's tmux session: the task's, plus -w03.
func WorkerSession(task string, n int) string { return fmt.Sprintf("loop-%s-w%02d", task, n) }

// WorkerDir finds the worker's folder, workers/<nn>-<role>.
func WorkerDir(task string, n int) string {
	matches, _ := filepath.Glob(filepath.Join(Dir(task), "workers", fmt.Sprintf("%02d-*", n)))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// LoadWorker reads worker.json.
func LoadWorker(task string, n int) (*Worker, error) {
	dir := WorkerDir(task, n)
	if dir == "" {
		return nil, fmt.Errorf("no worker %s", workerID(task, n))
	}
	var w Worker
	if err := readJSON(filepath.Join(dir, "worker.json"), &w); err != nil {
		return nil, err
	}
	return &w, nil
}

func saveWorker(w *Worker) error {
	return writeJSON(filepath.Join(WorkerDir(w.Task, w.N), "worker.json"), w)
}

// Workers lists the task's workers in order.
func Workers(task string) []Worker {
	dirs, _ := filepath.Glob(filepath.Join(Dir(task), "workers", "*", "worker.json"))
	var out []Worker
	for _, p := range dirs {
		var w Worker
		if readJSON(p, &w) == nil {
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].N < out[j].N })
	return out
}

// Spec is one spawn request from a supervisor.
type Spec struct {
	Role, Agent, Model, Dir, Why, Instructions string
	ReadOnly                                   bool
}

// Spawn checks the request and starts the worker in its own tmux session.
//
// The checks are the supervisor's guardrails: the model ceiling, the shared
// worker pool, and that only a worker with write access writes, and only in
// a worktree of the task's repo, never the main checkout.
func Spawn(ctx context.Context, tm *tmux.Client, exe, task string, s Spec) (*Worker, error) {
	t, err := Load(task)
	if err != nil {
		return nil, err
	}
	if !Active(t.Phase) {
		return nil, fmt.Errorf("task is %s; workers start only while it runs", t.Phase)
	}
	if !roleRe.MatchString(s.Role) {
		return nil, errors.New("role is a short lowercase name, such as builder or security-reviewer")
	}
	if strings.TrimSpace(s.Instructions) == "" {
		return nil, errors.New("the worker needs instructions")
	}
	if err := CheckModel(s.Agent, s.Model); err != nil {
		return nil, err
	}
	if s.Dir == "" {
		s.Dir = t.Repo
	}
	s.Dir = filepath.Clean(s.Dir)
	if err := checkDir(ctx, t.Repo, s.Dir, s.ReadOnly); err != nil {
		return nil, err
	}

	unlock, err := lock(filepath.Join(Root(), "pool.lock"))
	if err != nil {
		return nil, err
	}
	defer unlock()
	if n, max := RunningWorkers(ctx, tm), CurrentLimits().Workers; n >= max {
		return nil, fmt.Errorf("the worker pool is full (%d of %d running). Wait for one of yours, then spawn again", n, max)
	}
	n := len(Workers(task)) + 1
	w := &Worker{ID: workerID(task, n), Task: task, N: n, Role: s.Role, Agent: s.Agent, Model: s.Model,
		Dir: s.Dir, ReadOnly: s.ReadOnly, Why: s.Why, State: "running", Started: time.Now().UTC()}
	dir := filepath.Join(Dir(task), "workers", fmt.Sprintf("%02d-%s", n, s.Role))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "instructions.md"), []byte(s.Instructions), 0o600); err != nil {
		return nil, err
	}
	if err := saveWorker(w); err != nil {
		return nil, err
	}
	if err := startWorker(ctx, tm, exe, w, false); err != nil {
		w.State = "failed"
		saveWorker(w)
		return nil, err
	}
	model := s.Model
	if model == "" {
		model = "default"
	}
	access := "write"
	if s.ReadOnly {
		access = "read-only"
	}
	AddEvent(task, Event{Kind: "spawn", Worker: w.ID, Text: fmt.Sprintf("%s · %s %s · %s · %s", s.Role, s.Agent, model, access, s.Why)})
	return w, nil
}

// checkDir: read-only workers may look anywhere in the repo; a worker that
// writes gets a linked worktree of it, never the main checkout.
func checkDir(ctx context.Context, repo, dir string, readOnly bool) error {
	out, err := exec.CommandContext(ctx, "git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return fmt.Errorf("cannot list the worktrees of %s: %v", repo, err)
	}
	var trees []string
	for _, line := range strings.Split(string(out), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			trees = append(trees, filepath.Clean(p))
		}
	}
	if len(trees) == 0 {
		return fmt.Errorf("%s is not a git repo", repo)
	}
	main := trees[0]
	for i, t := range trees {
		if dir == t || strings.HasPrefix(dir, t+"/") {
			if i == 0 && !readOnly && (dir == main || !strings.HasPrefix(dir, main+"/.claude/worktrees/")) {
				return fmt.Errorf("a worker that writes needs its own worktree, not the main checkout. Create one with git worktree add under %s/.claude/worktrees", main)
			}
			return nil
		}
	}
	return fmt.Errorf("%s is not inside the task's repo or one of its worktrees", dir)
}

func startWorker(ctx context.Context, tm *tmux.Client, exe string, w *Worker, resume bool) error {
	name := WorkerSession(w.Task, w.N)
	tm.KillLoop(ctx, name) // a finished session of the same worker, before a resume
	// Flags before the id: Go's flag parsing stops at the first argument
	// that is not a flag, so a trailing --resume would be ignored.
	argv := []string{exe, "sv", "worker"}
	if resume {
		argv = append(argv, "--resume")
	}
	argv = append(argv, w.ID)
	if err := tm.NewLoopSession(ctx, name, w.Dir, argv); err != nil {
		return err
	}
	// Without a role, the server's loop watcher would take it for an empty
	// shell left by a crash and remove it.
	tm.SetLoopOption(ctx, name, "@loop_role", "sv-worker")
	return nil
}

// RunningWorkers counts the pool: workers marked running whose session is
// still alive, across every task. A running worker with no session died with
// the Mac or the server; it is marked failed here, so the pool frees itself.
func RunningWorkers(ctx context.Context, tm *tmux.Client) int {
	n := 0
	for _, t := range List() {
		for _, w := range Workers(t.ID) {
			if w.State != "running" {
				continue
			}
			if _, err := tm.LoopOption(ctx, WorkerSession(w.Task, w.N), "@loop_role"); err != nil {
				w.State, w.Ended = "failed", time.Now().UTC()
				saveWorker(&w)
				AddEvent(w.Task, Event{Kind: "worker", Worker: w.ID, Text: "failed: its session is gone"})
				continue
			}
			n++
		}
	}
	return n
}

// Resume gives a finished worker a new message in its own session: the
// checkpoint way to send instructions. It keeps its context.
func Resume(ctx context.Context, tm *tmux.Client, exe, task string, n int, text string) error {
	w, err := LoadWorker(task, n)
	if err != nil {
		return err
	}
	if w.State == "running" {
		return errors.New("the worker is still running; use tell for a message it reads on its next step")
	}
	if w.Session == "" {
		return errors.New("the worker has no session to resume; spawn a fresh one")
	}
	unlock, err := lock(filepath.Join(Root(), "pool.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	if n, max := RunningWorkers(ctx, tm), CurrentLimits().Workers; n >= max {
		return fmt.Errorf("the worker pool is full (%d of %d running)", n, max)
	}
	if err := os.WriteFile(filepath.Join(WorkerDir(task, n), "resume.md"), []byte(text), 0o600); err != nil {
		return err
	}
	w.State, w.Ended = "running", time.Time{}
	if err := saveWorker(w); err != nil {
		return err
	}
	AddEvent(task, Event{Kind: "worker", Worker: w.ID, Text: "resumed: " + short(text, 200)})
	return startWorker(ctx, tm, exe, w, true)
}

// StopWorker ends a worker's session.
func StopWorker(ctx context.Context, tm *tmux.Client, task string, n int) error {
	w, err := LoadWorker(task, n)
	if err != nil {
		return err
	}
	tm.KillLoop(ctx, WorkerSession(task, n))
	if w.State == "running" {
		w.State, w.Ended = "stopped", time.Now().UTC()
		AddEvent(task, Event{Kind: "worker", Worker: w.ID, Text: "stopped"})
		return saveWorker(w)
	}
	return nil
}

// FailWorker marks a worker that could not run as failed, so it does not
// look busy forever.
func FailWorker(id string, why error) {
	task, n, ok := ParseWorker(id)
	if !ok {
		return
	}
	w, err := LoadWorker(task, n)
	if err != nil || w.State != "running" {
		return
	}
	w.State, w.Ended = "failed", time.Now().UTC()
	saveWorker(w)
	AddEvent(task, Event{Kind: "worker", Worker: w.ID, Text: "failed to start: " + short(why.Error(), 200)})
}

// RunWorker is what runs inside a worker's session: one agent run, then the
// report, then exit.
func RunWorker(ctx context.Context, exe, id string, resume bool) error {
	task, n, ok := ParseWorker(id)
	if !ok {
		return fmt.Errorf("%q is not a worker id", id)
	}
	w, err := LoadWorker(task, n)
	if err != nil {
		return err
	}
	dir := WorkerDir(task, n)
	body, err := os.ReadFile(filepath.Join(dir, "instructions.md"))
	if err != nil {
		return err
	}
	prompt := string(body)
	session := ""
	if resume {
		msg, _ := os.ReadFile(filepath.Join(dir, "resume.md"))
		prompt, session = "Message from your supervisor:\n\n"+string(msg), w.Session
	}
	prompt += "\n\n" + workerFooter(w)
	r := &Run{Agent: w.Agent, Model: w.Model, Effort: "medium", Dir: w.Dir, Prompt: prompt, Session: session,
		ReadOnly: w.ReadOnly, Stream: filepath.Join(dir, "stream.jsonl"),
		Hook: "'" + exe + "' sv inbox --hook", Env: []string{"REMUX_SV_TASK=" + task, "REMUX_SV_WORKER=" + w.ID}}
	res := r.Exec(ctx)

	w, _ = LoadWorker(task, n) // a stop may have changed it meanwhile
	if w.State == "stopped" {
		return nil
	}
	w.Session, w.Exit, w.Ended = res.Session, res.Code, time.Now().UTC()
	switch {
	case !res.Limit.IsZero():
		w.State = "limited"
	case res.Code != 0:
		w.State = "failed"
	default:
		w.State = "done"
	}
	// The report is the worker's final message. A file the worker writes
	// itself would not work for every worker: a read-only Codex sandbox
	// cannot write it.
	report := orStr(strings.TrimSpace(res.Final), "(no final message)")
	os.WriteFile(filepath.Join(dir, "report.md"), []byte(report+"\n"), 0o600)
	note := w.State
	if w.State == "limited" {
		note += ", back at " + res.Limit.Format("15:04")
	}
	AddEvent(task, Event{Kind: "worker", Worker: w.ID, Text: note})
	return saveWorker(w)
}

func workerFooter(w *Worker) string {
	access := "You may change files only inside " + w.Dir + "."
	if w.ReadOnly {
		access = "You are read-only: do not change, create or delete any file, branch or PR. Only read and report."
	}
	return fmt.Sprintf(`---
You are worker %s, role %s, working for a supervisor agent. Work only on the task above.
%s
Your supervisor may send you messages while you work; they arrive as extra context after a tool call. Follow them.
Follow the repo's RULEBOOK.md and AGENTS.md if it has them.
Your final message is your report to the supervisor, in plain short sentences. Start it with a one-line
summary, then:
1. What you did, with file paths, commits, branches and PRs.
2. What you checked and how: the exact commands you ran and their results.
3. What is not done, what failed, and open problems. Say it honestly; never claim a check you did not run.`, w.ID, w.Role, access)
}

// ── mailboxes ─────────────────────────────────────────────────────────────

// inboxPath is the supervisor's mailbox, or a worker's.
func inboxPath(task string, n int) string {
	if n == 0 {
		return filepath.Join(Dir(task), "inbox.jsonl")
	}
	return filepath.Join(WorkerDir(task, n), "inbox.jsonl")
}

// Send puts a message in a mailbox.
func Send(task string, n int, from, text string) error {
	if n > 0 && WorkerDir(task, n) == "" {
		return fmt.Errorf("no worker %s", workerID(task, n))
	}
	return appendJSON(inboxPath(task, n), Message{At: time.Now().UTC(), From: from, Text: text})
}

// Take empties a mailbox and returns what was in it. The rename makes a
// message sent at the same moment land in the next take, not get lost.
func Take(task string, n int) []Message {
	p := inboxPath(task, n)
	taken := p + ".taking"
	if os.Rename(p, taken) != nil {
		return nil
	}
	defer os.Remove(taken)
	return tailJSON[Message](taken, 0)
}

// FormatInbox is the messages as text for an agent.
func FormatInbox(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "Message from %s (%s): %s\n", m.From, m.At.Local().Format("15:04"), m.Text)
	}
	return b.String()
}
