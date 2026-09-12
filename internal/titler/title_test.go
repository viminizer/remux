package titler

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/viminizer/remux/internal/tmux"
)

// fakeRunner stands in for one tier of the chain.
type fakeRunner struct {
	label string
	out   string
	err   error
	// usd is what this tier reports the call cost. Zero leaves the answer
	// unmetered, which is the tier-3 shape.
	usd float64

	mu      sync.Mutex
	calls   int
	prompts []string
}

func (r *fakeRunner) Name() string { return r.label }

func (r *fakeRunner) Run(_ context.Context, prompt string) (Answer, error) {
	r.mu.Lock()
	r.calls++
	r.prompts = append(r.prompts, prompt)
	r.mu.Unlock()
	if r.err != nil {
		return Answer{}, r.err
	}
	return Answer{Text: r.out, USD: r.usd, Metered: r.usd > 0}, nil
}

func (r *fakeRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// settle waits for the background naming run to finish. The pass deliberately
// does not block the caller's tick on a model call, so the tests have to.
func settle(t *testing.T, p *Pass) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for p.naming.Load() {
		if time.Now().After(deadline) {
			t.Fatal("naming run did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNamesEveryPaneInOneCall(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen
	f.screens["%2"] = busyScreen

	// The failure this guards against is the one every transcript-mapping
	// heuristic had: two panes in the same repo, told apart only by what is
	// on their screens.
	r := &fakeRunner{label: "fake", out: "1: venue filter pagination\n2: token refresh race\n"}
	p := newPass(f)
	p.Chain = []Runner{r}

	p.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "codex", Path: "/Users/mac/dev/shortlist"},
		&tmux.Pane{ID: "%2", Command: "codex", Path: "/Users/mac/dev/shortlist"},
	))
	settle(t, p)

	if r.count() != 1 {
		t.Errorf("made %d model calls for 2 panes; the batch is the whole cost story", r.count())
	}
	if got := f.tasks["%1"]; got != "venue filter pagination" {
		t.Errorf("pane %%1 task = %q", got)
	}
	if got := f.tasks["%2"]; got != "token refresh race" {
		t.Errorf("pane %%2 task = %q", got)
	}
}

// SAME is what stops a title flickering between synonyms every ninety seconds.
func TestSameLeavesTheTitleAlone(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen

	r := &fakeRunner{label: "fake", out: "1: SAME\n"}
	p := newPass(f)
	p.Chain = []Runner{r}

	p.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "codex", RemuxTask: "venue filter pagination"},
	))
	settle(t, p)

	if _, wrote := f.tasks["%1"]; wrote {
		t.Errorf("SAME still rewrote the task: %q", f.tasks["%1"])
	}
}

// A pane is asked about at most once per cooldown, however many ticks it takes
// to get there. Without this, a busy agent's window has activity on every
// tick, the gate passes it through, and a two-second tree poll becomes a model
// call every two seconds.
func TestOnePaneIsNotAskedTwiceInsideTheCooldown(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen

	r := &fakeRunner{label: "fake", out: "1: venue filter pagination\n"}
	p := newPass(f)
	p.Chain = []Runner{r}

	for i := 0; i < 3; i++ {
		// A new activity value each time, so the gate always lets it through.
		p.OnTree(context.Background(), tree(int64(100+i),
			&tmux.Pane{ID: "%1", Command: "codex"},
		))
		settle(t, p)
	}

	if r.count() != 1 {
		t.Errorf("asked %d times in %v; the cooldown did not hold", r.count(), askEvery)
	}
}

// The chain falls through on a failing tier, and the prompt it hands the next
// one is the same question.
func TestChainFallsThrough(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen

	dead := &fakeRunner{label: "dead", err: errors.New("not found")}
	junk := &fakeRunner{label: "junk", out: "I'm sorry, I can't help with that."}
	good := &fakeRunner{label: "good", out: "1: token refresh race\n"}

	p := newPass(f)
	p.Chain = []Runner{dead, junk, good}

	p.OnTree(context.Background(), tree(100, &tmux.Pane{ID: "%1", Command: "codex"}))
	settle(t, p)

	if dead.count() != 1 || junk.count() != 1 || good.count() != 1 {
		t.Errorf("chain calls: dead=%d junk=%d good=%d; each tier should be tried once",
			dead.count(), junk.count(), good.count())
	}
	if got := f.tasks["%1"]; got != "token refresh race" {
		t.Errorf("task = %q, want the answer from the tier that worked", got)
	}
}

