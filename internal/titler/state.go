// Package titler names agent panes on the laptop itself.
//
// The problem it exists for is scale, not information: with twenty-odd agent
// panes running, four of them titled "shortlist" because Codex names itself
// after the folder, finding the pane you care about means opening four of
// them. remux already knows what each pane is doing - agent.Classify drives
// the phone's badges and its push notifications - and this writes that verdict
// back where the laptop can see it.
//
// This file is the half that needs no model: one glyph per pane, from the
// classifier that already runs. The task title is the other half.
package titler

import (
	"context"
	"log"
	"sync"

	"github.com/viminizer/remux/internal/agent"
	"github.com/viminizer/remux/internal/tmux"
)

// The glyphs, chosen to be readable in a status line at a glance rather than
// descriptive. Across twenty panes the expensive question is not what each
// agent is doing, it is which two or three are blocked on an answer - so "!"
// is the one that has to stand out.
const (
	glyphWaiting = "!"
	glyphBusy    = "✳"
	glyphDone    = "✓"
)

// glyph maps a verdict onto what gets written, with "" meaning "clear the
// option". A shell is not an agent and never carries a state; Unknown means
// the classifier did not recognise the screen, and inventing a glyph there
// would be a confident guess about a pane we cannot read.
func glyph(s agent.Status) string {
	switch s {
	case agent.Waiting:
		return glyphWaiting
	case agent.Busy:
		return glyphBusy
	case agent.Idle:
		return glyphDone
	default:
		return ""
	}
}

// Panes is the slice of tmux this needs: read some screens, write one option.
// *tmux.Client satisfies it. It is an interface only so the pass can be tested
// without a tmux server, since the whole point of the tests here is what does
// *not* get written.
type Panes interface {
	Previews(ctx context.Context, paneIDs []string, n int) map[string]string
	SetPaneState(ctx context.Context, paneID, state string) error
}

// State keeps @remux_state up to date for every pane in the workspace.
//
// It does not own a loop. remux already has one unconditional pass over the
// tree (Server.WatchPanes), and #32 is the standing lesson about what a second
// timer over the same panes costs - so this hangs off that pass the same way
// the GitHub push watcher hangs off the GitHub poller.
type State struct {
	Tmux Panes

	gate *tmux.ActivityGate

	mu     sync.Mutex
	failed map[string]bool // panes whose last write errored, so it is logged once
}

func NewState(tm Panes) *State {
	return &State{Tmux: tm, gate: tmux.NewActivityGate(), failed: map[string]bool{}}
}

// OnTree reclassifies the panes that can have changed and writes the ones
// whose glyph is now wrong.
//
// Two filters keep a quiet workspace free. The activity gate drops every pane
// in a window tmux has not written to since the last pass, so nothing is
// captured overnight. And the write is compared against the value already on
// the pane - read from the tree, which is the truth on disk rather than a
// cache that can drift - so a busy agent that stays busy costs no tmux writes
// at all. #32 again: a write on a timer is the thing to avoid.
func (s *State) OnTree(ctx context.Context, tree *tmux.Tree) {
	panes := tree.Panes()

	agents := make([]*tmux.Pane, 0, len(panes))
	for _, p := range panes {
		if agent.IsShell(p.Command) {
			// A pane that was an agent and is now back at a shell prompt
			// still carries the last glyph. Clearing it is the only way the
			// status line does not lie about a pane that finished.
			s.write(ctx, p, "")
			continue
		}
		agents = append(agents, p)
	}

	stale := map[string]bool{}
	for _, id := range s.gate.Changed(agents) {
		stale[id] = true
	}
	ids := make([]string, 0, len(stale))
	for _, p := range agents {
		if stale[p.ID] {
			ids = append(ids, p.ID)
		}
	}
	if len(ids) == 0 {
		return
	}

	screens := s.Tmux.Previews(ctx, ids, tmux.PreviewLines)
	for _, p := range agents {
		if !stale[p.ID] {
			continue
		}
		s.write(ctx, p, glyph(agent.Classify(p.Command, p.Title, screens[p.ID])))
	}
}

// write sets the option only when the value actually changes, and complains at
// most once per pane so a pane that cannot be written does not fill the log
// every pass.
func (s *State) write(ctx context.Context, p *tmux.Pane, want string) {
	if p.RemuxState == want {
		return
	}
	err := s.Tmux.SetPaneState(ctx, p.ID, want)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		if !s.failed[p.ID] {
			s.failed[p.ID] = true
			log.Printf("pane state %s: %v", p.ID, err)
		}
		return
	}
	delete(s.failed, p.ID)
}
