package titler

import (
	"context"
	"errors"
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

	mu      sync.Mutex
	calls   int
	prompts []string
}

func (r *fakeRunner) Name() string { return r.label }

func (r *fakeRunner) Run(_ context.Context, prompt string) (string, error) {
	r.mu.Lock()
	r.calls++
	r.prompts = append(r.prompts, prompt)
	r.mu.Unlock()
	return r.out, r.err
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
	p := New(f)
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
	p := New(f)
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
	p := New(f)
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

	p := New(f)
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

	p := New(f)
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

	p := New(f)
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
	p := New(f)
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
	p := New(f)
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
// own screen, and the name it already has for the SAME check.
func TestPromptCarriesScreenAndCurrentName(t *testing.T) {
	p := buildPrompt([]job{
		{ID: "%1", Dir: "/Users/mac/dev/shortlist", Task: "venue filter", Screen: "\x1b[31mworking on auth\x1b[0m"},
	})
	for _, want := range []string{
		"## Pane 1",
		"/Users/mac/dev/shortlist",
		"current name: venue filter",
		"working on auth",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "\x1b[") {
		t.Error("prompt still carries ANSI escapes; that is tokens spent on colour codes")
	}
}