// Tier 4. Every model failed, so the name comes off the pane's own screen. It
// cannot fail, which is what makes the chain terminate - the alternative is a
// pane left blank, or worse, left with a name from an hour ago.
func TestFallsBackToTheScreenWhenNoModelAnswers(t *testing.T) {
	f := newFake()
	f.screens["%1"] = "some output\n❯ fix the venue filter pagination bug\n"

	p := newPass(f)
	p.Chain = []Runner{&fakeRunner{label: "dead", err: errors.New("not found")}}

	p.OnTree(context.Background(), tree(100, &tmux.Pane{ID: "%1", Command: "codex"}))
	settle(t, p)

	if got := f.tasks["%1"]; got != "fix the venue filter pagination" {
		t.Errorf("task = %q, want the last user line, trimmed to five words", got)
	}
}

// SAME on a pane that has no name yet is the model declining, not an answer.
// Treating it as one stranded five of twenty-one panes on the first real run:
// blank forever, because the next pass asks the same question and gets the
// same shrug.
func TestSameOnAnUnnamedPaneFallsBackToTheScreen(t *testing.T) {
	f := newFake()
	f.screens["%1"] = "some output\n\u276f fix the venue filter pagination bug\n"

	p := newPass(f)
	p.Chain = []Runner{&fakeRunner{label: "fake", out: "1: SAME\n"}}

	p.OnTree(context.Background(), tree(100, &tmux.Pane{ID: "%1", Command: "codex"}))
	settle(t, p)

	if got := f.tasks["%1"]; got != "fix the venue filter pagination" {
		t.Errorf("task = %q, want the screen fallback rather than nothing", got)
	}
}

// With the switch off, the state half keeps working and no model is asked.
func TestDisabledNamesNothingButStillStates(t *testing.T) {
	f := newFake()
	f.screens["%1"] = waitingScreen

	r := &fakeRunner{label: "fake", out: "1: venue filter pagination\n"}
	p := newPass(f)
	p.Chain = []Runner{r}
	p.Enabled = func() bool { return false }

	p.OnTree(context.Background(), tree(100, &tmux.Pane{ID: "%1", Command: "codex"}))
	settle(t, p)

	if r.count() != 0 {
		t.Errorf("asked a model %d times with naming switched off", r.count())
	}
	if got := f.writes["%1"]; got != "!" {
		t.Errorf("state = %q; the free half must keep working", got)
	}
}

// A shell gets its old name cleared along with its glyph, so a pane that has
// finished does not keep advertising the task it used to be running.
func TestShellLosesItsName(t *testing.T) {
	f := newFake()
	p := newPass(f)
	p.Chain = []Runner{&fakeRunner{label: "fake", out: "1: nope\n"}}

	p.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "zsh", RemuxTask: "venue filter pagination"},
	))
	settle(t, p)

	if got, wrote := f.tasks["%1"]; !wrote || got != "" {
		t.Errorf("shell kept the name %q", got)
	}
}

func TestLastUserLineSkipsSlashCommands(t *testing.T) {
	// "/clear" is something done to the agent, not the work. On the first
	// real run it named two panes "clear".
	screen := "some output\n\u276f fix the venue filter\n\u276f /clear\n"
	if got := lastUserLine(screen); got != "fix the venue filter" {
		t.Errorf("lastUserLine = %q, want the last real instruction", got)
	}
	if got := lastUserLine("\u276f /compact\n"); got != "" {
		t.Errorf("lastUserLine = %q, want nothing rather than a command name", got)
	}
}

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"Venue Filter Pagination":                      "venue filter pagination",
		`"token refresh race"`:                         "token refresh race",
		"Fixing the venue filter, then the pagination": "fixing the venue filter then",
		"reviewing PR #364":                            "reviewing pr #364",
		"...":                                          "",
		"":                                             "",
	} {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}
}

// The model's answer is matched back to a pane by its position in the prompt.
// A stray line of CLI chatter must not throw away the titles around it.
func TestParseAnswerIgnoresChatter(t *testing.T) {
	due := []job{{ID: "%1"}, {ID: "%2"}, {ID: "%3"}}
	out := `Here are the names:

1: venue filter pagination
oops something unrelated
2. token refresh race
3) pr 364 review
9: a pane that does not exist
`
	got := parseAnswer(out, due)
	want := map[string]string{
		"%1": "venue filter pagination",
		"%2": "token refresh race",
		"%3": "pr 364 review",
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %v, want %v", got, want)
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("pane %s = %q, want %q", id, got[id], w)
		}
	}
}

