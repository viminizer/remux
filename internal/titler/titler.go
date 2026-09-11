// Package titler names agent panes on the laptop itself.
//
// The problem it exists for is scale, not information: with twenty-odd agent
// panes running, four of them titled "shortlist" because Codex names itself
// after the folder and three more saying only "Claude Code", finding the pane
// you care about means opening four of them.
//
// Two things get written, on the same rule for every pane and every agent:
//
//   - @remux_state, one glyph, from the classifier that already runs. Free.
//   - @remux_task, three to five words, from a cheap model reading the tail of
//     the screen. Costs a fraction of a cent, and only when the work changes.
//
// The whole design rests on reading the rendered screen rather than parsing
// any agent's output or transcripts. That is what makes one code path cover
// Codex, Claude Code, opencode and whatever gets installed next year, and it
// is why the twenty-one agents already running get named without restarting
// anything.
package titler

import (
	"context"
	"log"
	"sync"
	"sync/atomic"

	"github.com/viminizer/remux/internal/agent"
	"github.com/viminizer/remux/internal/tmux"
)

// Panes is the slice of tmux this needs: read some screens, write two options.
// *tmux.Client satisfies it. It is an interface only so the pass can be tested
// without a tmux server, since most of what these tests assert is what does
// *not* get written.
type Panes interface {
	Previews(ctx context.Context, paneIDs []string, n int) map[string]string
	SetPaneState(ctx context.Context, paneID, state string) error
	SetPaneTask(ctx context.Context, paneID, task string) error
}

// Pass names every agent pane in the workspace, once per tree poll.
//
// It does not own a loop. remux already has one unconditional pass over the
// tree (api.Server.WatchPanes), and #32 is the standing lesson about what a
// second timer over the same panes costs - so this hangs off that pass the way
// the GitHub push watcher hangs off the GitHub poller.
//
// One capture serves both halves. The state needs the screen and so does the
// title, and capturing twice would put back exactly the sweep #32 removed.
type Pass struct {
	Tmux Panes
	// Chain is tried in order until one returns something usable. Empty
	// disables the naming half entirely and the state half keeps working,
	// which is why New leaves it empty: the one part of remux that spends
	// money is opted into at the call site, and no test can start a model by
	// forgetting to switch it off.
	Chain []Runner
	// Enabled gates the model half only. Nil means on.
	Enabled func() bool

	gate *tmux.ActivityGate

	// naming is held for the length of a model call, which runs off the tick
	// so a slow CLI cannot stall the pane/repo refresher sharing this loop.
	naming atomic.Bool
	asked  *cooldown

	mu     sync.Mutex
	failed map[string]bool // panes whose last write errored, so it is logged once
}

func New(tm Panes) *Pass {
	return &Pass{
		Tmux:   tm,
		gate:   tmux.NewActivityGate(),
		asked:  newCooldown(),
		failed: map[string]bool{},
	}
}

// OnTree reclassifies the panes that can have changed, writes the states that
// are now wrong, and starts a naming run for the panes that are due one.
//
// Only the first part is synchronous. Writing a glyph is a tmux call and costs
// under a millisecond; asking a model takes seconds, and the caller's loop
// also keeps the pane/repo mapping fresh.
func (p *Pass) OnTree(ctx context.Context, tree *tmux.Tree) {
	panes := tree.Panes()

	agents := make([]*tmux.Pane, 0, len(panes))
	for _, pane := range panes {
		if agent.IsShell(pane.Command) {
			// A pane that was an agent and is now back at a shell prompt
			// still carries the last glyph and the last task. Clearing both
			// is the only way the status line does not lie about a pane that
			// finished.
			p.writeState(ctx, pane, "")
			p.writeTask(ctx, pane, "")
			continue
		}
		agents = append(agents, pane)
	}

	stale := map[string]bool{}
	for _, id := range p.gate.Changed(agents) {
		stale[id] = true
	}
	ids := make([]string, 0, len(stale))
	for _, pane := range agents {
		if stale[pane.ID] {
			ids = append(ids, pane.ID)
		}
	}
	if len(ids) == 0 {
		return
	}

	screens := p.Tmux.Previews(ctx, ids, tmux.PreviewLines)

	var due []job
	for _, pane := range agents {
		if !stale[pane.ID] {
			continue
		}
		screen := screens[pane.ID]
		p.writeState(ctx, pane, glyph(agent.Classify(pane.Command, pane.Title, screen)))
		if p.dueForNaming(pane) {
			due = append(due, job{
				ID: pane.ID, Dir: pane.Path, Task: pane.RemuxTask, Screen: screen,
			})
		}
	}
	p.startNaming(ctx, due)
}

// report records the outcome of one option write, complaining at most once per
// pane so a pane that cannot be written does not fill the log every pass.
func (p *Pass) report(_ context.Context, paneID string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		if !p.failed[paneID] {
			p.failed[paneID] = true
			log.Printf("pane %s: %v", paneID, err)
		}
		return
	}
	delete(p.failed, paneID)
}
