package tmux

import (
	"sync"
	"time"
)

// ActivityGate answers one question: which panes can I skip capturing?
//
// remux used to sweep the whole workspace on a timer - the push watcher every
// 5 s forever, the tree poller every 2 s per connection - and every sweep ran
// capture-pane against all 27 panes. That is ~20 ms of CPU per pane per
// sweep, and it ran whether or not anything on the laptop had moved. Measured,
// it held about 19% of a core permanently.
//
// tmux already knows the answer. window_activity is a unix second that it
// bumps whenever anything is written to a pane in that window, and it rides
// along in the tree we were fetching anyway. If it has not moved, the screen
// has not changed, so the previous verdict still stands and there is nothing
// to capture.
//
// One gate per consumer: the watcher and each connection's poller run on
// different intervals and must not consume each other's "seen" marks.
type ActivityGate struct {
	mu   sync.Mutex
	seen map[string]int64 // window id -> activity at the last capture
}

func NewActivityGate() *ActivityGate {
	return &ActivityGate{seen: map[string]int64{}}
}

// Changed returns the ids of the panes worth capturing now, and records what
// it saw. A pane never seen before is always returned, so the first sweep
// after a connect is complete.
func (g *ActivityGate) Changed(panes []*Pane) []string {
	now := time.Now().Unix()

	g.mu.Lock()
	defer g.mu.Unlock()

	live := make(map[string]bool, len(panes))
	out := make([]string, 0, len(panes))
	for _, p := range panes {
		live[p.WindowID] = true
		prev, had := g.seen[p.WindowID]
		// window_activity has one-second resolution, so output that lands
		// after the capture but inside the same second leaves the timestamp
		// looking untouched. Anything that moved in the last two seconds is
		// captured again regardless, which costs one extra sweep of the panes
		// that are actually working and closes the hole.
		if !had || prev != p.Activity || now-p.Activity <= 2 {
			out = append(out, p.ID)
		}
	}
	for _, p := range panes {
		g.seen[p.WindowID] = p.Activity
	}
	// Forget windows that are gone, so the map cannot grow without bound and
	// a recycled window id does not inherit a stale timestamp.
	for id := range g.seen {
		if !live[id] {
			delete(g.seen, id)
		}
	}
	return out
}

// Forget drops every mark, so the next Changed returns everything. Used when a
// consumer knows its cached verdicts are worthless - a reconnect, a resume.
func (g *ActivityGate) Forget() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seen = map[string]int64{}
}