// The prompt carries what tells two panes in the same repo apart: each one's
// own screen, and the name it already has for the SAME check. It also carries
// the project, so the answer does not spend two of its five words repeating
// what is already shown beside it.
func TestPromptCarriesScreenAndCurrentName(t *testing.T) {
	p := buildPrompt([]job{
		{
			ID: "%1", Dir: "/Users/mac/dev/shortlist", Task: "venue filter",
			Project: "shortlist", Screen: "\x1b[31mworking on auth\x1b[0m",
		},
	})
	for _, want := range []string{
		"## Pane 1",
		"project: shortlist",
		"never repeat the project",
		"/Users/mac/dev/shortlist",
		"current name, written from an older screen: venue filter",
		"working on auth",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
	// The screen is the evidence and the current name is a prior, so the name
	// comes second. Read first, it is taken as the answer: on the live
	// workspace every pane but the empty ones came back byte-identical to the
	// name it already had.
	if strings.Index(p, "working on auth") > strings.LastIndex(p, "current name") {
		t.Error("current name is above the screen; that is what the model answers with")
	}
	if strings.Contains(p, "\x1b[") {
		t.Error("prompt still carries ANSI escapes; that is tokens spent on colour codes")
	}
}

// The away gate is the whole cost control for an unattended overnight run, and
// it was dead: ioreg was called without -r, so HIDIdleTime was never found and
// away() answered false forever. This is the test that would have caught it -
// and the reason Pass.Away is a field.
func TestAwayAsksNothing(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen

	r := &fakeRunner{label: "fake", out: "1: venue filter pagination\n"}
	p := newPass(f)
	p.Chain = []Runner{r}
	p.Away = func() bool { return true }

	p.OnTree(context.Background(), tree(100, &tmux.Pane{ID: "%1", Command: "codex"}))
	settle(t, p)

	if r.count() != 0 {
		t.Errorf("asked a model %d times with nobody at the laptop", r.count())
	}
	// The free half still runs: the glyph costs a tmux write, not a model call.
	if got := f.writes["%1"]; got != "✳" {
		t.Errorf("state = %q; the away gate is about money, not about states", got)
	}
}

// One transient chain failure must not rewrite twenty curated titles from raw
// screen text. Tier 4 is for a pane with no name at all - that is the case its
// argument covers - and when every tier fails, every pane in the batch arrives
// there at once.
func TestChainFailureLeavesAGoodTitleAlone(t *testing.T) {
	f := newFake()
	f.screens["%1"] = "some output\n❯ fix the venue filter pagination bug\n"

	p := newPass(f)
	p.Chain = []Runner{&fakeRunner{label: "dead", err: errors.New("rate limited")}}

	p.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "codex", RemuxTask: "venue filter pagination"},
	))
	settle(t, p)

	if got, wrote := f.tasks["%1"]; wrote {
		t.Errorf("a failed chain rewrote a good title to %q", got)
	}
}

// A chain failing at every tier re-sends the same ~25KB prompt to the default
// model, so retrying it every ninety seconds forever is the one way this
// feature becomes expensive by accident. After a total failure nothing is
// asked again until the backoff expires.
func TestTotalFailureBacksOff(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen

	dead := &fakeRunner{label: "dead", err: errors.New("rate limited")}
	p := newPass(f)
	p.Chain = []Runner{dead}

	for i := 0; i < 3; i++ {
		p.OnTree(context.Background(), tree(int64(100+i), &tmux.Pane{ID: "%1", Command: "codex"}))
		settle(t, p)
	}

	if dead.count() != 1 {
		t.Errorf("retried %d times; the backoff did not hold", dead.count())
	}
	if p.backoff != askEvery {
		t.Errorf("backoff = %v, want it to start at %v", p.backoff, askEvery)
	}
}

