package titler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/viminizer/remux/internal/agent"
	"github.com/viminizer/remux/internal/tmux"
)

// fakePanes records what the pass asked for and what it wrote, which is what
// every test here is actually about: the writes it does not make.
type fakePanes struct {
	mu       sync.Mutex
	screens  map[string]string
	captured [][]string        // one entry per Previews call
	writes   map[string]string // pane -> last state written
	tasks    map[string]string // pane -> last task written
	projects map[string]string // pane -> last project written
	order    []string          // panes whose state was written, in order
	err      error
	// uncapturable panes are left out of the Previews result, which is how a
	// failed capture-pane actually reaches a caller.
	uncapturable map[string]bool
}

func newFake() *fakePanes {
	return &fakePanes{
		screens:      map[string]string{},
		writes:       map[string]string{},
		tasks:        map[string]string{},
		projects:     map[string]string{},
		uncapturable: map[string]bool{},
	}
}

// newPass is New plus the one stub every test here needs.
//
// away() reads this Mac's HID idle timer. Left real, the whole naming suite
// passes or fails on whether anyone touched the keyboard in the last fifteen
// minutes - and for a while it passed only because away() was broken and
// always answered false, so the six tests that call settle() were asserting
// against a run that had never started.
func newPass(tm Panes) *Pass {
	p := New(tm)
	p.Away = func() bool { return false }
	// Every test but the two about startup is describing a workspace that has
	// been running a while, where the first tree is long past. Leaving this
	// false would make each of them a test of Pass.settle instead of the thing
	// it is named after - the named pane would simply be held back.
	p.settled = true
	return p
}

// newColdPass is a process that has just started: nothing seen, nothing asked.
func newColdPass(tm Panes) *Pass {
	p := newPass(tm)
	p.settled = false
	return p
}

func (f *fakePanes) Previews(_ context.Context, ids []string, _ int) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.captured = append(f.captured, append([]string(nil), ids...))
	out := map[string]string{}
	for _, id := range ids {
		// Omitted, exactly as the real Previews omits a pane whose
		// capture-pane failed. That is the difference this fake has to be able
		// to express: a missing entry and an empty one are not the same thing.
		if f.uncapturable[id] {
			continue
		}
		out[id] = f.screens[id]
	}
	return out
}

func (f *fakePanes) SetPaneState(_ context.Context, paneID, state string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.writes[paneID] = state
	f.order = append(f.order, paneID)
	return nil
}

func (f *fakePanes) SetPaneProject(_ context.Context, paneID, project string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.projects[paneID] = project
	return nil
}

func (f *fakePanes) SetPaneTask(_ context.Context, paneID, task string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.tasks[paneID] = task
	return nil
}

// lastCapture returns the ids the most recent pass captured.
func (f *fakePanes) lastCapture() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.captured) == 0 {
		return nil
	}
	return f.captured[len(f.captured)-1]
}

// Screens taken from the classifier's own fixtures, cut down to the marker
// each verdict turns on.
const (
	waitingScreen = "❯ 1. Yes\n  2. No\n"
	busyScreen    = "✳ Thinking… (12s · esc to interrupt)\n"
	idleScreen    = "› \n  ? for shortcuts\n"
)

// tree builds a one-window workspace. activity is shared by the window, which
// is how tmux reports it and what the gate keys off.
func tree(activity int64, panes ...*tmux.Pane) *tmux.Tree {
	for i, p := range panes {
		p.WindowID = "@1"
		p.Activity = activity
		if p.ID == "" {
			p.ID = string(rune('%')) + string(rune('1'+i))
		}
	}
	return &tmux.Tree{Sessions: []*tmux.Session{{
		ID: "$1", Name: "saas",
		Windows: []*tmux.Window{{ID: "@1", Panes: panes}},
	}}}
}

func TestGlyphPerVerdict(t *testing.T) {
	for _, c := range []struct {
		status agent.Status
		want   string
	}{
		{agent.Waiting, "!"},
		{agent.Busy, "✳"},
		{agent.Idle, "✓"},
		// Both mean "remux cannot say", and an empty option is the only
		// honest rendering of that.
		{agent.Shell, ""},
		{agent.Unknown, ""},
	} {
		if got := glyph(c.status); got != c.want {
			t.Errorf("glyph(%q) = %q, want %q", c.status, got, c.want)
		}
	}
}

func TestWritesTheVerdictOfEachPane(t *testing.T) {
	f := newFake()
	f.screens["%1"] = waitingScreen
	f.screens["%2"] = busyScreen
	f.screens["%3"] = idleScreen

	s := newPass(f)
	s.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "codex"},
		&tmux.Pane{ID: "%2", Command: "codex"},
		&tmux.Pane{ID: "%3", Command: "2.1.263"},
	))

	for id, want := range map[string]string{"%1": "!", "%2": "✳", "%3": "✓"} {
		if got := f.writes[id]; got != want {
			t.Errorf("pane %s state = %q, want %q", id, got, want)
		}
	}
}

