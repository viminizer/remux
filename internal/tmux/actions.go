package tmux

import (
	"context"
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

// Bracketed paste markers, as hex byte arguments to send-keys -H.
//
// ESC [ 200 ~ ... ESC [ 201 ~
var (
	pasteStart = []string{"1b", "5b", "32", "30", "30", "7e"}
	pasteEnd   = []string{"1b", "5b", "32", "30", "31", "7e"}
)

// SendText types text into a pane.
//
// Multi-line text is wrapped in bracketed paste. Without it, the newlines in
// the middle of a prompt are read as submissions by Codex and Claude Code, so
// a three-line instruction gets sent as three separate half-finished ones.
// Inside the paste markers the agent inserts a literal newline instead.
//
// Single-line text does not need the wrapper and skips it.
func (c *Client) SendText(ctx context.Context, paneID, text string, submit bool) error {
	if err := CheckPaneID(paneID); err != nil {
		return err
	}
	if text != "" {
		multiline := strings.ContainsAny(text, "\n\r")

		if multiline {
			if err := c.sendHex(ctx, paneID, pasteStart); err != nil {
				return err
			}
		}
		// -l sends the string literally; -- stops text that begins with a
		// dash from being read as a flag.
		if _, err := c.run(ctx, "send-keys", "-t", paneID, "-l", "--", text); err != nil {
			return err
		}
		if multiline {
			if err := c.sendHex(ctx, paneID, pasteEnd); err != nil {
				return err
			}
		}
	}
	if submit {
		return c.SendKeys(ctx, paneID, []string{"Enter"})
	}
	return nil
}

func (c *Client) sendHex(ctx context.Context, paneID string, hex []string) error {
	args := append([]string{"send-keys", "-t", paneID, "-H"}, hex...)
	_, err := c.run(ctx, args...)
	return err
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
// This is the only call in remux that runs select-window and select-pane, and
// it exists because the user explicitly asked for it from the pane menu
// ("Focus on laptop"). It bypasses the forbidden-command guard on purpose.
// Nothing else may call exec with these verbs.
func (c *Client) Focus(ctx context.Context, paneID string) error {
	if err := CheckPaneID(paneID); err != nil {
		return err
	}
	if _, err := c.exec(ctx, "select-window", "-t", paneID); err != nil {
		return err
	}
	_, err := c.exec(ctx, "select-pane", "-t", paneID)
	return err
}

// SessionOf reports which session a pane belongs to. Tests use it to prove a
// destructive target is inside the scratch session before touching it.
func (c *Client) SessionOf(ctx context.Context, paneID string) (string, error) {
	if err := CheckPaneID(paneID); err != nil {
		return "", err
	}
	out, err := c.run(ctx, "display-message", "-p", "-t", paneID, "#{session_name}")
	return strings.TrimSpace(out), err
}