// SAME is answered in more shapes than the constant. The prompt asks for
// lowercase three rules earlier, so "same" is at least as likely as "SAME",
// and matching only the exact string wrote "same" into the pane as a title -
// for every pane at once, since one model answers the whole batch.
func TestIsSame(t *testing.T) {
	for in, want := range map[string]bool{
		"SAME":                               true,
		"same":                               true,
		"SAME.":                              true,
		`"SAME"`:                             true,
		" same ":                             true,
		"SAME - the current name still fits": true,
		"same (nothing has changed)":         true,
		// Not SAME: a real title that happens to start with the word.
		"same origin policy fix":  false,
		"venue filter pagination": false,
		"":                        false,
	} {
		if got := isSame(in); got != want {
			t.Errorf("isSame(%q) = %v, want %v", in, got, want)
		}
	}
}

// Both maps are keyed by pane id in a process that runs for weeks. tmux also
// recycles pane ids, so an inherited cooldown would leave a brand new pane
// unnamed for up to askEvery.
func TestClosingAPaneForgetsIt(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen

	p := newPass(f)
	p.Chain = []Runner{&fakeRunner{label: "fake", out: "1: venue filter pagination\n"}}

	p.OnTree(context.Background(), tree(100, &tmux.Pane{ID: "%1", Command: "codex"}))
	settle(t, p)

	// The pane is gone on the next pass.
	p.OnTree(context.Background(), tree(101, &tmux.Pane{ID: "%2", Command: "codex"}))
	settle(t, p)

	p.asked.mu.Lock()
	_, remembered := p.asked.seen["%1"]
	p.asked.mu.Unlock()
	if remembered {
		t.Error("a pane that no longer exists is still on the cooldown")
	}
}

// Turning naming off has to clear what naming wrote. Without this a name from
// before the switch was flipped sits on the pane forever, hiding the live
// pane_title that is free and always current.
func TestDisablingNamingClearsTheOldName(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen

	p := newPass(f)
	p.Chain = []Runner{&fakeRunner{label: "fake", out: "1: nope\n"}}
	p.Enabled = func() bool { return false }

	p.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "codex", RemuxTask: "venue filter pagination"},
	))
	settle(t, p)

	if got, wrote := f.tasks["%1"]; !wrote || got != "" {
		t.Errorf("task = %q, want it cleared once naming is off", got)
	}
}

// A pane that went quiet while nobody was looking still gets named when Kevin
// comes back.
//
// This is the shape of the real failure, measured on the live workspace: of
// twenty-one agent panes, the three still writing output were named and the
// other eighteen stayed blank forever. The gate offers a pane when its window
// moves; if naming is impossible at that instant the pane is skipped, and a
// pane that has since gone quiet is never offered again - which is exactly the
// set worth naming.
func TestPanesQuietWhileAwayAreNamedOnReturn(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen

	r := &fakeRunner{label: "fake", out: "1: venue filter pagination\n"}
	p := New(f)
	p.Chain = []Runner{r}

	away := true
	p.Away = func() bool { return away }
	p.AwayFor = time.Nanosecond // no reuse, so the return is seen at once

	// Two passes while away. The first offers the pane because the gate has
	// never seen it; the second does not, and from here it never would again.
	quiet := int64(100)
	for i := 0; i < 2; i++ {
		p.OnTree(context.Background(), tree(quiet, &tmux.Pane{ID: "%1", Command: "codex"}))
		settle(t, p)
	}
	if r.count() != 0 {
		t.Fatalf("asked a model %d times while away", r.count())
	}

	// He comes back. The pane has produced nothing in the meantime, so its
	// window activity is unchanged - the gate alone would skip it.
	away = false
	p.OnTree(context.Background(), tree(quiet, &tmux.Pane{ID: "%1", Command: "codex"}))
	settle(t, p)

	if got := f.tasks["%1"]; got != "venue filter pagination" {
		t.Errorf("task = %q; a pane that went quiet while away was never named", got)
	}
}

// The re-read happens once per return, not on every pass, or coming back would
// mean recapturing every pane in the workspace every two seconds.
//
// The pane carries a name on purpose: an unnamed one is recaptured every pass
// by design (see TestAnUnnamedPaneIsRetriedWhileQuiet), so it could not tell a
// latch that re-fires from one that works.
func TestReturningReReadsOnlyOnce(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen

	p := New(f)
	p.Chain = []Runner{&fakeRunner{label: "fake", out: "1: SAME\n"}}
	away := true
	p.Away = func() bool { return away }
	p.AwayFor = time.Nanosecond

	quiet := int64(100)
	pane := func() *tmux.Pane {
		return &tmux.Pane{ID: "%1", Command: "codex", RemuxTask: "venue filter pagination"}
	}

	p.OnTree(context.Background(), tree(quiet, pane()))
	settle(t, p)

	away = false
	p.OnTree(context.Background(), tree(quiet, pane()))
	settle(t, p)
	afterReturn := len(f.captured)

	// Still here, still quiet: nothing more to read.
	p.OnTree(context.Background(), tree(quiet, pane()))
	settle(t, p)

	if len(f.captured) != afterReturn {
		t.Errorf("captured again on a quiet pass after returning (%d -> %d); "+
			"the latch is re-firing", afterReturn, len(f.captured))
	}
}

