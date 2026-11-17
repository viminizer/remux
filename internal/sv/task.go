// Package sv is the supervisor: an agent that plans a task Kevin gives it,
// splits it, hands the parts to worker agents, checks their work and merges
// it. It never writes code itself.
//
// Everything a supervisor knows lives in one folder per task under
// ~/.remux/supervisor/tasks, so a restart of remux, of the driver or of the
// Mac loses nothing: the files are the source of truth, and the registry is
// a scan of the folders.
package sv

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Phases of a task, in order. Blocked is a pause inside running.
const (
	Discussing = "discussing" // Kevin and the supervisor talk
	Planning   = "planning"   // the supervisor writes the plan
	Review     = "review"     // the plan waits for Kevin's approval
	Queued     = "queued"     // approved, waiting for a free supervisor slot
	Running    = "running"
	Blocked    = "blocked" // needs Kevin: Question says what
	Done       = "done"    // merged, branches and worktrees cleaned up, retro written
	Stopped    = "stopped" // Kevin stopped it
)

// Active phases hold a supervisor slot.
func Active(phase string) bool { return phase == Running || phase == Blocked }

// Finished phases never change again.
func Finished(phase string) bool { return phase == Done || phase == Stopped }

// Task is status.json: what the app shows and what other supervisors read.
type Task struct {
	ID       string    `json:"id"`
	Repo     string    `json:"repo"`  // main checkout
	Agent    string    `json:"agent"` // the supervisor's own agent: claude or codex
	Goal     string    `json:"goal"`  // the brief's first line
	Phase    string    `json:"phase"`
	Summary  string    `json:"summary,omitempty"`  // one line: where it is
	Question string    `json:"question,omitempty"` // what it needs from Kevin while blocked
	Steps    []Step    `json:"steps,omitempty"`
	Changes  []string  `json:"changes,omitempty"`  // plan changes and why
	Evidence []string  `json:"evidence,omitempty"` // tests, merged PRs
	Areas    []string  `json:"areas,omitempty"`    // parts of the code it touches
	Session  string    `json:"session,omitempty"`  // the supervisor agent's session id
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Ended    time.Time `json:"ended,omitzero"`
}

// Step is one step of the approved plan.
type Step struct {
	Title string `json:"title"`
	State string `json:"state"` // next, running, done, blocked
	Note  string `json:"note,omitempty"`
}

// Event is one line of events.jsonl.
type Event struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"` // phase, spawn, worker, decision, change, failure, merge, read, msg, note
	Text   string    `json:"text"`
	Worker string    `json:"worker,omitempty"`
}

// Message is one line of chat.jsonl: the discussion, and Kevin's answers.
type Message struct {
	At   time.Time `json:"at"`
	From string    `json:"from"` // kevin or supervisor
	Text string    `json:"text"`
}

// Root is ~/.remux/supervisor. A variable so tests can move it.
var Root = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".remux", "supervisor")
}

var idRe = regexp.MustCompile(`^sv-[a-z0-9][a-z0-9-]{0,52}$`)

// ValidID reports whether id names a task. It is also what keeps a path
// built from it inside Root.
func ValidID(id string) bool { return idRe.MatchString(id) && !strings.HasSuffix(id, "-") }

// Dir is the task's folder.
func Dir(id string) string { return filepath.Join(Root(), "tasks", id) }