// A shell is never given a state, and a pane that was an agent and is now back
// at a prompt has its old glyph cleared. A "waiting" mark left on a finished
// pane is the failure that makes the whole status line untrustworthy.
func TestShellIsNeverStatedAndIsCleared(t *testing.T) {
	f := newFake()
	s := newPass(f)
	s.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "zsh"},
		&tmux.Pane{ID: "%2", Command: "zsh", RemuxState: "!"},
	))

	if _, wrote := f.writes["%1"]; wrote {
		t.Errorf("a shell with no state was written to: %q", f.writes["%1"])
	}
	if got, wrote := f.writes["%2"]; !wrote || got != "" {
		t.Errorf("a shell carrying a stale glyph was not cleared: %q", got)
	}
	if ids := f.lastCapture(); len(ids) != 0 {
		t.Errorf("captured %v; a shell is classified from its command alone", ids)
	}
}

// The value already on the pane is the comparison, so an agent that stays busy
// costs no tmux writes. #32 is the standing lesson about writes on a timer.
func TestUnchangedVerdictIsNotRewritten(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen

	s := newPass(f)
	s.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "codex", RemuxState: "✳"},
	))

	if len(f.order) != 0 {
		t.Errorf("wrote %v; the pane already said the same thing", f.order)
	}
}

// With no window activity anywhere, a pass captures nothing. On this laptop
// that is what keeps a workspace of twenty agent panes free overnight, and it
// is the gate the model half will rely on too.
func TestQuietWorkspaceCapturesNothing(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen

	// Activity well in the past: the gate re-captures anything that moved in
	// the last two seconds regardless, to cover its one-second resolution.
	old := time.Now().Unix() - 60
	s := newPass(f)
	pane := func() *tmux.Pane { return &tmux.Pane{ID: "%1", Command: "codex", RemuxState: "✳"} }

	s.OnTree(context.Background(), tree(old, pane())) // first pass: nothing seen yet
	before := len(f.captured)

	s.OnTree(context.Background(), tree(old, pane()))
	if len(f.captured) != before {
		t.Errorf("captured %v on an unchanged window; the gate did not hold",
			f.captured[len(f.captured)-1])
	}
}

// A pane tmux refuses to write is reported once, not once per pass, so a
// permanently unwritable pane cannot fill the log.
func TestWriteFailureIsLoggedOnce(t *testing.T) {
	f := newFake()
	f.screens["%1"] = waitingScreen
	f.err = errors.New("no such pane")

	s := newPass(f)
	p := &tmux.Pane{ID: "%1", Command: "codex"}
	s.OnTree(context.Background(), tree(100, p))

	s.mu.Lock()
	failed := s.failed["%1"]
	s.mu.Unlock()
	if !failed {
		t.Fatal("a failed write was not remembered")
	}
}

// A failed capture is not a blank screen. Before this, tmux.Previews omitting
// a pane it could not read left screens[id] == "", which classifies as Unknown
// and writes "" - so one flaky exec stripped the "!" off a pane genuinely
// blocked on an answer, and it stayed stripped until some later pass both
// captured and reclassified it.
func TestFailedCaptureLeavesTheGlyphAlone(t *testing.T) {
	f := newFake()
	f.uncapturable["%1"] = true

	s := newPass(f)
	s.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "codex", RemuxState: "!"},
	))

	if got, wrote := f.writes["%1"]; wrote {
		t.Errorf("a failed capture cleared the state to %q", got)
	}
}

// Only agents are classified and named. vim, htop, npm run dev and a tail -f
// are none of them and are not shells either, and the old !IsShell test put
// every one of them into a paid model call - then wrote the model's guess over
// a real pane_title. A dev server writing to its window also passes the
// activity gate on every tick, so it was the most expensive pane to get wrong.
func TestNonAgentPanesAreNeitherStatedNorNamed(t *testing.T) {
	f := newFake()
	// vim in insert mode matches idleRe, so the old code marked it done.
	f.screens["%1"] = "-- INSERT --\n"
	f.screens["%2"] = busyScreen

	r := &fakeRunner{label: "fake", out: "1: should not be asked\n"}
	p := newPass(f)
	p.Chain = []Runner{r}

	p.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "vim"},
		&tmux.Pane{ID: "%2", Command: "node"},
	))
	settle(t, p)

	if r.count() != 0 {
		t.Errorf("asked a model about %d non-agent panes", r.count())
	}
	for _, id := range []string{"%1", "%2"} {
		if got, wrote := f.writes[id]; wrote {
			t.Errorf("pane %s got the state %q; it is not an agent", id, got)
		}
		if got, wrote := f.tasks[id]; wrote {
			t.Errorf("pane %s got the name %q; it is not an agent", id, got)
		}
	}
}

// A pane that stops being an agent still carries both options, and they have
// to be cleared or the status line lies about a pane that finished.
func TestAPaneThatStopsBeingAnAgentIsCleared(t *testing.T) {
	f := newFake()
	s := newPass(f)
	s.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "vim", RemuxState: "✳", RemuxTask: "venue filter pagination"},
	))

	if got, wrote := f.writes["%1"]; !wrote || got != "" {
		t.Errorf("state = %q, want it cleared", got)
	}
	if got, wrote := f.tasks["%1"]; !wrote || got != "" {
		t.Errorf("task = %q, want it cleared", got)
	}
}