// A pane the model declined to name stays eligible, whatever the gate says.
//
// Measured against the live workspace: asked for twenty-three names in one
// batch, haiku answered eight of them SAME. For a pane that already has a name
// that is an answer; for one that does not it is a shrug, and the screen
// fallback behind it finds an empty composer more often than not. The pane is
// then blank for good, because the gate will not offer an idle pane again and
// nothing is ever going to write to its window.
//
// The same model named those very panes on the next attempt, which is the
// whole point: one unlucky answer must not be final.
func TestAnUnnamedPaneIsRetriedWhileQuiet(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen

	// First answer declines, second succeeds - the run-to-run variance that
	// made this permanent rather than momentary.
	r := &fakeRunner{label: "fake", out: "1: SAME\n"}
	p := New(f)
	p.Chain = []Runner{r}
	p.AwayFor = time.Nanosecond
	p.Away = func() bool { return false }

	quiet := int64(100)
	pane := func() *tmux.Pane { return &tmux.Pane{ID: "%1", Command: "codex"} }

	p.OnTree(context.Background(), tree(quiet, pane()))
	settle(t, p)
	if _, named := f.tasks["%1"]; named {
		t.Fatalf("SAME on an unnamed pane wrote %q", f.tasks["%1"])
	}

	// The cooldown has to lapse for a second attempt, and the pane is still
	// quiet - same window activity, nothing written to it.
	p.asked.seen = map[string]time.Time{}
	r.out = "1: prioritize demo work items\n"

	p.OnTree(context.Background(), tree(quiet, pane()))
	settle(t, p)

	if got := f.tasks["%1"]; got != "prioritize demo work items" {
		t.Errorf("task = %q; an unnamed quiet pane was never asked about again", got)
	}
}

// A pane that has a name is left to the gate, so a named workspace sitting
// still costs nothing.
func TestANamedQuietPaneIsNotRecaptured(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen

	p := New(f)
	p.Chain = []Runner{&fakeRunner{label: "fake", out: "1: SAME\n"}}
	p.AwayFor = time.Nanosecond
	p.Away = func() bool { return false }

	quiet := int64(100)
	named := func() *tmux.Pane {
		return &tmux.Pane{ID: "%1", Command: "codex", RemuxTask: "venue filter pagination"}
	}

	p.OnTree(context.Background(), tree(quiet, named()))
	settle(t, p)
	before := len(f.captured)

	p.OnTree(context.Background(), tree(quiet, named()))
	settle(t, p)

	if len(f.captured) != before {
		t.Errorf("recaptured a named quiet pane (%d -> %d)", before, len(f.captured))
	}
}

// NONE is the answer for a pane with nothing on it, and the point of it is
// that the model stops inventing. Before it existed a freshly cleared pane in
// a busy project came back named after the pane above it in the same batch.
func TestNoneClearsAStaleName(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen
	f.screens["%2"] = busyScreen

	r := &fakeRunner{label: "fake", out: "1: review pr 269\n2: NONE\n"}
	p := newPass(f)
	p.Chain = []Runner{r}

	p.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "codex", Path: "/Users/mac/dev/shortlist"},
		&tmux.Pane{ID: "%2", Command: "codex", Path: "/Users/mac/dev/shortlist", RemuxTask: "fix issue 270"},
	))
	settle(t, p)

	if got := f.tasks["%2"]; got != "" {
		t.Errorf("NONE left %q on a pane with nothing on it", got)
	}
	n := p.Report()
	if len(n.Runs) != 1 || n.Runs[0].Cleared != 1 {
		t.Fatalf("run did not record the clear: %+v", n.Runs)
	}
	if n.Wrote != 1 {
		t.Errorf("clearing counted as a name written: wrote = %d", n.Wrote)
	}
}

