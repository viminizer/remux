package tmux

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// KeyAllowlist is the complete set of key names remux will forward.
//
// send-keys interprets its arguments as key names, so a raw pass-through would
// let a caller name any key tmux knows, including prefix chords bound to
// destructive commands. The UI only ever needs these.
var KeyAllowlist = map[string]bool{
	"Enter": true, "Escape": true, "Tab": true, "BTab": true,
	"Up": true, "Down": true, "Left": true, "Right": true,
	"Home": true, "End": true, "PageUp": true, "PageDown": true,
	"BSpace": true, "Space": true,
	"C-c": true, "C-d": true, "C-u": true, "C-r": true,
	"C-l": true, "C-a": true, "C-e": true, "M-Enter": true,
	"1": true, "2": true, "3": true, "4": true, "5": true,
	"6": true, "7": true, "8": true, "9": true,
	"y": true, "n": true,
}

// keyAliases maps the names the UI sends to tmux key names.
var keyAliases = map[string]string{
	"Esc":   "Escape",
	"S-Tab": "BTab",
	"Ret":   "Enter",
}

// NormalizeKey resolves an alias and checks the allowlist.
func NormalizeKey(k string) (string, error) {
	if alias, ok := keyAliases[k]; ok {
		k = alias
	}
	if !KeyAllowlist[k] {
		return "", fmt.Errorf("key %q is not allowed", k)
	}
	return k, nil
}

// SendText types text into a pane.
//
// It goes in as a paste, not as keystrokes, because a keystroke is not
// literal. Claude Code's vim mode reads "/compact" as normal-mode commands -
// c is an operator, o aborts it, mp sets a mark, a opens insert - and what
// lands in the composer is "ct". Any modal TUI has its own version of that.
// A paste is inserted as text whatever mode the program is in.
//
// paste-buffer -p is what makes this safe for every send rather than only the
// multi-line ones. tmux emits the bracketed-paste markers only when the
// program has actually asked for them, so a paste-aware TUI gets one literal
// insert - newlines included, instead of a three-line prompt arriving as three
// half-finished submissions - while a program that never asked, like cat in
// canonical mode, still gets plain bytes instead of a stray ESC [ 200 ~. That
// is the part hand-written markers got wrong in both directions.
//
// The buffer is named per pane, so two sends cannot clobber each other, and
// -d drops it once pasted. A named buffer also stays off tmux's numbered
// stack, which on this machine holds real work that must not be renumbered.
func (c *Client) SendText(ctx context.Context, paneID, text string, submit bool) error {
	if err := CheckPaneID(paneID); err != nil {
		return err
	}
	if text != "" {
		// Pane ids are %N, so this is always a safe buffer name.
		buf := "remux_paste_" + strings.TrimPrefix(paneID, "%")
		// -- stops text that begins with a dash from being read as a flag.
		if _, err := c.run(ctx, "set-buffer", "-b", buf, "--", text); err != nil {
			return err
		}
		if _, err := c.run(ctx, "paste-buffer", "-d", "-p", "-b", buf, "-t", paneID); err != nil {
			return err
		}
	}
	if submit {
		return c.SendKeys(ctx, paneID, []string{"Enter"})
	}
	return nil
}

// SendKeys sends allowlisted key names to a pane.
func (c *Client) SendKeys(ctx context.Context, paneID string, keys []string) error {
	if err := CheckPaneID(paneID); err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	args := []string{"send-keys", "-t", paneID, "--"}
	for _, k := range keys {
		norm, err := NormalizeKey(k)
		if err != nil {
			return err
		}
		args = append(args, norm)
	}
	_, err := c.run(ctx, args...)
	return err
}

// Interrupt sends C-c.
func (c *Client) Interrupt(ctx context.Context, paneID string) error {
	return c.SendKeys(ctx, paneID, []string{"C-c"})
}

// ── create ────────────────────────────────────────────────────────────────

// NewSession creates a detached session. -d matters: without it tmux would
// try to attach, which is exactly what must never happen.
func (c *Client) NewSession(ctx context.Context, name, path string) (string, error) {
	args := []string{"new-session", "-d", "-P", "-F", "#{session_id}"}
	if name != "" {
		args = append(args, "-s", name)
	}
	if path != "" {
		args = append(args, "-c", path)
	}
	out, err := c.run(ctx, args...)
	return strings.TrimSpace(out), err
}

