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
}

func newWorkspace() *workspace {
	return &workspace{gate: tmux.NewActivityGate(), status: map[string]string{}}
}

// published returns the last tree and how old it is.
func (w *workspace) published() (*tmux.Tree, time.Duration) {
	w.pub.RLock()
	defer w.pub.RUnlock()
	if w.tree == nil {
		return nil, 0
	}
	return w.tree, time.Since(w.at)
}

// Workspace returns the shared tree, refreshing it if what is published is
// older than maxAge.
//
// The refresh is here rather than only on the timer so the first frame after a
// connect is immediate. Waiting for the next shared tick would have shown the
// phone an empty drawer for up to a whole poll interval, and a server with no
// timer running at all - which is every test that dials the socket - would have
// shown it nothing ever.
func (s *Server) Workspace(ctx context.Context, maxAge time.Duration) *tmux.Tree {
	w := s.ws
	if tree, age := w.published(); tree != nil && age < maxAge {
		return tree
	}

	w.one.Lock()
	defer w.one.Unlock()
	// Somebody else may have refreshed it while we waited for the lock, and
	// that answer is as good as the one we were about to fetch.
	if tree, age := w.published(); tree != nil && age < maxAge {
		return tree
	}
	return s.refreshWorkspace(ctx)
}

// refreshWorkspace reads the workspace, classifies every pane, and publishes
// the result. The caller must hold w.one.
//
// Deliberately cheap enough to sit on a connection's path: a tree read plus the
// captures the gate did not rule out. The pane/repo matching is not here and
// must not be - it touches the filesystem, and a directory macOS has not
// granted access to blocks rather than fails. That stays on WatchPanes, where
// one background goroutine is the only thing that can be left waiting.
func (s *Server) refreshWorkspace(ctx context.Context) *tmux.Tree {
	w := s.ws
	tree, err := s.Tmux.Tree(ctx)
	if err != nil {
		return nil
	}

	panes := tree.Panes()
	live := make([]*tmux.Pane, 0, len(panes))
	for _, pn := range panes {
		if !agent.IsShell(pn.Command) {
			live = append(live, pn)
		}
	}
	stale := map[string]bool{}
	for _, id := range w.gate.Changed(live) {
		stale[id] = true
	}
	ids := make([]string, 0, len(stale))
	for _, pn := range live {
		if stale[pn.ID] {
			ids = append(ids, pn.ID)
		}
	}
	screens := s.Tmux.Previews(ctx, ids, tmux.PreviewLines)

	// A shell is classified from its command alone, so it never needs a
	// screen; everything else either has a fresh one or keeps the verdict it
	// had.
	for _, pn := range panes {
		if agent.IsShell(pn.Command) || stale[pn.ID] {
			pn.Status = string(agent.Classify(pn.Command, pn.Title, screens[pn.ID]))
			w.status[pn.ID] = pn.Status
			continue
		}
		pn.Status = w.status[pn.ID]
	}
	for id := range w.status {
		if tree.Pane(id) == nil {
			delete(w.status, id)
		}
	}

	// Repos come from the map WatchPanes keeps, so this is a lookup and never
	// a probe. It is at worst one tick behind, which is what a directory that
	// has just been cd'd into costs - and the alternative is resolving paths
	// on a path that must not block.
	s.mu.Lock()
	byPane := make(map[string]string, len(panes))
	for repo, panes := range s.paneRepos {
		for _, id := range panes {
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
	w.tree, w.at = tree, time.Now()
	w.pub.Unlock()
	return tree
}