// NONE on a pane that is already blank is the common case - it must not fall
// through to the screen fallback, which is the path that used to guess.
func TestNoneDoesNotFallBackToTheScreen(t *testing.T) {
	f := newFake()
	f.screens["%1"] = "❯ now lets do 270\n"

	r := &fakeRunner{label: "fake", out: "1: none\n"}
	p := newPass(f)
	p.Chain = []Runner{r}

	p.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "codex"},
	))
	settle(t, p)

	if got, wrote := f.tasks["%1"]; wrote && got != "" {
		t.Errorf("NONE fell back to the screen and wrote %q", got)
	}
	if c := p.Report().Runs[0].Cleared; c != 0 {
		t.Errorf("a pane that had no name counted as cleared: %d", c)
	}
}

func TestIsNone(t *testing.T) {
	for in, want := range map[string]bool{
		"NONE":                        true,
		"none":                        true,
		"none.":                       true,
		`"none"`:                      true,
		"none - nothing on screen":    true,
		"none of the tests are green": false,
		"nonexistent route fix":       false,
		"":                            false,
	} {
		if got := isNone(in); got != want {
			t.Errorf("isNone(%q) = %v, want %v", in, got, want)
		}
	}
}

// The bug this guards against did not look like a bug. Names came back
// well-formed and about the wrong thing, because on a wide pane the whole
// screen budget was spent on the rules and status bar at the bottom and the
// model never saw a line the agent had written.
func TestWideScreensStillReachRealWork(t *testing.T) {
	rule := strings.Repeat("─", 195)
	screen := strings.Join([]string{
		"  Take api 128 next, it finishes the rule you just built",
		"",
		"※ recap: picking the critical work left before the demo",
		"",
		rule,
		"❯ ",
		rule,
		"  Opus 5 | seoulwomen-docs | 15% of 1000k tokens",
		"  129 and 215 are done",
		"  -- INSERT -- bypass permissions on",
	}, "\n")

	got := tailScreen(screen)
	if !strings.Contains(got, "recap: picking the critical work") {
		t.Errorf("the one line that says what the pane is doing did not survive:\n%s", got)
	}
	if strings.Contains(got, rule) {
		t.Error("a full-width rule made it into the prompt; that is 195 characters of the budget")
	}
	// The status bar draws its meter out of the same block characters the
	// rules use, and it is the only place the project and the last request
	// appear.
	if !strings.Contains(got, "129 and 215 are done") {
		t.Errorf("the status bar was dropped with the furniture:\n%s", got)
	}
}

func TestTailScreenStillObeysTheBudget(t *testing.T) {
	long := strings.Repeat("this is a line of an agent talking about its work\n", 200)
	got := tailScreen(long)
	if len(got) > maxScreenChars {
		t.Errorf("tailScreen returned %d chars, over the %d budget", len(got), maxScreenChars)
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "about its work") {
		t.Error("kept the head instead of the tail; the newest lines are the point")
	}
}

// A restart is not a reason to rename the workspace. Neither the cooldown nor
// the activity gate survives the process, so without this the first tree makes
// every pane due at once and the first pass rewrites names that were right.
func TestARestartDoesNotRenameNamedPanes(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen
	f.screens["%2"] = busyScreen

	r := &fakeRunner{label: "fake", out: "1: something else\n2: something else\n"}
	p := newColdPass(f)
	p.Chain = []Runner{r}

	p.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "codex", RemuxTask: "review pr 269"},
		&tmux.Pane{ID: "%2", Command: "codex", RemuxTask: "fix issue 270"},
	))
	settle(t, p)

	if r.count() != 0 {
		t.Errorf("asked the model about %d panes that already had names", r.count())
	}
	if _, wrote := f.tasks["%1"]; wrote {
		t.Errorf("renamed a pane on startup: %q", f.tasks["%1"])
	}
}

// The pane with no name is the whole point of the first pass, so it is not
// held back with the rest.
func TestARestartStillNamesABlankPane(t *testing.T) {
	f := newFake()
	f.screens["%1"] = busyScreen
	f.screens["%2"] = busyScreen

	r := &fakeRunner{label: "fake", out: "1: token refresh race\n"}
	p := newColdPass(f)
	p.Chain = []Runner{r}

	p.OnTree(context.Background(), tree(100,
		&tmux.Pane{ID: "%1", Command: "codex"},
		&tmux.Pane{ID: "%2", Command: "codex", RemuxTask: "fix issue 270"},
	))
	settle(t, p)

	if got := f.tasks["%1"]; got != "token refresh race" {
		t.Errorf("the blank pane was not named: %q", got)
	}
	if _, wrote := f.tasks["%2"]; wrote {
		t.Errorf("the named pane was dragged in with it: %q", f.tasks["%2"])
	}
}

