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
	writes   map[string]string // pane -> last value written
	order    []string          // panes written, in order, with repeats
	err      error
}

func newFake() *fakePanes {
	return &fakePanes{screens: map[string]string{}, writes: map[string]string{}}
}

func (f *fakePanes) Previews(_ context.Context, ids []string, _ int) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.captured = append(f.captured, append([]string(nil), ids...))
	out := map[string]string{}
	for _, id := range ids {
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

	s := NewState(f)
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
	s := NewState(f)
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

	s := NewState(f)
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
	s := NewState(f)
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

	s := NewState(f)
	p := &tmux.Pane{ID: "%1", Command: "codex"}
	s.OnTree(context.Background(), tree(100, p))

	s.mu.Lock()
	failed := s.failed["%1"]
	s.mu.Unlock()
	if !failed {
		t.Fatal("a failed write was not remembered")
	}
}
