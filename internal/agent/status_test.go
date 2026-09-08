package agent

import (
	"os"
	"path/filepath"
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