// One prompt has a ceiling. Past it the run costs the most and returns the
// least: killed at the timeout, every tier tried, then a backoff.
func TestOneBatchIsCapped(t *testing.T) {
	f := newFake()
	var panes []*tmux.Pane
	for i := 1; i <= maxBatch+4; i++ {
		id := fmt.Sprintf("%%%d", i)
		f.screens[id] = busyScreen
		panes = append(panes, &tmux.Pane{ID: id, Command: "codex"})
	}

	r := &fakeRunner{label: "fake", out: "1: a name\n"}
	p := newPass(f)
	p.Chain = []Runner{r}

	p.OnTree(context.Background(), tree(100, panes...))
	settle(t, p)

	if n := p.Report().Runs[0].Panes; n != maxBatch {
		t.Errorf("sent %d panes in one prompt, cap is %d", n, maxBatch)
	}
	// The ones left out must not have been marked as asked, or they wait out
	// a cooldown they never cost.
	left := 0
	for _, pane := range panes[maxBatch:] {
		if p.asked.due(pane.ID, askEvery) {
			left++
		}
	}
	if left != len(panes)-maxBatch {
		t.Errorf("%d of %d panes left out of the batch are still due", left, len(panes)-maxBatch)
	}
}

// realEnvelope is trimmed from an actual `claude -p --output-format json` run
// on this machine. Keeping the real shape matters: the object has two dozen
// keys and gains more each release, and the point of the parser is that it
// ignores all of them.
const realEnvelope = `{"duration_api_ms":1929,"stop_reason":"end_turn",` +
	`"session_id":"d0e014e0","total_cost_usd":0.037389,` +
	`"usage":{"input_tokens":9,"cache_creation_input_tokens":18134,` +
	`"cache_read_input_tokens":0,"output_tokens":35,"service_tier":"standard"},` +
	`"permission_denials":[],"is_error":false,"num_turns":1,` +
	`"result":"1: venue filter pagination","type":"result"}`

func TestParseEnvelopeReadsTheAnswerAndTheBill(t *testing.T) {
	a, err := parseEnvelope(realEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if a.Text != "1: venue filter pagination" {
		t.Errorf("text = %q", a.Text)
	}
	if !a.Metered || a.USD != 0.037389 {
		t.Errorf("usd = %v metered = %v, want 0.037389 metered", a.USD, a.Metered)
	}
	if a.CacheWrite != 18134 || a.Out != 35 {
		t.Errorf("tokens = %+v", a)
	}
}

func TestParseEnvelopeRefusesWhatIsNotOne(t *testing.T) {
	// Falling back to the raw stdout would put a line of JSON, or a CLI's
	// error page, on a pane as its name - a confident wrong answer, which is
	// the one outcome tier 4 exists to avoid.
	for _, in := range []string{
		"1: venue filter pagination",
		`{"is_error":true,"result":"Credit balance is too low"}`,
		`{"is_error":false,"result":"   "}`,
	} {
		if a, err := parseEnvelope(in); err == nil {
			t.Errorf("parseEnvelope(%q) = %+v, want an error", in, a)
		}
	}
}

func TestRunIsBilledForEveryTierThatAnswered(t *testing.T) {
	// The expensive case: tier 1 answers with something unusable and tier 2
	// has to be asked. Both were paid for. Charging only the tier that
	// worked would understate exactly the runs worth knowing about.
	junk := &fakeRunner{label: "junk", out: "I'm sorry, I can't help with that.", usd: 0.03}
	good := &fakeRunner{label: "good", out: "1: token refresh race\n", usd: 0.002}

	p := newPass(newFake())
	p.Chain = []Runner{junk, good}
	_, run := p.ask(context.Background(), []job{{ID: "%1", Screen: "working"}})

	if run.Tier != "good" {
		t.Fatalf("tier = %q, want good", run.Tier)
	}
	if math.Abs(run.USD-0.032) > 1e-9 {
		t.Errorf("usd = %v, want 0.032 - both tiers billed", run.USD)
	}
	if run.Metered != 2 {
		t.Errorf("metered = %d, want 2", run.Metered)
	}
}
