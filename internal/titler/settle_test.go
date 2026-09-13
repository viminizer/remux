package titler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/viminizer/remux/internal/tmux"
)

// round drives one more naming pass over a workspace that is otherwise still.
//
// Two things have to be forced for that to happen at all, and both are the
// point of the feature rather than test scaffolding. The cooldown is lapsed by
// hand because askEvery is ninety seconds of real time. The window activity is
// moved because a pane that already has a name is only recaptured when its
// window writes something - a named workspace sitting still costs nothing, and
// these tests would otherwise be asserting against a gate, not a settler.
func round(t *testing.T, p *Pass, activity int64, panes ...*tmux.Pane) {
	t.Helper()
	p.asked.seen = map[string]time.Time{}
	p.OnTree(context.Background(), tree(activity, panes...))
	settle(t, p)
}

// pane rebuilds the pane record the way a fresh tree poll would: whatever
// remux wrote last time is on it now.
func namedPane(f *fakePanes, id, title string) *tmux.Pane {
	return &tmux.Pane{ID: id, Command: "codex", Title: title, RemuxTask: f.tasks[id]}
}

// The saving, stated as a test: a name the model confirms twice stops costing
// anything at all.
func TestTwoSamesStopTheAsking(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen
	f.tasks["%1"] = "review pr 269"

	r := &fakeRunner{label: "fake", out: "1: SAME\n"}
	p := newPass(f)
	p.Chain = []Runner{r}

	round(t, p, 100, namedPane(f, "%1", "✳ Review PRs"))
	round(t, p, 200, namedPane(f, "%1", "✳ Review PRs"))
	if r.count() != 2 {
		t.Fatalf("asked %d times before settling; want 2", r.count())
	}

	// Everything that would have provoked a call before: the cooldown lapsed,
	// the window busy, the screen still there.
	round(t, p, 300, namedPane(f, "%1", "✳ Review PRs"))
	round(t, p, 400, namedPane(f, "%1", "✳ Review PRs"))
	if r.count() != 2 {
		t.Errorf("asked %d times; a settled pane must leave the rotation", r.count())
	}
	if got := f.tasks["%1"]; got != "review pr 269" {
		t.Errorf("task = %q, want the confirmed name untouched", got)
	}
}

// One SAME is a screen with nothing new on it. Two is a name that survived two
// different screens - see settleAfter.
func TestOneSameDoesNotStopTheAsking(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen
	f.tasks["%1"] = "review pr 269"

	r := &fakeRunner{label: "fake", out: "1: SAME\n"}
	p := newPass(f)
	p.Chain = []Runner{r}

	round(t, p, 100, namedPane(f, "%1", "✳ Review PRs"))
	if p.settles.locked("%1") {
		t.Fatal("locked after a single SAME")
	}
	round(t, p, 200, namedPane(f, "%1", "✳ Review PRs"))
	if !p.settles.locked("%1") {
		t.Errorf("still not locked after %d SAMEs", settleAfter)
	}
}

// A name that changed has been confirmed by nothing, so the count starts over.
func TestANewNameUnsettlesThePane(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen
	f.tasks["%1"] = "review pr 269"

	r := &fakeRunner{label: "fake", out: "1: SAME\n"}
	p := newPass(f)
	p.Chain = []Runner{r}

	round(t, p, 100, namedPane(f, "%1", "✳ Review PRs"))

	r.out = "1: fix token expiry test\n"
	round(t, p, 200, namedPane(f, "%1", "✳ Review PRs"))
	if f.tasks["%1"] != "fix token expiry test" {
		t.Fatalf("task = %q, want the new name written", f.tasks["%1"])
	}
	if p.settles.locked("%1") {
		t.Error("a pane locked on a SAME that came before a rename")
	}

	// And it has to earn the lock again from zero.
	r.out = "1: SAME\n"
	round(t, p, 300, namedPane(f, "%1", "✳ Review PRs"))
	if p.settles.locked("%1") {
		t.Error("locked after one SAME following a rename")
	}
}