// NewWindow creates a window in a session without switching to it.
//
// -d keeps the laptop's active window where it is.
func (c *Client) NewWindow(ctx context.Context, sessionID, name, path string) (string, error) {
	if err := CheckSessionID(sessionID); err != nil {
		return "", err
	}
	args := []string{"new-window", "-d", "-P", "-F", "#{window_id}", "-t", sessionID + ":"}
	if name != "" {
		args = append(args, "-n", name)
	}
	if path != "" {
		args = append(args, "-c", path)
	}
	out, err := c.run(ctx, args...)
	return strings.TrimSpace(out), err
}

// SplitPane splits one pane in two and returns the new pane's id.
//
// A split is a resize of the pane being split - that is what it is - and
// resize-pane and resize-window are forbidden here precisely because changing
// the laptop's geometry is what remux must not do. The difference is that a
// split only touches its target: the other panes in the window keep their
// size. The forbidden list exists to stop remux resizing panes as a side
// effect, by attaching as a client and renegotiating terminal dimensions. A
// split is neither a side effect nor a surprise - it is one named pane,
// changed because someone asked for it.
//
// An agent's pane was exempt from that until #23, which measured the exemption
// at 13 of 17 windows with nothing in them that could be split. It is now
// allowed, behind a press-and-hold on the phone; both Codex and Claude Code
// handle SIGWINCH and redraw.
//
// right splits side by side (tmux -h), otherwise the new pane goes below (-v).
func (c *Client) SplitPane(ctx context.Context, paneID string, right bool) (string, error) {
	if err := CheckPaneID(paneID); err != nil {
		return "", err
	}
	dir := "-v"
	if right {
		dir = "-h"
	}
	// -d leaves the laptop's active pane where it is. Without it tmux makes
	// the new pane active, which moves the cursor on the Mac - the one thing
	// only Focus is allowed to do, and then only when asked.
	out, err := c.run(ctx, "split-window", "-d", dir, "-P", "-F", "#{pane_id}", "-t", paneID)
	return strings.TrimSpace(out), err
}

// ── rename ────────────────────────────────────────────────────────────────

func (c *Client) RenameSession(ctx context.Context, sessionID, name string) error {
	if err := CheckSessionID(sessionID); err != nil {
		return err
	}
	if name == "" {
		return fmt.Errorf("empty name")
	}
	_, err := c.run(ctx, "rename-session", "-t", sessionID, "--", name)
	return err
}

// SetPaneTitle names one pane, for the drawer to show instead of whatever the
// running program calls itself.
//
// It writes the @remux_title pane option rather than the real pane title.
// `select-pane -T` would be the obvious choice and it does not move the
// laptop's cursor - verified against the scratch session - but it loses the
// race: Codex and Claude Code rewrite pane_title on every render, so a name set
// there is gone inside a second. Measured on a pane echoing its own title, the
// rename survived one poll and was overwritten by the next. A user option is
// the only channel the program cannot clobber.
//
// An empty name clears the option and hands the pane back to its own title.
func (c *Client) SetPaneTitle(ctx context.Context, paneID, name string) error {
	return c.setPaneOption(ctx, paneID, "@remux_title", name)
}

// SetPaneTask writes the task title remux read off the screen into
// @remux_task.
//
// It is not @remux_title, and the difference is the point. @remux_title is the
// name Kevin typed from the phone, and the drawer has always let that win over
// anything the machine came up with. A model that overwrote it would take a
// deliberate choice away roughly ninety seconds after he made it. Two options
// keep both, and the reader picks: his name first, this second, the program's
// own pane_title last.
func (c *Client) SetPaneTask(ctx context.Context, paneID, task string) error {
	return c.setPaneOption(ctx, paneID, "@remux_task", task)
}

// SetPaneState writes the agent verdict for one pane into @remux_state, so the
// laptop's own tmux status line can show which panes are blocked.
//
// Same channel and same reasoning as SetPaneTitle: a user option is the only
// place a running agent cannot clobber. It is a separate option rather than a
// prefix on the title because the two move at different speeds - the state
// changes whenever the agent does, the title only when the work does - and
// packing them into one string would mean rewriting the title to change a
// glyph, plus a delimiter for someone to parse back out later.
//
// An empty state clears the option. That is the honest value for a shell and
// for a pane nothing matched: a stale glyph left behind reads as confident and
// is worse than none.
func (c *Client) SetPaneState(ctx context.Context, paneID, state string) error {
	return c.setPaneOption(ctx, paneID, "@remux_state", state)
}

