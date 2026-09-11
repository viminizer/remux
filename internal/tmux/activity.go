package tmux

import (
	"context"
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

// Previewer is the slice of tmux Capture needs. *Client satisfies it, and so
// does the fake both consumers test against.
type Previewer interface {
	Previews(ctx context.Context, paneIDs []string, n int) map[string]string
}

// Screened is one pane the gate let through, paired with the screen that
// changed.
type Screened struct {
	Pane   *Pane
	Screen string
	// Captured separates "capture-pane failed for this pane" from "this pane
	// is blank". Previews omits a pane it could not read, so both arrive as
	// the empty string, and they mean opposite things: a blank screen is a
	// verdict, a failed exec is the absence of one. A caller that conflates
	// them reclassifies a working pane as Unknown on one flaky exec and
	// clears the glyph it had.
	Captured bool
}

// Capture runs the gate over panes and reads the screens of the ones that
// moved, returned in the order they were given.
//
// Both consumers of the gate want exactly this sequence - filter, capture,
// classify - and each had grown its own copy of it. The copies drifted: the
// push watcher prunes its per-pane maps when a pane disappears and the titler
// did not, so the same fix had to be found twice.
func (g *ActivityGate) Capture(ctx context.Context, p Previewer, panes []*Pane) []Screened {
	return g.CaptureWith(ctx, p, panes, nil)
}

// CaptureWith is Capture plus a set of panes to read whatever the gate says.
//
// The gate answers "can this screen have changed". That is the right question
// for a verdict computed from the screen and the wrong one for a caller that
// needs a screen it has never successfully used - it would wait for output
// that an idle pane is never going to produce. always is how such a caller
// says so, and it still costs one capture, because a pane named twice is only
// listed once.
func (g *ActivityGate) CaptureWith(ctx context.Context, p Previewer, panes, always []*Pane) []Screened {
	stale := map[string]bool{}
	for _, id := range g.Changed(panes) {
		stale[id] = true
	}
	for _, pane := range always {
		stale[pane.ID] = true
	}
	ids := make([]string, 0, len(stale))
	for _, pane := range panes {
		if stale[pane.ID] {
			ids = append(ids, pane.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}

	screens := p.Previews(ctx, ids, PreviewLines)

	out := make([]Screened, 0, len(ids))
	for _, pane := range panes {
		if !stale[pane.ID] {
			continue
		}
		screen, ok := screens[pane.ID]
		out = append(out, Screened{Pane: pane, Screen: screen, Captured: ok})
	}
	return out
}