// The reset signal. Kevin ends a session by clearing it or opening a new one,
// and both agents rewrite pane_title when that happens.
func TestANewAgentTitleStartsTheAskingAgain(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen
	f.tasks["%1"] = "review pr 269"

	r := &fakeRunner{label: "fake", out: "1: SAME\n"}
	p := newPass(f)
	p.Chain = []Runner{r}

	round(t, p, 100, namedPane(f, "%1", "✳ Review PRs"))
	round(t, p, 200, namedPane(f, "%1", "✳ Review PRs"))
	if !p.settles.locked("%1") {
		t.Fatal("did not settle")
	}

	r.out = "1: fix issue 296 alignment\n"
	round(t, p, 300, namedPane(f, "%1", "✳ Issue 296"))
	if got := f.tasks["%1"]; got != "fix issue 296 alignment" {
		t.Errorf("task = %q; a new session did not get a new name", got)
	}
}

// The spinner moves several times a second. If that counted as a new session
// the lock would never hold for more than one pass, which is the bug this
// whole design would have shipped with.
func TestASpinnerChangeIsNotANewSession(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen
	f.tasks["%1"] = "review pr 269"

	r := &fakeRunner{label: "fake", out: "1: SAME\n"}
	p := newPass(f)
	p.Chain = []Runner{r}

	round(t, p, 100, namedPane(f, "%1", "⠧ Fix GitHub issues | shortlist"))
	round(t, p, 200, namedPane(f, "%1", "⠹ Fix GitHub issues | shortlist"))
	if !p.settles.locked("%1") {
		t.Fatal("did not settle across a spinner change")
	}

	r.out = "1: something else entirely\n"
	round(t, p, 300, namedPane(f, "%1", "⠏ Fix GitHub issues | shortlist"))
	if r.count() != 2 {
		t.Errorf("asked %d times; a moving spinner was read as a new session", r.count())
	}
	if got := f.tasks["%1"]; got != "review pr 269" {
		t.Errorf("task = %q, want the settled name untouched", got)
	}
}

// A blank pane is the one worth asking about again, so NONE must not settle it.
func TestNoneNeverSettlesAPane(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen

	r := &fakeRunner{label: "fake", out: "1: NONE\n"}
	p := newPass(f)
	p.Chain = []Runner{r}

	for _, a := range []int64{100, 200, 300} {
		round(t, p, a, namedPane(f, "%1", "✳ Claude Code"))
	}
	if p.settles.locked("%1") {
		t.Error("a pane the model refused to name was locked")
	}
	if r.count() != 3 {
		t.Errorf("asked %d times; a blank pane must stay in the rotation", r.count())
	}
}

