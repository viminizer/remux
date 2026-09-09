package tmux

import (
	"testing"
	"time"
)

func panesAt(act int64) []*Pane {
	return []*Pane{
		{ID: "%1", WindowID: "@1", Activity: act},
		{ID: "%2", WindowID: "@1", Activity: act},
		{ID: "%3", WindowID: "@2", Activity: act},
	}
}

// Old enough that the two-second same-second guard does not fire.
func old(delta int64) int64 { return time.Now().Unix() - 10 + delta }

func TestGateReturnsEverythingItHasNotSeen(t *testing.T) {
	g := NewActivityGate()
	if got := g.Changed(panesAt(old(0))); len(got) != 3 {
		t.Fatalf("first call returned %d panes, want all 3", len(got))
	}
}

func TestGateSkipsQuietWindows(t *testing.T) {
	g := NewActivityGate()
	g.Changed(panesAt(old(0)))
	if got := g.Changed(panesAt(old(0))); len(got) != 0 {
		t.Errorf("nothing moved but %v came back", got)
	}
}

func TestGateReturnsTheWindowThatMoved(t *testing.T) {
	g := NewActivityGate()
	panes := panesAt(old(0))
	g.Changed(panes)

	// Output in @1 only. Both its panes are captured: window_activity is
	// per window, so we cannot tell which of them printed.
	panes[0].Activity = old(3)
	panes[1].Activity = old(3)
	got := g.Changed(panes)
	if len(got) != 2 || got[0] != "%1" || got[1] != "%2" {
		t.Errorf("got %v, want [%%1 %%2]", got)
	}
}

// window_activity is a whole second, so output landing after the capture but
// inside the same second leaves it looking untouched. Anything that moved in
// the last two seconds is captured again regardless.
func TestGateRecapturesTheCurrentSecond(t *testing.T) {
	g := NewActivityGate()
	now := time.Now().Unix()
	g.Changed(panesAt(now))
	if got := g.Changed(panesAt(now)); len(got) != 3 {
		t.Errorf("same-second activity returned %d panes, want 3", len(got))
	}
}

func TestGateForgetsDeadWindows(t *testing.T) {
	g := NewActivityGate()
	g.Changed(panesAt(old(0)))

	only := []*Pane{{ID: "%3", WindowID: "@2", Activity: old(0)}}
	g.Changed(only)
	if n := len(g.seen); n != 1 {
		t.Errorf("seen holds %d windows after @1 went away, want 1", n)
	}

	// A recycled window id must not inherit the timestamp of the dead one.
	if got := g.Changed(panesAt(old(0))); len(got) != 2 {
		t.Errorf("got %v, want @1's two panes back", got)
	}
}

func TestForgetMakesTheNextCallComplete(t *testing.T) {
	g := NewActivityGate()
	g.Changed(panesAt(old(0)))
	g.Forget()
	if got := g.Changed(panesAt(old(0))); len(got) != 3 {
		t.Errorf("after Forget got %d panes, want 3", len(got))
	}
}
