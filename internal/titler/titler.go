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
	"time"

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
	SetPaneProject(ctx context.Context, paneID, project string) error
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
	// Away reports whether Kevin is at the laptop. Nil means ask the machine.
	//
	// It is a field for the same reason Enabled is: the real implementation
	// reads this Mac's HID idle timer, so without a seam every naming test
	// passes or fails on whether anyone happened to touch the keyboard in the
	// last fifteen minutes - and the suite quietly depended on HIDAway being
	// broken.
	Away func() bool
	// AwayFor is how long one Away answer is reused. Zero means the default.
	//
	// A seam for the same reason Away is. The cache is what bounds ioreg to
	// one fork per ten seconds overnight, and it also means a transition takes
	// up to that long to notice - fine when Kevin picks up the phone and the
	// names appear a few seconds later, impossible to test against without
	// sleeping ten seconds per case.
	AwayFor time.Duration

	gate *tmux.ActivityGate

	// naming is held for the length of a model call, which runs off the tick
	// so a slow CLI cannot stall the pane/repo refresher sharing this loop.
	naming atomic.Bool
	asked  *cooldown

	mu        sync.Mutex
	failed    map[string]bool // panes whose last write errored, so it is logged once
	backoff   time.Duration   // current wait after a chain that failed at every tier
	retryAt   time.Time       // nothing is asked before this
	wasAway   bool            // last away answer
	awayAt    time.Time       // when it was asked
	couldName bool            // whether naming was possible on the previous pass
}

// namingJustOpened reports the pass on which naming became possible - the
// switch flipped on, or Kevin came back to something that is reading.
//
// It asks the away check only when the switch is on, so a workspace with
// naming turned off never forks ioreg at all. The first pass answers false
// whatever the state: the gate is empty then, so every pane is already offered
// and there is nothing to make stale.
func (p *Pass) namingJustOpened(enabled bool) bool {
	can := enabled && len(p.Chain) > 0 && !p.isAway()

	p.mu.Lock()
	defer p.mu.Unlock()
	opened := can && !p.couldName
	p.couldName = can
	return opened
}

// awayFor is how long one Away answer is reused.
//
// Nothing marks the cooldown while nobody is at the desk - a pane is marked
// when its question is asked, and no question is asked - so due stays
// non-empty and, uncached, every tick would fork ioreg for the whole night at
// the tree poll interval. The answer moves on a fifteen-minute scale, so ten
// seconds of reuse costs nothing and bounds it to one exec per ten seconds.
const awayFor = 10 * time.Second

func (p *Pass) isAway() bool {
	reuse := p.AwayFor
	if reuse == 0 {
		reuse = awayFor
	}

	p.mu.Lock()
	if !p.awayAt.IsZero() && time.Since(p.awayAt) < reuse {
		v := p.wasAway
		p.mu.Unlock()
		return v
	}
	p.mu.Unlock()

	// Outside the lock: the default Away execs ioreg with a 3s timeout, and report() on
	// the background naming goroutine wants this same mutex to record a write.
	ask := p.Away
	if ask == nil {
		ask = HIDAway
	}
	v := ask()

	p.mu.Lock()
	p.wasAway, p.awayAt = v, time.Now()
	p.mu.Unlock()
	return v
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

	live := make(map[string]bool, len(panes))
	agents := make([]*tmux.Pane, 0, len(panes))
	for _, pane := range panes {
		live[pane.ID] = true
		// agent.IsAgent, not !IsShell. The two are not complements: vim, htop,
		// npm run dev, less and tail are none of them, and the loose test put
		// every one of those into a paid model call and then wrote the model's
		// guess over a real pane_title. A dev server writing to its window
		// also passes the activity gate on every tick, so it was the most
		// expensive kind of pane to get wrong.
		if !agent.IsAgent(pane.Command) {
			// A pane that was an agent and is now a shell - or vim, or
			// anything else - still carries the last glyph and the last task.
			// Clearing both is the only way the status line does not lie
			// about a pane that finished. Neither write happens when the
			// options are already unset, so an ordinary shell costs nothing.
			p.writeState(ctx, pane, "")
			p.writeTask(ctx, pane, "")
			p.writeProject(ctx, pane, "")
			continue
		}
		agents = append(agents, pane)
	}
	p.forget(live)
	short := p.writeProjects(ctx, agents)

	naming := p.Enabled == nil || p.Enabled()

	// Naming has just become possible, so every pane is made stale once.
	//
	// The gate offers a pane when its window moves and not again until it
	// moves next. If naming was impossible at that moment - the switch off, or
	// nobody looking - the pane is skipped, and a pane that has since gone
	// quiet is never offered again. That loses precisely the set worth naming:
	// the agents that finished hours ago and are sitting there waiting.
	//
	// Measured on the real workspace, with the presence gate live: of
	// twenty-one agent panes, the three still writing output got named and the
	// other eighteen - every one of them idle for hours - stayed blank
	// permanently. Forget is what the ws poller already calls on "resume", for
	// the same reason: a consumer whose cached verdicts are worthless.
	if p.namingJustOpened(naming) {
		p.gate.Forget()
	}

	// An agent pane with no name yet is offered whatever the gate says.
	//
	// The gate answers "can this screen have changed", which is the right
	// question for a glyph and the wrong one for a name a pane does not have.
	// One unlucky answer - the model declining, the screen fallback finding an
	// empty composer - and an idle pane is blank for good, because nothing
	// will write to its window again to bring it back.
	//
	// Retrying is close to free: these panes join a batch that was happening
	// anyway, the cooldown still holds each one to once per askEvery, and the
	// presence gate means none of it runs while nobody is looking. They stop
	// costing anything the moment they get a name.
	// len(Chain) too, not just the switch: with no model configured there is
	// nothing that could name these, so reading them would be a capture spent
	// on an answer nobody can give.
	unnamed := make([]*tmux.Pane, 0, len(agents))
	if naming && len(p.Chain) > 0 {
		for _, pane := range agents {
			if pane.RemuxTask == "" {
				unnamed = append(unnamed, pane)
			}
		}
	}

	var due []job
	for _, s := range p.gate.CaptureWith(ctx, p.Tmux, agents, unnamed) {
		// A capture that failed is not a blank screen. Classifying the empty
		// string gives Unknown, and writing that back would strip the "!" off
		// a pane genuinely blocked on an answer until some later pass both
		// captured and reclassified it.
		if s.Captured {
			p.writeState(ctx, s.Pane, glyph(agent.Classify(s.Pane.Command, s.Pane.Title, s.Screen)))
		}

		switch {
		case !naming:
			// Off means off. Without this a name written before the switch was
			// flipped sits on the pane forever, hiding the live pane_title
			// that is free and always current.
			p.writeTask(ctx, s.Pane, "")
		case s.Captured && p.dueForNaming(s.Pane):
			due = append(due, job{
				ID: s.Pane.ID, Dir: s.Pane.Path, Task: s.Pane.RemuxTask,
				// From this pass's map, not the pane's option: the option is
				// what was on the pane when the tree was read, so a pane that
				// has just changed directory would be told the old project.
				Project: short[s.Pane.ID],
				Screen:  s.Screen,
			})
		}
	}
	p.startNaming(ctx, due)
}