func TestNormalizeTitle(t *testing.T) {
	cases := []struct {
		name, raw, dir, project, want string
	}{
		{"claude code spinner", "✳ GitHub repo watchlist screen", "/x/remux", "remux", "github repo watchlist screen"},
		{"codex spinner and suffix", "⠹ Fix GitHub issues | shortlist", "/x/shortlist", "shortlist", "fix github issues"},
		{"suffix matches project not dir", "Review PRs | remux", "/x/.claude/worktrees/w", "remux", "review prs"},
		{"a pipe that is not the suffix", "a | b filter", "/x/remux", "remux", "a | b filter"},
		{"placeholder", "Claude Code", "/x/remux", "remux", ""},
		{"spinner on a placeholder", "✳ Claude Code", "/x/remux", "remux", ""},
		{"bare directory", "shortlist", "/x/shortlist", "shortlist", ""},
		{"nothing", "", "/x/remux", "remux", ""},
		{"leading hash survives", "✳ #339 venue filter", "/x/remux", "remux", "#339 venue filter"},
		{"whitespace collapsed", "  ✳   Issue   39  ", "/x/remux", "remux", "issue 39"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizeTitle(c.raw, c.dir, c.project); got != c.want {
				t.Errorf("normalizeTitle(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

// The agent's own title is the only record of the goal on a Codex pane, where
// the request scrolls away and no recap is ever printed.
func TestPromptCarriesTheAgentsOwnTitle(t *testing.T) {
	got := buildPrompt([]job{{
		ID: "%1", Project: "shortlist", Screen: "PR #457 is ready for review.",
		Agent: "review prs in parallel",
	}})
	if !strings.Contains(got, "agent calls itself:\n") {
		t.Error("prompt does not label the agent's own title")
	}
	if !strings.Contains(got, "review prs in parallel") {
		t.Error("prompt does not carry the agent's own title")
	}
	// Fenced like the screen: it is written by the same untrusted program.
	if strings.Count(got, "-----") < 4 {
		t.Errorf("agent title is not fenced; found %d markers", strings.Count(got, "-----"))
	}
}

// A pane with no usable title says nothing rather than saying "empty", which
// would be a line of prompt per pane spent on nothing.
func TestPromptOmitsAnAbsentAgentTitle(t *testing.T) {
	got := buildPrompt([]job{{ID: "%1", Screen: "working"}})
	// The label, not the mention: the rules name the field so the model knows
	// what it is, and that line is there whether any pane has one.
	if strings.Contains(got, "agent calls itself:\n") {
		t.Error("prompt carries an agent title block for a pane with no title")
	}
}

// The rules the strategy turns on, asserted so a future prompt edit that drops
// one is a failing test rather than a slow drift back to step-naming.
func TestPromptStatesTheWorkingPattern(t *testing.T) {
	got := buildPrompt([]job{{ID: "%1", Screen: "working"}})
	for _, want := range []string{
		"one session per problem",
		"name the problem, not the phase",
		"compaction summary",
		"imperative, never past tense",
		"SAME is the normal answer",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt is missing the rule %q", want)
		}
	}
}

// Models confirm a name by repeating it at least as often as by saying SAME.
// Measured on the live workspace: four of eight confirmations came back as the
// name itself. Read only the keyword and those panes never settle.
func TestRepeatingTheNameSettlesLikeSame(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen
	f.tasks["%1"] = "review pr 269"

	r := &fakeRunner{label: "fake", out: "1: review pr 269\n"}
	p := newPass(f)
	p.Chain = []Runner{r}

	round(t, p, 100, namedPane(f, "%1", "✳ Review PRs"))
	round(t, p, 200, namedPane(f, "%1", "✳ Review PRs"))
	if !p.settles.locked("%1") {
		t.Fatal("a name repeated back twice did not settle the pane")
	}

	r.out = "1: something else\n"
	round(t, p, 300, namedPane(f, "%1", "✳ Review PRs"))
	if got := f.tasks["%1"]; got != "review pr 269" {
		t.Errorf("task = %q, want the settled name untouched", got)
	}
}

// Capitalisation and trailing punctuation are what clean() exists for, so a
// confirmation that comes back "Review PR 269." still counts as one.
func TestARepeatIsComparedAfterCleaning(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen
	f.tasks["%1"] = "review pr 269"

	r := &fakeRunner{label: "fake", out: "1: Review PR 269.\n"}
	p := newPass(f)
	p.Chain = []Runner{r}

	round(t, p, 100, namedPane(f, "%1", "✳ Review PRs"))
	round(t, p, 200, namedPane(f, "%1", "✳ Review PRs"))
	if !p.settles.locked("%1") {
		t.Errorf("did not settle; clean() gave %q", f.tasks["%1"])
	}
}

func TestCleanTrimsTrailingPunctuation(t *testing.T) {
	cases := map[string]string{
		"Review PR 269.":      "review pr 269",
		"review pr 269":       "review pr 269",
		"bump jq to v0.3.62":  "bump jq to v0.3.62",
		"fix config.json":     "fix config.json",
		"mark pr #457 ready!": "mark pr #457 ready",
		"open pr -":           "open pr",
	}
	for in, want := range cases {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}
}
