package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fixtures are real captures from this machine's workspace, except the two
// scratch-session ones noted below, which were produced by printing the exact
// strings the agents use.
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

func TestClassifyFixtures(t *testing.T) {
	cases := []struct {
		file string
		cmd  string
		want Status
	}{
		{"codex-idle.txt", "codex", Idle},
		{"codex-idle-recap.txt", "codex", Idle},
		{"claude-idle.txt", "2.1.263", Idle},
		{"shell-zsh.txt", "zsh", Shell},
		{"waiting-choice.txt", "codex", Waiting},
		{"busy-working.txt", "codex", Busy},
		{"claude-busy-long.txt", "2.1.263", Busy},
	}
	for _, c := range cases {
		got := Classify(c.cmd, "", fixture(t, c.file))
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.file, got, c.want)
		}
	}
}

func TestWaitingBeatsBusy(t *testing.T) {
	// An agent asking a question often still shows a timer from the work it
	// just finished. The question has to win.
	screen := "Working (12s · esc to interrupt)\nDo you want to apply this patch?\n❯ 1. Yes"
	if got := Classify("codex", "", screen); got != Waiting {
		t.Errorf("got %q, want waiting", got)
	}
}

func TestBusyBeatsIdle(t *testing.T) {
	// The mirror of TestWaitingBeatsBusy, and the case that was missing.
	// Claude Code's footer chrome - the empty prompt, the mode line - is on
	// screen the entire time it works, so every busy screen is also an idle
	// screen by pattern count. Busy has to win.
	screen := strings.Join([]string{
		"✢ Manifesting… (1m 5s · ↓ 4.0k tokens)",
		"──────────────────────────────────────",
		"❯ ",
		"──────────────────────────────────────",
		"  -- INSERT -- ⏵⏵ bypass permissions on (shift+tab to cycle)",
	}, "\n")
	if got := Classify("2.1.263", "", screen); got != Busy {
		t.Errorf("got %q, want busy", got)
	}
}

func TestLongRunningTimerIsBusy(t *testing.T) {
	// The regression this all came from: the timer pattern required digits
	// immediately followed by "s", so it matched the first minute of a task
	// and nothing after it. A task that runs for an hour is exactly the one
	// you walk away from, and "busy -> idle" is what fires the "finished"
	// push.
	for _, timer := range []string{"(45s ·", "(1m 5s ·", "(12m 3s ·", "(1h 2m 3s ·"} {
		screen := "Effecting… " + timer + " ↓ 4.0k tokens)\n❯ \n  -- INSERT -- (shift+tab to cycle)"
		if got := Classify("2.1.263", "", screen); got != Busy {
			t.Errorf("timer %q: got %q, want busy", timer, got)
		}
	}
}

func TestRandomisedSpinnerWordIsBusy(t *testing.T) {
	// Claude Code invents the gerund on every render, so a fixed word list
	// cannot keep up. These four are real, observed on this machine.
	for _, word := range []string{"Manifesting", "Effecting", "Wandering", "Pondering"} {
		screen := "✢ " + word + "…\n❯ \n  -- INSERT -- (shift+tab to cycle)"
		if got := Classify("2.1.263", "", screen); got != Busy {
			t.Errorf("%s: got %q, want busy", word, got)
		}
	}
}

func TestIdleNeedsTheAbsenceOfActivity(t *testing.T) {
	// Idle is not "the idle markers are present" - they always are. It is
	// "they are present and nothing else is".
	idleOnly := "❯ \n  -- INSERT -- ⏵⏵ bypass permissions on (shift+tab to cycle)"
	if got := Classify("2.1.263", "", idleOnly); got != Idle {
		t.Errorf("idle screen: got %q, want idle", got)
	}
	if got := Classify("2.1.263", "", "✻ Thinking… (3s ·\n"+idleOnly); got != Busy {
		t.Errorf("same screen plus a spinner: got %q, want busy", got)
	}
}

func TestProseAboutAQuotedStringIsNotIdle(t *testing.T) {
	// The dropped `try "..."` pattern matched any prose containing a quoted
	// string, which is common in code discussion and was carrying an idle
	// verdict on its own.
	screen := `The old pattern was try "[^"]+". Roughly 15 lines to replace it.`
	if got := Classify("2.1.263", "", screen); got == Idle {
		t.Errorf("prose classified as idle")
	}
}

func TestShellWinsOverScreenContent(t *testing.T) {
	// A shell that happens to have an agent's output scrolled up must not be
	// reported as a working agent.
	screen := "esc to interrupt\n$ "
	if got := Classify("zsh", "", screen); got != Shell {
		t.Errorf("got %q, want shell", got)
	}
}

func TestTrailingBlankLinesDoNotHideState(t *testing.T) {
	screen := "Do you want to continue?\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n"
	if got := Classify("codex", "", screen); got != Waiting {
		t.Errorf("got %q, want waiting", got)
	}
}

func TestStaleQuestionScrolledOffIsNotWaiting(t *testing.T) {
	// The question was answered 40 lines ago. Only the tail counts.
	screen := "Do you want to apply this patch?\n"
	for i := 0; i < 40; i++ {
		screen += "some later output line\n"
	}
	screen += "› Ask Codex to do anything\n"
	if got := Classify("codex", "", screen); got != Idle {
		t.Errorf("got %q, want idle", got)
	}
}

func TestStripANSI(t *testing.T) {
	cases := []struct{ in, want string }{
		{"\x1b[32mgreen\x1b[0m", "green"},
		{"\x1b[38;2;255;0;0mtruecolor\x1b[m", "truecolor"},
		{"\x1b]8;;https://example.com\x07link\x1b]8;;\x07", "link"},
		{"plain", "plain"},
	}
	for _, c := range cases {
		if got := StripANSI(c.in); got != c.want {
			t.Errorf("StripANSI(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsAgent(t *testing.T) {
	for _, cmd := range []string{"codex", "claude", "2.1.263"} {
		if !IsAgent(cmd) {
			t.Errorf("IsAgent(%q) = false", cmd)
		}
	}
	for _, cmd := range []string{"zsh", "vim", "node", "2.1"} {
		if IsAgent(cmd) {
			t.Errorf("IsAgent(%q) = true", cmd)
		}
	}
}

// Regression: a real Claude Code pane on this machine was classified waiting
// because a paragraph contained "6390. No collision." A number followed by a
// sentence starting with "No" is not a choice menu.
func TestProseNumberIsNotAMenu(t *testing.T) {
	screen := "Redis on 56391, with their own volumes. Mine used shortlist_live231* on\n" +
		"6390. No collision.\n\n❯ \n"
	if got := Classify("2.1.263", "", screen); got == Waiting {
		t.Errorf("got waiting for prose containing a numbered sentence")
	}
}

// A real menu has more than one option, and single-digit numbers.
func TestRealNumberedMenuIsWaiting(t *testing.T) {
	screen := "Apply patch to auth.service.ts?\n\n" +
		"  1. Yes, apply\n" +
		"  2. Yes, and don't ask again this session\n" +
		"  3. No, tell Codex what to do differently\n"
	if got := Classify("codex", "", screen); got != Waiting {
		t.Errorf("got %q, want waiting", got)
	}
}

// One numbered line is not enough on its own.
func TestSingleNumberedLineIsNotAMenu(t *testing.T) {
	screen := "I found three problems.\n  1. No tests cover the refund path\n\n› Ask Codex to do anything\n"
	if got := Classify("codex", "", screen); got == Waiting {
		t.Errorf("got waiting for a single numbered prose line")
	}
}