// setPaneOption is the shared write. Both options sit ahead of pane_title in
// treeFormat, so a value carrying the field separator would shift every column
// of the record that reads them back - hence the guard, not just sanitizing
// for tmux's sake.
func (c *Client) setPaneOption(ctx context.Context, paneID, option, value string) error {
	if err := CheckPaneID(paneID); err != nil {
		return err
	}
	if strings.Contains(value, fieldSep) {
		return fmt.Errorf("%s may not contain %q", option, fieldSep)
	}
	if strings.ContainsAny(value, "\n\r\x00") {
		return fmt.Errorf("%s may not contain control characters", option)
	}
	if value == "" {
		_, err := c.run(ctx, "set-option", "-p", "-u", "-t", paneID, option)
		return err
	}
	_, err := c.run(ctx, "set-option", "-p", "-t", paneID, "--", option, value)
	return err
}

func (c *Client) RenameWindow(ctx context.Context, windowID, name string) error {
	if err := CheckWindowID(windowID); err != nil {
		return err
	}
	if name == "" {
		return fmt.Errorf("empty name")
	}
	_, err := c.run(ctx, "rename-window", "-t", windowID, "--", name)
	return err
}

// ── kill ──────────────────────────────────────────────────────────────────

func (c *Client) KillPane(ctx context.Context, paneID string) error {
	if err := CheckPaneID(paneID); err != nil {
		return err
	}
	_, err := c.run(ctx, "kill-pane", "-t", paneID)
	return err
}

func (c *Client) KillWindow(ctx context.Context, windowID string) error {
	if err := CheckWindowID(windowID); err != nil {
		return err
	}
	_, err := c.run(ctx, "kill-window", "-t", windowID)
	return err
}

func (c *Client) KillSession(ctx context.Context, sessionID string) error {
	if err := CheckSessionID(sessionID); err != nil {
		return err
	}
	_, err := c.run(ctx, "kill-session", "-t", sessionID)
	return err
}

// ── focus: the one deliberate exception ───────────────────────────────────

// Focus moves the laptop's own cursor to a pane.
//
// This is the only call in remux that runs select-window, select-pane and
// switch-client, and it exists because the user explicitly asked for it from
// the pane menu ("Focus on laptop"). It bypasses the forbidden-command guard
// on purpose. Nothing else may call exec with these verbs.
//
// switch-client is the one that was missing, and without it the button did
// nothing most of the time. select-window and select-pane move the cursor
// within the pane's own session and say nothing about which session the
// terminal is looking at, so focusing a pane in any other session rearranged
// a session nobody could see and then reported success. On this machine that
// was two sessions out of three.
//
// It goes last so the client arrives already on the right window instead of
// landing on the old one and jumping.
//
// No -c, so tmux picks its most recently used client. That is exactly right
// for one laptop and the only defensible guess for two.
//
// Deliberately not covered by a live test. Every other action here is proved
// against the remux-test scratch session, but switch-client has no scratch
// equivalent: its whole effect is on the one real client, and a test would
// yank a working terminal into the test session.
func (c *Client) Focus(ctx context.Context, paneID string) error {
	if err := CheckPaneID(paneID); err != nil {
		return err
	}
	session, err := c.SessionOf(ctx, paneID)
	if err != nil {
		return err
	}
	// Asked before switching rather than after failing. tmux's own words for
	// this are "no current client", which reaches the phone as a toast and
	// says nothing about the laptop simply not having a terminal open.
	clients, err := c.run(ctx, "list-clients", "-F", "#{client_name}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(clients) == "" {
		return errors.New("no terminal is attached to tmux on the laptop")
	}
	if _, err := c.exec(ctx, "select-window", "-t", paneID); err != nil {
		return err
	}
	if _, err := c.exec(ctx, "select-pane", "-t", paneID); err != nil {
		return err
	}
	_, err = c.exec(ctx, "switch-client", "-t", session)
	return err
}

// SessionOf reports which session a pane belongs to. Tests use it to prove a
// destructive target is inside the scratch session before touching it.
// CommandOf reports the program a pane is currently running.
func (c *Client) CommandOf(ctx context.Context, paneID string) (string, error) {
	if err := CheckPaneID(paneID); err != nil {
		return "", err
	}
	out, err := c.run(ctx, "display-message", "-p", "-t", paneID, "#{pane_current_command}")
	return strings.TrimSpace(out), err
}

func (c *Client) SessionOf(ctx context.Context, paneID string) (string, error) {
	if err := CheckPaneID(paneID); err != nil {
		return "", err
	}
	out, err := c.run(ctx, "display-message", "-p", "-t", paneID, "#{session_name}")
	return strings.TrimSpace(out), err
}
