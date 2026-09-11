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

	// The same text sent with -l alone, which is what SendText used to do
	// before it switched to paste-buffer.
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

// TestPasteMarkersDoNotLeakToPlainPrograms is the other half of bracketing.
//
// SendText pastes everything now, not just multi-line text, because a literal
// keystroke is not literal to a modal TUI - Claude Code's vim mode turns
// "/compact" into "ct". The risk that buys is the opposite one: a program that
// never asked for bracketed paste would see the markers as input.
//
// paste-buffer -p is what resolves it, and this pins that down. cat reads a
// tty in canonical mode and never requests bracketed paste, so it must receive
// the bytes and nothing else.
func TestPasteMarkersDoNotLeakToPlainPrograms(t *testing.T) {
	c, pane := scratchPane(t)
	ctx := context.Background()

	reset(t, c, pane)

	// cat echoes its input back, so the screen shows exactly what arrived.
	if err := c.SendText(ctx, pane, "cat", true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)

	if err := c.SendText(ctx, pane, "/compact", false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)

	screen, err := c.Capture(ctx, pane, 60, false)
	if err != nil {
		t.Fatal(err)
	}
	// C-c before any assertion, so a failure cannot leave cat holding the pane.
	_ = c.SendKeys(ctx, pane, []string{"C-c"})
	time.Sleep(200 * time.Millisecond)

	if !strings.Contains(screen, "/compact") {
		t.Errorf("text did not arrive intact:\n%s", screen)
	}
	for _, marker := range []string{"[200~", "[201~"} {
		if strings.Contains(screen, marker) {
			t.Errorf("bracketed paste marker %q leaked to a plain program:\n%s", marker, screen)
		}
	}
	reset(t, c, pane)
}

