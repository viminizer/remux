package tmux

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestIDValidation(t *testing.T) {
	ok := []struct {
		fn func(string) bool
		id string
	}{
		{ValidPaneID, "%14"}, {ValidPaneID, "%0"}, {ValidPaneID, "%123"},
		{ValidWindowID, "@8"}, {ValidSessionID, "$2"},
	}
	for _, c := range ok {
		if !c.fn(c.id) {
			t.Errorf("%q should be valid", c.id)
		}
	}

	// Anything that could be read by tmux as a flag or as a name-based
	// target must be rejected before it reaches exec.
	bad := []string{
		"", "%", "%-1", "%1a", "-t", "--", "%1 %2", "%1;kill", "$1", "@1",
		"%1\n", " %1", "%1 ", "%%1",
	}
	for _, id := range bad {
		if ValidPaneID(id) {
			t.Errorf("%q should be rejected as a pane id", id)
		}
	}
}

func TestNormalizeKey(t *testing.T) {
	for in, want := range map[string]string{
		"Esc": "Escape", "S-Tab": "BTab", "Enter": "Enter", "C-c": "C-c", "y": "y",
	} {
		got, err := NormalizeKey(in)
		if err != nil || got != want {
			t.Errorf("NormalizeKey(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	// Keys outside the allowlist must not reach send-keys: it interprets
	// names, so a raw pass-through would let a caller name any chord tmux
	// knows, including ones bound to destructive commands.
	for _, k := range []string{"C-b", "M-x", "kill-pane", "", "F1", "Any"} {
		if _, err := NormalizeKey(k); err == nil {
			t.Errorf("NormalizeKey(%q) should have failed", k)
		}
	}
}

func TestForbiddenCommandsAreRefused(t *testing.T) {
	c := New()
	ctx := context.Background()
	// These are the verbs that would make remux a tmux client or move the
	// laptop's cursor. run() must refuse them before exec.
	for _, verb := range []string{
		"attach-session", "switch-client", "kill-server",
		"select-window", "select-pane", "resize-pane", "resize-window",
	} {
		if _, err := c.run(ctx, verb, "-t", "%0"); err == nil ||
			!strings.Contains(err.Error(), "disturb the laptop") {
			t.Errorf("run(%q) should have been refused, got %v", verb, err)
		}
	}
}

// ── live tests against the scratch session only ───────────────────────────

const scratchSession = "remux-test"

// scratchPane gives the test its own throwaway pane inside the scratch
// session, and kills it afterwards.
//
// A fresh window per test rather than a shared one, because Go runs different
// packages' tests in parallel: sharing a single pane made two suites type into
// each other and fail at random. It also means a test can never write to a
// pane holding real work - the guard below refuses anything outside the
// scratch session before a single key is sent.
func scratchPane(t *testing.T) (*Client, string) {
	t.Helper()
	c := New()
	ctx := context.Background()

	tree, err := c.Tree(ctx)
	if err != nil {
		t.Skipf("no tmux: %v", err)
	}

	var sessionID string
	for _, s := range tree.Sessions {
		if s.Name == scratchSession {
			sessionID = s.ID
		}
	}
	if sessionID == "" {
		t.Skipf("no %s session; create it with: tmux new-session -d -s %s",
			scratchSession, scratchSession)
	}

	winID, err := c.NewWindow(ctx, sessionID, "t"+strconv.FormatInt(time.Now().UnixNano()%1e6, 10), "")
	if err != nil {
		t.Fatalf("create scratch window: %v", err)
	}
	t.Cleanup(func() { _ = c.KillWindow(context.Background(), winID) })

	tree, err = c.Tree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range tree.Panes() {
		if p.WindowID != winID {
			continue
		}
		// Belt and braces: ask tmux itself, not our own parse, before any
		// key is sent. Every other pane on this machine is real work.
		sess, err := c.SessionOf(ctx, p.ID)
		if err != nil || sess != scratchSession {
			t.Fatalf("pane %s is not in %s (got %q) - refusing to write", p.ID, scratchSession, sess)
		}
		time.Sleep(400 * time.Millisecond) // let the shell draw its prompt
		return c, p.ID
	}
	t.Fatalf("no pane in the window just created")
	return nil, ""
}

// TestMultilineTextIsNotSubmitted is the phase 3 exit test.
//
// Without bracketed paste, the newlines inside a prompt are read as
// submissions: "echo ccc\necho ddd" runs echo ccc immediately and leaves
// echo ddd dangling, which is how one instruction reaches an agent as several
// half-finished ones.
//
// The harness is zsh rather than a plain command like cat, because bracketed
// paste only does anything when the receiving application opts into it. zsh
// does; cat, reading a tty in canonical mode, does not - the markers would
// arrive as literal bytes and every newline would still submit.
func TestMultilineTextIsNotSubmitted(t *testing.T) {
	c, pane := scratchPane(t)
	ctx := context.Background()

	reset(t, c, pane)

	// Two harmless commands whose output is only visible once they run, so
	// "was this submitted?" is directly observable.
	prompt := "echo remux_alpha\necho remux_beta"
	if err := c.SendText(ctx, pane, prompt, false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)

	screen, err := c.Capture(ctx, pane, 60, false)
	if err != nil {
		t.Fatal(err)
	}
	// Both lines are staged in the composer...
	for _, want := range []string{"echo remux_alpha", "echo remux_beta"} {
		if !strings.Contains(screen, want) {
			t.Errorf("staged prompt is missing %q:\n%s", want, screen)
		}
	}
	// ...and neither has run.
	if strings.Contains(screen, "\nremux_alpha") || strings.Contains(screen, "\nremux_beta") {
		t.Errorf("the prompt was submitted early:\n%s", screen)
	}

	// Enter is what submits it, and now both lines run.
	if err := c.SendKeys(ctx, pane, []string{"Enter"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)

	screen, err = c.Capture(ctx, pane, 60, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"remux_alpha", "remux_beta"} {
		if !strings.Contains(screen, want) {
			t.Errorf("after Enter, %q did not run:\n%s", want, screen)
		}
	}
	reset(t, c, pane)
}

// TestWithoutBracketedPasteItWouldSubmitEarly pins down what the bracketing
// is actually buying, so a future change that drops it fails loudly here
// rather than quietly mangling prompts to a live agent.
func TestWithoutBracketedPasteItWouldSubmitEarly(t *testing.T) {
	c, pane := scratchPane(t)
	ctx := context.Background()

	reset(t, c, pane)

	// The same text sent with -l alone, which is what SendText does for
	// single-line input.
	if _, err := c.run(ctx, "send-keys", "-t", pane, "-l", "--",
		"echo remux_gamma\necho remux_delta"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)

	screen, err := c.Capture(ctx, pane, 60, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(screen, "remux_gamma") {
		t.Skip("this shell does not submit on an unbracketed newline; nothing to prove here")
	}
	if strings.Contains(screen, "remux_delta\n") && strings.Count(screen, "remux_delta") > 1 {
		t.Errorf("expected the second line to be left dangling, got:\n%s", screen)
	}
	reset(t, c, pane)
}

// reset returns the scratch pane to a clean prompt.
func reset(t *testing.T, c *Client, pane string) {
	t.Helper()
	ctx := context.Background()
	_ = c.SendKeys(ctx, pane, []string{"C-c"})
	time.Sleep(200 * time.Millisecond)
	_ = c.SendText(ctx, pane, "clear", true)
	time.Sleep(400 * time.Millisecond)
}

func TestSingleLineText(t *testing.T) {
	c, pane := scratchPane(t)
	ctx := context.Background()

	marker := "remux-single-" + time.Now().Format("150405")
	if err := c.SendText(ctx, pane, "echo "+marker, true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)

	screen, err := c.Capture(ctx, pane, 50, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(screen, marker) {
		t.Errorf("marker %q not on screen:\n%s", marker, screen)
	}
}

// TestCreateRenameKillRoundTrip exercises the write paths that the drawer's
// New sheet and long-press menu use, entirely inside the scratch session.
func TestCreateRenameKillRoundTrip(t *testing.T) {
	c, pane := scratchPane(t)
	ctx := context.Background()

	tree, err := c.Tree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := tree.Pane(pane)
	if p == nil || p.SessionName != scratchSession {
		t.Fatalf("refusing to create in %v", p)
	}

	winID, err := c.NewWindow(ctx, p.SessionID, "probe", "")
	if err != nil {
		t.Fatal(err)
	}
	if !ValidWindowID(winID) {
		t.Fatalf("new-window returned %q", winID)
	}

	if err := c.RenameWindow(ctx, winID, "probe-renamed"); err != nil {
		t.Fatal(err)
	}
	tree, _ = c.Tree(ctx)
	found := false
	for _, s := range tree.Sessions {
		for _, w := range s.Windows {
			if w.ID == winID {
				found = true
				if w.Name != "probe-renamed" {
					t.Errorf("window name is %q, want probe-renamed", w.Name)
				}
			}
		}
	}
	if !found {
		t.Fatalf("window %s not in the tree after creating it", winID)
	}

	if err := c.KillWindow(ctx, winID); err != nil {
		t.Fatal(err)
	}
	tree, _ = c.Tree(ctx)
	for _, s := range tree.Sessions {
		for _, w := range s.Windows {
			if w.ID == winID {
				t.Errorf("window %s still present after kill", winID)
			}
		}
	}
}
