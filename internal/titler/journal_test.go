package titler

import (
	"context"
	"errors"
	"testing"

	"github.com/viminizer/remux/internal/tmux"
)

// The journal's whole job is to say where a name came from. A model answer and
// the tier-4 screen fallback look identical on the pane, and they are not
// worth the same: one is what was paid for, the other is what happens when the
// chain is down.
func TestARunRecordsWhatItWroteAndWhichTierWroteIt(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen

	p := newPass(f)
	p.Chain = []Runner{&fakeRunner{label: "fake", out: "1: venue filter pagination\n"}}

	p.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "codex", Path: "/Users/mac/dev/shortlist"},
	))
	settle(t, p)

	n := p.Report()
	if n.Calls != 1 || n.Wrote != 1 || n.Failed != 0 {
		t.Fatalf("totals = %d calls, %d wrote, %d failed", n.Calls, n.Wrote, n.Failed)
	}
	if len(n.Runs) != 1 {
		t.Fatalf("kept %d runs", len(n.Runs))
	}
	if n.Chars != n.Runs[0].Chars {
		t.Errorf("chars total = %d, want the one run's %d", n.Chars, n.Runs[0].Chars)
	}
	run := n.Runs[0]
	if run.Tier != "fake" {
		t.Errorf("tier = %q, want the runner that answered", run.Tier)
	}
	if run.Panes != 1 || run.Chars == 0 || run.At == 0 {
		t.Errorf("run = %+v; panes, prompt size and a timestamp are the cost record", run)
	}
	if len(run.Names) != 1 {
		t.Fatalf("recorded %d names", len(run.Names))
	}
	if got := run.Names[0]; got.Pane != "%1" || got.Title != "venue filter pagination" || got.From != fromModel {
		t.Errorf("name = %+v", got)
	}
}

// A chain that fails at every tier is the case worth seeing from the phone,
// because nothing on the laptop shows it: the panes simply stay as they were.
func TestAFailedChainIsFiledWithTheReasonPerTier(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen

	p := newPass(f)
	p.Chain = []Runner{
		&fakeRunner{label: "dead", err: errors.New("not logged in")},
		&fakeRunner{label: "junk", out: "I'm sorry, I can't help with that."},
	}

	p.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "codex", RemuxTask: "venue filter pagination"},
	))
	settle(t, p)

	n := p.Report()
	if n.Failed != 1 {
		t.Errorf("failed = %d, want 1", n.Failed)
	}
	if n.RetryMS <= 0 {
		t.Error("a failed chain backs off, and the phone has to be able to say for how long")
	}
	run := n.Runs[0]
	if run.Tier != "" {
		t.Errorf("tier = %q, want empty when nothing answered", run.Tier)
	}
	if len(run.Notes) != 2 {
		t.Fatalf("notes = %v; one per tier tried, or there is no telling which one broke", run.Notes)
	}
	if len(run.Names) != 0 {
		t.Errorf("wrote %v with no answer", run.Names)
	}
}

// A name off the screen is not a model answer, and a report that blurred the
// two would make a dead chain look like a working one.
func TestTheScreenFallbackIsRecordedAsSuchNotAsTheModel(t *testing.T) {
	f := newFake()
	f.screens["%1"] = "> fix the token expiry test\n"

	p := newPass(f)
	p.Chain = []Runner{&fakeRunner{label: "dead", err: errors.New("rate limited")}}

	p.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "codex"},
	))
	settle(t, p)

	run := p.Report().Runs[0]
	if len(run.Names) != 1 || run.Names[0].From != fromScreen {
		t.Fatalf("names = %+v, want one marked %q", run.Names, fromScreen)
	}
}

// The journal runs inside a LaunchAgent for weeks. Unbounded, it would be the
// one place in the naming pass that grows without limit.
func TestTheJournalStopsAtItsLimit(t *testing.T) {
	p := newPass(newFake())
	for i := 0; i < kept*3; i++ {
		p.record(Run{At: int64(i), Tier: "fake"})
	}

	n := p.Report()
	if len(n.Runs) != kept {
		t.Fatalf("kept %d runs, want %d", len(n.Runs), kept)
	}
	// Newest first, so the phone shows the interesting end without scrolling.
	if n.Runs[0].At != int64(kept*3-1) {
		t.Errorf("first run is at %d, want the newest", n.Runs[0].At)
	}
	if n.Calls != kept*3 {
		t.Errorf("calls = %d; the totals count everything, not just what is kept", n.Calls)
	}
}

// Off and "no CLI installed" look identical from the phone and want completely
// different fixes, so the report carries both.
func TestTheReportSaysWhatTheChainWouldRun(t *testing.T) {
	p := newPass(newFake())
	p.Chain = []Runner{&fakeRunner{label: "claude haiku"}, &fakeRunner{label: "codex"}}
	p.Enabled = func() bool { return false }

	n := p.Report()
	if n.Enabled {
		t.Error("enabled = true with the switch off")
	}
	if len(n.Chain) != 2 || n.Chain[0] != "claude haiku" {
		t.Errorf("chain = %v", n.Chain)
	}
}