// TestSplitPane covers the pane creation path added for issue #16.
//
// The safety rule - never split a pane running an agent - lives in the API
// layer, so what matters here is narrower and mechanical: the split lands in
// the scratch session, produces a second pane in that window, and leaves the
// laptop's active pane alone. That last part is what -d buys, and it is the
// reason this is allowed to exist beside the forbidden resize commands.
func TestSplitPane(t *testing.T) {
	c, pane := scratchPane(t)
	ctx := context.Background()

	before, err := c.SessionOf(ctx, pane)
	if err != nil {
		t.Fatal(err)
	}
	if before != scratchSession {
		t.Fatalf("refusing to split %s: it is in %q, not %s", pane, before, scratchSession)
	}

	newID, err := c.SplitPane(ctx, pane, true)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidPaneID(newID) {
		t.Fatalf("split returned %q, want a %%N pane id", newID)
	}

	// The new pane has to be in the scratch session too, or something targeted
	// the wrong window entirely.
	if sess, err := c.SessionOf(ctx, newID); err != nil || sess != scratchSession {
		t.Fatalf("new pane %s landed in %q (err %v), not %s", newID, sess, err, scratchSession)
	}

	tree, err := c.Tree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var winID string
	count := 0
	for _, p := range tree.Panes() {
		if p.ID == pane {
			winID = p.WindowID
		}
	}
	for _, p := range tree.Panes() {
		if p.WindowID == winID {
			count++
		}
	}
	if count != 2 {
		t.Errorf("window holds %d panes after one split, want 2", count)
	}

	// -d means the split must not have stolen the active pane.
	for _, p := range tree.Panes() {
		if p.ID == newID && p.Active {
			t.Errorf("the new pane %s was made active; -d should have prevented that", newID)
		}
	}

	if err := c.KillPane(ctx, newID); err != nil {
		t.Fatal(err)
	}
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

// ── format parsing ────────────────────────────────────────────────────────

// buildLine assembles a record the way treeFormat would.
func buildLine(title string) string { return buildLineWith("", title) }

// buildLineWith takes both names: @remux_title, then pane_title last.
func buildLineWith(remuxTitle, title string) string {
	return buildLineFull(remuxTitle, "", "", title)
}

// buildLineFull adds @remux_task and @remux_state, which sit between the two.
func buildLineFull(remuxTitle, remuxTask, remuxState, title string) string {
	f := []string{
		"$2", "saas", "1",
		"@8", "8", "issue168", "1", "1788946490",
		"%13", "0", "2.1.263",
		"/Users/mac/dev", "1", "213", "54",
		"0", "0", "0", "35713",
		remuxTitle,
		remuxTask,
		remuxState,
		title,
	}
	return strings.Join(f, fieldSep)
}

func TestParseTreeLine(t *testing.T) {
	s, w, p, ok := parseTreeLine(buildLine("✳ Separate worktree"))
	if !ok {
		t.Fatal("did not parse")
	}
	if s.ID != "$2" || s.Name != "saas" || !s.Attached {
		t.Errorf("session: %+v", s)
	}
	if w.ID != "@8" || w.Index != 8 || w.Name != "issue168" {
		t.Errorf("window: %+v", w)
	}
	if p.ID != "%13" || p.Command != "2.1.263" || p.Title != "✳ Separate worktree" {
		t.Errorf("pane: %+v", p)
	}
	if p.RemuxTitle != "" {
		t.Errorf("unnamed pane got RemuxTitle %q", p.RemuxTitle)
	}
	if p.Width != 213 || p.Height != 54 || p.History != 35713 {
		t.Errorf("pane geometry: %+v", p)
	}
	if p.Activity != 1788946490 {
		t.Errorf("window activity = %d", p.Activity)
	}
}

// pane_title is last and parsed with SplitN, so an agent writing the separator
// into its title shifts nothing.
func TestSeparatorInsidePaneTitleIsKept(t *testing.T) {
	title := "weird" + fieldSep + "title"
	_, _, p, ok := parseTreeLine(buildLine(title))
	if !ok {
		t.Fatal("did not parse")
	}
	if p.Title != title {
		t.Errorf("title = %q, want %q", p.Title, title)
	}
	if p.Command != "2.1.263" {
		t.Errorf("command shifted to %q", p.Command)
	}
}

// The separator must not be a tab. tmux replaces tabs in format output with
// "_" when the command has no controlling terminal, which is exactly how remux
// runs as a LaunchAgent - so a tab works in every interactive test and
// collapses every record into one field in production.
func TestRemuxTitleIsItsOwnColumn(t *testing.T) {
	_, _, p, ok := parseTreeLine(buildLineWith("my name", "✳ agent output"))
	if !ok {
		t.Fatal("did not parse")
	}
	if p.RemuxTitle != "my name" {
		t.Errorf("RemuxTitle = %q, want %q", p.RemuxTitle, "my name")
	}
	if p.Title != "✳ agent output" {
		t.Errorf("Title = %q, want the program's own title", p.Title)
	}
	if p.History != 35713 {
		t.Errorf("columns before the new field shifted: History = %d", p.History)
	}
}

func TestSeparatorIsNotWhitespaceOrControl(t *testing.T) {
	for _, r := range fieldSep {
		if r < 0x21 || r > 0x7e {
			t.Fatalf("separator %q contains %q, which tmux will escape", fieldSep, r)
		}
	}
	// A bare "|" appears in Claude Code's own status line.
	if fieldSep == "|" {
		t.Error(`separator "|" occurs in real pane titles`)
	}
}

func TestMalformedLineIsRejected(t *testing.T) {
	for _, line := range []string{
		"",
		"not a record",
		// What a tab separator produced under launchd: one field.
		"$2_saas_1_@8_8_issue168_1_%13_0_2.1.263",
		strings.Join([]string{"$2", "saas", "1"}, fieldSep),
	} {
		if _, _, _, ok := parseTreeLine(line); ok {
			t.Errorf("parsed a malformed line: %q", line)
		}
	}
}

// TestPaneTitleSurvivesTheProgramRewritingIts Own is the reason a pane name
// lives in @remux_title and not in pane_title.
//
// `select-pane -T` is the obvious way to name a pane, and it is safe - it sets
// the title without moving the laptop's active pane. It is still wrong here:
// pane_title belongs to the running program, and Codex and Claude Code rewrite
// it on every render. 26 of the 28 panes on this machine set their own title,
// so a name written there would visibly revert within a second on exactly the
// panes worth naming.
func TestPaneTitleSurvivesTheProgramRewritingItsOwn(t *testing.T) {
	c, pane := scratchPane(t)
	ctx := context.Background()

	// A stand-in for an agent: reprints its own title once a second.
	if err := c.SendText(ctx, pane, `while true; do printf '\033]2;program-owns-this\007'; sleep 1; done`, true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)

	if err := c.SetPaneTitle(ctx, pane, "user-owns-this"); err != nil {
		t.Fatal(err)
	}

	// Long enough for the program to have rewritten pane_title several times.
	time.Sleep(2500 * time.Millisecond)

	p := paneByID(t, c, pane)
	if p.RemuxTitle != "user-owns-this" {
		t.Errorf("@remux_title = %q, want %q - the program clobbered the user's name",
			p.RemuxTitle, "user-owns-this")
	}
	if p.Title != "program-owns-this" {
		t.Errorf("pane_title = %q, want the program's own title; the fixture is not "+
			"reproducing the race this test exists for", p.Title)
	}

	// An empty name hands the pane back to the program.
	if err := c.SetPaneTitle(ctx, pane, ""); err != nil {
		t.Fatal(err)
	}
	if got := paneByID(t, c, pane).RemuxTitle; got != "" {
		t.Errorf("after clearing, @remux_title = %q, want empty", got)
	}
}

func TestSetPaneTitleRejectsTheFieldSeparator(t *testing.T) {
	c := New()
	// @remux_title sits before pane_title in treeFormat, so a separator inside
	// it would shift the title column. It is refused rather than escaped.
	err := c.SetPaneTitle(context.Background(), "%1", "a"+fieldSep+"b")
	if err == nil {
		t.Fatal("expected a name containing the field separator to be refused")
	}
	if err := c.SetPaneTitle(context.Background(), "%1", "two\nlines"); err == nil {
		t.Fatal("expected a name containing a newline to be refused")
	}
}

// TestSetPaneStateRoundTrips proves the state lands in its own option and
// comes back through the tree without disturbing the name beside it.
//
// The two share a record and sit next to each other ahead of pane_title, so
// the failure this guards against is a silent column shift, not a lost write:
// a state written into the title's slot would read back as a title and the
// tree would still parse.
func TestSetPaneStateRoundTrips(t *testing.T) {
	c, pane := scratchPane(t)
	ctx := context.Background()

	if err := c.SetPaneTitle(ctx, pane, "venue filter"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPaneTask(ctx, pane, "token refresh race"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPaneState(ctx, pane, "!"); err != nil {
		t.Fatal(err)
	}

	p := paneByID(t, c, pane)
	if p.RemuxState != "!" {
		t.Errorf("@remux_state = %q, want %q", p.RemuxState, "!")
	}
	if p.RemuxTask != "token refresh race" {
		t.Errorf("@remux_task = %q, want %q", p.RemuxTask, "token refresh race")
	}
	if p.RemuxTitle != "venue filter" {
		t.Errorf("@remux_title = %q, want %q - the three options collided",
			p.RemuxTitle, "venue filter")
	}

	// Clearing is how a shell, and a pane nothing matched, get their state
	// back. A glyph left behind would be a confident lie.
	if err := c.SetPaneState(ctx, pane, ""); err != nil {
		t.Fatal(err)
	}
	p = paneByID(t, c, pane)
	if p.RemuxState != "" {
		t.Errorf("after clearing, @remux_state = %q, want empty", p.RemuxState)
	}
	if p.RemuxTitle != "venue filter" {
		t.Errorf("clearing the state cleared the title too: %q", p.RemuxTitle)
	}
	if p.RemuxTask != "token refresh race" {
		t.Errorf("clearing the state cleared the task too: %q", p.RemuxTask)
	}
}

func TestSetPaneStateRejectsTheFieldSeparator(t *testing.T) {
	c := New()
	if err := c.SetPaneState(context.Background(), "%1", "a"+fieldSep+"b"); err == nil {
		t.Fatal("expected a state containing the field separator to be refused")
	}
	if err := c.SetPaneState(context.Background(), "%1", "two\nlines"); err == nil {
		t.Fatal("expected a state containing a newline to be refused")
	}
}

func paneByID(t *testing.T, c *Client, id string) *Pane {
	t.Helper()
	tree, err := c.Tree(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p := tree.Pane(id)
	if p == nil {
		t.Fatalf("pane %s vanished", id)
	}
	return p
}