// SessionName is the task's tmux session. It starts with loop- so it falls
// under the rules the harness sessions already follow.
func SessionName(id string) string { return "loop-" + id }

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string, max int) string {
	s = strings.Trim(nonWord.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > max {
		s = strings.TrimRight(s[:max], "-")
	}
	return s
}

// filler words that make a poor task name.
var filler = map[string]bool{"a": true, "an": true, "the": true, "to": true, "and": true, "of": true,
	"for": true, "in": true, "on": true, "with": true, "please": true, "can": true, "you": true, "we": true, "i": true}

// shortName is the first three real words of the brief: "Refactor the
// payments module" is "refactor-payments-module".
func shortName(brief string) string {
	var words []string
	for _, w := range strings.Fields(strings.ToLower(brief)) {
		w = slug(w, 20)
		if w == "" || filler[w] {
			continue
		}
		words = append(words, w)
		if len(words) == 3 {
			break
		}
	}
	if n := slug(strings.Join(words, "-"), 24); n != "" {
		return n
	}
	return "task"
}

// Create makes the task folder and its first files. The ID is
// sv-<repo>-<short-name>, with a number added when taken.
func Create(repo, agent, brief string) (*Task, error) {
	brief = strings.TrimSpace(brief)
	if brief == "" {
		return nil, errors.New("the brief is empty")
	}
	base := "sv-" + slug(filepath.Base(repo), 20) + "-" + shortName(brief)
	if err := os.MkdirAll(filepath.Join(Root(), "tasks"), 0o700); err != nil {
		return nil, err
	}
	for n := 1; n < 100; n++ {
		id := base
		if n > 1 {
			id = fmt.Sprintf("%s-%d", base, n)
		}
		if !ValidID(id) {
			return nil, fmt.Errorf("cannot name a task for %q", repo)
		}
		// Mkdir is the claim: two creates cannot get the same folder.
		if err := os.Mkdir(Dir(id), 0o700); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		goal, _, _ := strings.Cut(brief, "\n")
		t := &Task{ID: id, Repo: repo, Agent: agent, Goal: strings.TrimSpace(goal), Phase: Discussing, Created: now, Updated: now}
		if err := os.WriteFile(filepath.Join(Dir(id), "brief.md"), []byte(brief+"\n"), 0o600); err != nil {
			return nil, err
		}
		if err := save(t); err != nil {
			return nil, err
		}
		AddEvent(id, Event{Kind: "phase", Text: "created: " + t.Goal})
		return t, nil
	}
	return nil, errors.New("too many tasks with this name")
}

// Load reads status.json.
func Load(id string) (*Task, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("%q is not a task id", id)
	}
	var t Task
	if err := readJSON(filepath.Join(Dir(id), "status.json"), &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// Update changes status.json under a lock: the driver, the supervisor agent
// and the server all write it.
func Update(id string, fn func(*Task) error) (*Task, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("%q is not a task id", id)
	}
	unlock, err := lock(filepath.Join(Dir(id), "status.lock"))
	if err != nil {
		return nil, err
	}
	defer unlock()
	t, err := Load(id)
	if err != nil {
		return nil, err
	}
	was := t.Phase
	if err := fn(t); err != nil {
		return nil, err
	}
	if t.Phase != was {
		if Finished(t.Phase) {
			t.Ended = time.Now().UTC()
		}
		AddEvent(id, Event{Kind: "phase", Text: was + " → " + t.Phase})
	}
	t.Updated = time.Now().UTC()
	return t, save(t)
}

func save(t *Task) error { return writeJSON(filepath.Join(Dir(t.ID), "status.json"), t) }

// List is every task, newest first. A folder without a readable
// status.json is skipped, not an error: it is a create in progress.
func List() []Task {
	entries, _ := os.ReadDir(filepath.Join(Root(), "tasks"))
	var out []Task
	for _, e := range entries {
		if t, err := Load(e.Name()); err == nil {
			out = append(out, *t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// AddEvent appends to events.jsonl. Losing one line is better than failing
// the work it describes, so errors are only returned for callers that care.
func AddEvent(id string, e Event) error {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	return appendJSON(filepath.Join(Dir(id), "events.jsonl"), e)
}

// Events reads the last n events, or all when n is 0.
func Events(id string, n int) []Event {
	return tailJSON[Event](filepath.Join(Dir(id), "events.jsonl"), n)
}

// AddMessage appends to the chat.
func AddMessage(id, from, text string) error {
	return appendJSON(filepath.Join(Dir(id), "chat.jsonl"), Message{At: time.Now().UTC(), From: from, Text: text})
}

// Chat is the whole discussion.
func Chat(id string) []Message { return tailJSON[Message](filepath.Join(Dir(id), "chat.jsonl"), 0) }

// Unanswered is what Kevin said after the supervisor last spoke.
func Unanswered(id string) []Message {
	chat := Chat(id)
	i := len(chat)
	for i > 0 && chat[i-1].From == "kevin" {
		i--
	}
	return chat[i:]
}

// ReadFile reads one of the task's own files by name, for the app and for
// other supervisors. Only plain names: no path can leave the folder.
func ReadFile(id, name string) string {
	if !ValidID(id) || filepath.Base(name) != name {
		return ""
	}
	b, _ := os.ReadFile(filepath.Join(Dir(id), name))
	return string(b)
}

// ── files ─────────────────────────────────────────────────────────────────

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// writeJSON replaces a file in one rename, so a reader never sees half of it.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func appendJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

func tailJSON[T any](path string, n int) []T {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		var v T
		if json.Unmarshal(sc.Bytes(), &v) == nil {
			out = append(out, v)
		}
	}
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// lock takes an exclusive flock on path, waiting for it. The lock goes with
// the process, so a crash cannot leave it held.
func lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
