package api

import (
	"context"
	"sync"
	"time"

	"github.com/viminizer/remux/internal/agent"
	"github.com/viminizer/remux/internal/tmux"
)

// workspace is the one pass over every pane, shared by everything that wants
// the tree.
//
// internal/github/poller.go states the lesson this applies: one poller, not one
// per connection. The GitHub side learned it, the pane side had not. Each
// WebSocket used to run its own tree tick - its own Tree() call, its own
// ActivityGate, its own captures - so a second phone doubled the sweep and the
// gate's whole saving was per connection rather than per workspace. On top of
// that WatchPanes was already reading the same tree on the same interval for
// the namer, so one phone meant two sweeps and two phones meant three.
//
// Now there is one. refresh does the work, everything else reads what it
// published. Two phones cost what one costs, and nobody watching costs the same
// as somebody watching, because the pass was already running for the namer.
type workspace struct {
	// one holds refresh, so N callers arriving together produce one sweep
	// rather than N. It is separate from pub for a reason: a caller must be
	// able to read the published tree while a refresh is in flight.
	one sync.Mutex

	// gate and status are the carry-forward. A pane whose window has had no
	// output since the last sweep still has the verdict we gave it then, so
	// it is not captured again. Shared now, which is the point: the verdict
	// survives a phone disconnecting, and a second phone rides along.
	gate   *tmux.ActivityGate
	status map[string]string

	pub  sync.RWMutex
	tree *tmux.Tree
	at   time.Time
	// classified records whether the published tree carries verdicts. It can
	// be false: a sweep with nobody watching skips the captures, and a phone
	// arriving afterwards must not be handed that tree with a drawer full of
	// blank status dots.
	classified bool
}

func newWorkspace() *workspace {
	return &workspace{gate: tmux.NewActivityGate(), status: map[string]string{}}
}

// fresh returns the published tree when it is younger than maxAge, and carries
// verdicts if the caller needs them.
func (w *workspace) fresh(maxAge time.Duration, needStatus bool) *tmux.Tree {
	w.pub.RLock()
	defer w.pub.RUnlock()
	if w.tree == nil || time.Since(w.at) >= maxAge {
		return nil
	}
	if needStatus && !w.classified {
		return nil
	}
	return w.tree
}

// Workspace returns the shared tree, refreshing it if what is published is
// older than maxAge - or if a phone is watching and what is published has no
// verdicts on it.
//
// The refresh is here rather than only on the timer so the first frame after a
// connect is immediate. Waiting for the next shared tick would have shown the
// phone an empty drawer for up to a whole poll interval, and a server with no
// timer running at all - which is every test that dials the socket - would have
// shown it nothing ever.
func (s *Server) Workspace(ctx context.Context, maxAge time.Duration) *tmux.Tree {
	w := s.ws
	// Read once: a socket opening between the two checks below would send us
	// down one path with the other's answer.
	watching := s.Watching()
	if tree := w.fresh(maxAge, watching); tree != nil {
		return tree
	}

	w.one.Lock()
	defer w.one.Unlock()
	// Somebody else may have refreshed it while we waited for the lock, and
	// that answer is as good as the one we were about to fetch.
	if tree := w.fresh(maxAge, watching); tree != nil {
		return tree
	}
	return s.refreshWorkspace(ctx, watching)
}

// refreshWorkspace reads the workspace, classifies every pane, and publishes
// the result. The caller must hold w.one, which is what makes the gate and the
// carry-forward map below safe without a lock of their own.
//
// Deliberately cheap enough to sit on a connection's path: a tree read plus the
// captures the gate did not rule out. The pane/repo matching is not here and
// must not be - it touches the filesystem, and a directory macOS has not
// granted access to blocks rather than fails. That stays on WatchPanes, where
// one background goroutine is the only thing that can be left waiting.
func (s *Server) refreshWorkspace(ctx context.Context, classify bool) *tmux.Tree {
	w := s.ws
	tree, err := s.Tmux.Tree(ctx)
	if err != nil {
		return nil
	}

	panes := tree.Panes()
	alive := make(map[string]bool, len(panes))
	for _, pn := range panes {
		alive[pn.ID] = true
	}

	// The captures are the expensive half and only a phone reads what they
	// produce: pane.Status is the drawer's status dot and nothing else
	// consumes it - the namer classifies for itself, off its own gate and a
	// much narrower set of panes. So with nobody watching this does the tree
	// read the namer needs and skips the rest, which is what the per-connection
	// poller used to achieve by simply not existing.
	//
	// It matters because agent.IsShell's complement is wide. vim, htop, less,
	// tail and `npm run dev` are none of them, and a dev server writes to its
	// window on every tick, so it passes the gate every time - the most
	// expensive kind of pane to sweep for a verdict nobody is going to read.
	if classify {
		live := make([]*tmux.Pane, 0, len(panes))
		var unknown []*tmux.Pane
		for _, pn := range panes {
			if agent.IsShell(pn.Command) {
				continue
			}
			live = append(live, pn)
			// The gate is keyed by window, so a pane that appears in a window
			// that has been quiet is not offered - and with no verdict cached
			// it would publish a pane with no status at all, until something
			// happened to write to that window. always is how the gate takes
			// "I have never had an answer for this one".
			if _, had := w.status[pn.ID]; !had {
				unknown = append(unknown, pn)
			}
		}
		for _, sc := range w.gate.CaptureWith(ctx, s.Tmux, live, unknown) {
			// A capture that failed says nothing about the pane. Classifying
			// the empty string gives Unknown, and storing that would mark a
			// working pane with a shrug that the gate then refuses to
			// reconsider until its window next moves. Left absent instead, it
			// is retried on the next sweep through unknown above.
			if !sc.Captured {
				continue
			}
			w.status[sc.Pane.ID] = string(agent.Classify(sc.Pane.Command, sc.Pane.Title, sc.Screen))
		}
	}

	for _, pn := range panes {
		// A shell is classified from its command alone - Classify
		// short-circuits before it looks at a screen - so it never needed one.
		if agent.IsShell(pn.Command) {
			pn.Status = string(agent.Classify(pn.Command, pn.Title, ""))
			w.status[pn.ID] = pn.Status
			continue
		}
		pn.Status = w.status[pn.ID]
	}
	for id := range w.status {
		if !alive[id] {
			delete(w.status, id)
		}
	}

	// Repos come from the map WatchPanes keeps, so this is a lookup and never
	// a probe. It is at worst one tick behind, which is what a directory that
	// has just been cd'd into costs - and the alternative is resolving paths
	// on a path that must not block.
	s.mu.Lock()
	byPane := make(map[string]string, len(panes))
	for repo, ids := range s.paneRepos {
		for _, id := range ids {
			byPane[id] = repo
		}
	}
	s.mu.Unlock()
	for _, pn := range panes {
		pn.Repo = byPane[pn.ID]
	}

	// Published last, and never mutated afterwards: readers marshal this very
	// tree, so anything writing to it after this point would be a race.
	w.pub.Lock()
	w.tree, w.at, w.classified = tree, time.Now(), classify
	w.pub.Unlock()
	return tree
}
