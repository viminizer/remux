package titler

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sep(day, hour int) time.Time {
	return time.Date(2026, 9, day, hour, 0, 0, 0, time.UTC)
}

func TestLedgerAccumulatesAcrossRestarts(t *testing.T) {
	path := SpendPath(t.TempDir())

	l := OpenLedger(path)
	l.Add(Run{Tier: "claude haiku", USD: 0.003, Metered: 1}, sep(5, 10))
	l.Add(Run{Tier: "claude haiku", USD: 0.037, Metered: 1}, sep(5, 11))

	// A new process, the same file. This is the whole reason the ledger is
	// not the ring buffer: remux restarts several times on a working day.
	again := OpenLedger(path)
	this, prev := again.Report(sep(5, 12))
	if prev != nil {
		t.Errorf("prev = %+v, want nil in the first month seen", prev)
	}
	if math.Abs(this.USD-0.040) > 1e-9 {
		t.Errorf("usd = %v, want 0.040", this.USD)
	}
	if this.Runs != 2 {
		t.Errorf("runs = %d, want 2", this.Runs)
	}
	if this.Month != "2026-09" {
		t.Errorf("month = %q, want 2026-09", this.Month)
	}
}

func TestLedgerRollsOverAtTheMonth(t *testing.T) {
	l := OpenLedger(SpendPath(t.TempDir()))
	l.Add(Run{Tier: "x", USD: 2, Metered: 1}, sep(28, 9))

	// October, with nothing spent in it yet. Report has to roll on its own:
	// a Mac left running over the boundary would otherwise show September's
	// total under October's heading until the next call.
	this, prev := l.Report(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	if this.Month != "2026-10" || this.USD != 0 {
		t.Errorf("this = %+v, want an empty 2026-10", this)
	}
	if prev == nil || prev.Month != "2026-09" || prev.USD != 2 {
		t.Errorf("prev = %+v, want 2026-09 at 2", prev)
	}
}

func TestLedgerCountsWhatNobodyMetered(t *testing.T) {
	// A tier that answers in plain text bills all the same. Counting it as
	// free would make the month's total quietly wrong, always downwards.
	l := OpenLedger(SpendPath(t.TempDir()))
	l.Add(Run{Tier: "codex", USD: 0, Metered: 0}, sep(3, 9))
	l.Add(Run{Tier: "claude haiku", USD: 0.01, Metered: 1}, sep(3, 10))

	this, _ := l.Report(sep(3, 11))
	if this.Runs != 2 {
		t.Errorf("runs = %d, want 2", this.Runs)
	}
	if this.Unmetered != 1 {
		t.Errorf("unmetered = %d, want 1", this.Unmetered)
	}
}

func TestLedgerSurvivesARubbishFile(t *testing.T) {
	// Refusing to name panes because a bookkeeping file was truncated would
	// be the wrong trade. The month restarts at zero and the feature runs.
	path := SpendPath(t.TempDir())
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := OpenLedger(path)
	l.Add(Run{Tier: "x", USD: 1, Metered: 1}, sep(4, 9))
	if this, _ := l.Report(sep(4, 9)); this.USD != 1 {
		t.Errorf("usd = %v, want 1", this.USD)
	}
}

func TestLedgerWritesWholeFiles(t *testing.T) {
	// The service is killed, not stopped, so a half-written ledger is a real
	// outcome - and OpenLedger reads one as zero. Temp file plus rename is
	// what keeps that from happening; this checks no stray temp is left.
	dir := t.TempDir()
	path := SpendPath(dir)
	l := OpenLedger(path)
	l.Add(Run{Tier: "x", USD: 0.5, Metered: 1}, sep(6, 9))

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var back book
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("file is not whole json: %v", err)
	}
	if back.This.USD != 0.5 {
		t.Errorf("read back %v, want 0.5", back.This.USD)
	}
	if _, err := os.Stat(filepath.Join(dir, "naming-spend.json.tmp")); err == nil {
		t.Error("left a .tmp behind")
	}
}

func TestProjectScalesByElapsedDays(t *testing.T) {
	// Ten days in, twenty dollars spent, thirty days in September.
	got := project(Month{USD: 20}, sep(11, 0))
	if math.Abs(got-60) > 1e-9 {
		t.Errorf("project = %v, want 60", got)
	}
	if got := project(Month{USD: 0}, sep(11, 0)); got != 0 {
		t.Errorf("project of nothing = %v, want 0", got)
	}
	// The first hours of a month must not multiply one call into a fortune.
	if got := project(Month{USD: 1}, sep(1, 0)); got > 121 {
		t.Errorf("project on day one = %v, want the floor to hold it near 120", got)
	}
}

func TestRecordFilesTheRunInTheLedger(t *testing.T) {
	// The wiring, not the ledger: a run that reaches the journal has to reach
	// the books too, or the month's total is whatever the screen last polled.
	path := SpendPath(t.TempDir())
	p := New(nil)
	p.Ledger = OpenLedger(path)

	p.record(Run{Tier: "claude haiku", USD: 0.004, Metered: 1})

	if this, _ := OpenLedger(path).Report(time.Now()); this.USD != 0.004 || this.Runs != 1 {
		t.Errorf("ledger on disk = %+v, want one run at 0.004", this)
	}
	n := p.Report()
	if n.Spend == nil || n.Spend.USD != 0.004 {
		t.Errorf("report spend = %+v, want 0.004", n.Spend)
	}
	if n.USD != 0.004 {
		t.Errorf("process usd = %v, want 0.004", n.USD)
	}
}

func TestReportHasNoSpendWithoutALedger(t *testing.T) {
	// Every test gets this shape. Naming is the one thing here that spends
	// money, and forgetting a field must not write to the real books.
	p := New(nil)
	p.record(Run{Tier: "fake", USD: 9})
	if n := p.Report(); n.Spend != nil {
		t.Errorf("spend = %+v, want nil with no ledger", n.Spend)
	}
}