// forget drops every pane that is no longer in the tree.
//
// Both maps are keyed by pane id in a LaunchAgent that runs for weeks, so
// without this each pane Kevin opens and closes leaves a permanent entry. The
// second reason is the one push.Watcher already learned: tmux recycles pane
// ids, and an inherited cooldown means a brand new pane goes unnamed for up to
// askEvery.
func (p *Pass) forget(live map[string]bool) {
	p.asked.keep(live)

	p.mu.Lock()
	defer p.mu.Unlock()
	for id := range p.failed {
		if !live[id] {
			delete(p.failed, id)
		}
	}
}

// writeProjects keeps @remux_project current for every agent pane.
//
// It runs outside the activity gate and costs nothing to do so: the repo comes
// from the tree, which was read anyway, and a write only happens when the
// value actually changes - which is when a pane changes directory, close to
// never. It is also the half of the name that must not wait on the gate or on
// anyone looking, because it needs no model and no screen.
//
// The shortening needs the whole workspace at once, since what a name can drop
// depends on what its siblings are called.
func (p *Pass) writeProjects(ctx context.Context, agents []*tmux.Pane) map[string]string {
	// The matcher's answer where there is one, the paths where there is not.
	//
	// Under a LaunchAgent that macOS has not granted Full Disk Access there is
	// never one: every .git/config read under ~/Desktop is refused, so every
	// pane comes back with no repo and the prefix disappears entirely. The
	// fallback is approximate - a pane in shortlist/server is called "server"
	// where the matcher would say "shortlist" - and an approximate prefix beats
	// no prefix while the permission is still ungranted.
	//
	// Per pane, not per workspace, so one unreadable directory does not throw
	// away the repos that did resolve.
	paths := make(map[string]string, len(agents))
	for _, pane := range agents {
		if pane.Repo == "" {
			paths[pane.ID] = pane.Path
		}
	}
	guessed := Fallback(paths)

	keys := make(map[string]string, len(agents)) // pane -> what Short is keyed by
	uniq := map[string]bool{}
	var all []string
	for _, pane := range agents {
		key := pane.Repo
		if key == "" {
			// The directory itself. Short reads the last path segment out of
			// it exactly as it reads the name out of "owner/name".
			key = guessed[pane.ID]
		}
		if key == "" {
			continue
		}
		keys[pane.ID] = key
		if !uniq[key] {
			uniq[key] = true
			all = append(all, key)
		}
	}
	short := Short(all)

	// Keyed by pane on the way out, because that is what the caller has: it
	// needs the project for a pane it is about to ask a model about, and
	// whether that name came from the matcher or from a path is not its
	// business.
	byPane := make(map[string]string, len(agents))
	for _, pane := range agents {
		// A pane with neither a repo nor a directory gets nothing rather than
		// a guess. Being somewhere is not the same as being in a project.
		name := short[keys[pane.ID]]
		byPane[pane.ID] = name
		p.writeProject(ctx, pane, name)
	}
	return byPane
}

func (p *Pass) writeProject(ctx context.Context, pane *tmux.Pane, want string) {
	if pane.RemuxProject == want {
		return
	}
	p.report(ctx, pane.ID, p.Tmux.SetPaneProject(ctx, pane.ID, want))
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
