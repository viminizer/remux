// Package tmux talks to the local tmux server with targeted, clientless
// commands only.
//
// The hard constraint of the whole product lives here: remux must never become
// a tmux client. Attaching (attach-session, -CC control mode) adds a client
// whose terminal size renegotiates pane dimensions, which would resize the
// layout on the laptop. select-window/select-pane/switch-client would move the
// cursor out from under whoever is sitting at the machine.
//
// Everything below is therefore expressed as -t targeted commands, and new
// sessions and windows are created with -d so the active window never jumps.
// The single deliberate exception is Focus(), which the UI labels
// "Focus on laptop" and the user has to ask for by name.
package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// ErrNoServer is returned when no tmux server is running. It is a normal
// state (an empty workspace), not a failure.
var ErrNoServer = errors.New("tmux: no server running")

// forbidden commands that would make us a client or move the laptop's cursor.
// Focus() is allowed to bypass this via focusEscapeHatch; nothing else is.
var forbidden = map[string]bool{
	"attach-session": true, "attach": true,
	"switch-client": true, "switchc": true,
	"kill-server":   true,
	"select-window": true, "selectw": true,
	"select-pane": true, "selectp": true,
	"resize-pane": true, "resizep": true,
	"resize-window": true, "resizew": true,
	"source-file": true, "source": true,
	"-CC": true, "-C": true,
}

// Client runs tmux commands.
type Client struct {
	Bin     string        // tmux binary, defaults to "tmux"
	Timeout time.Duration // per-command timeout
}

func New() *Client { return &Client{Bin: "tmux", Timeout: 5 * time.Second} }

func (c *Client) bin() string {
	if c.Bin == "" {
		return "tmux"
	}
	return c.Bin
}

func (c *Client) timeout() time.Duration {
	if c.Timeout == 0 {
		return 5 * time.Second
	}
	return c.Timeout
}

// run executes a tmux command after checking it against the forbidden list.
func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	for _, a := range args {
		if forbidden[a] {
			return "", fmt.Errorf("tmux: refusing to run %q: it would disturb the laptop", a)
		}
	}
	return c.exec(ctx, args...)
}

// exec runs without the forbidden check. Only focus.go may call it directly.
func (c *Client) exec(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()

	cmd := exec.CommandContext(ctx, c.bin(), args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if strings.Contains(msg, "no server running") || strings.Contains(msg, "no current session") {
			return "", ErrNoServer
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("tmux %s: %s", strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

// Version reports the tmux server version, e.g. "3.5a".
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := c.exec(ctx, "-V")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "tmux ")), nil
}

// ── ID validation ─────────────────────────────────────────────────────────
//
// os/exec never goes through a shell, so there is no injection risk in the
// usual sense. IDs are validated anyway so that a caller-supplied string can
// never be interpreted by tmux itself as a flag or as a name-based target
// (which could match a pane other than the one intended).

var (
	sessionRe = regexp.MustCompile(`^\$\d+$`)
	windowRe  = regexp.MustCompile(`^@\d+$`)
	paneRe    = regexp.MustCompile(`^%\d+$`)
)

func ValidSessionID(s string) bool { return sessionRe.MatchString(s) }
func ValidWindowID(s string) bool  { return windowRe.MatchString(s) }
func ValidPaneID(s string) bool    { return paneRe.MatchString(s) }

func CheckSessionID(s string) error {
	if !ValidSessionID(s) {
		return fmt.Errorf("invalid session id %q (want $N)", s)
	}
	return nil
}

func CheckWindowID(s string) error {
	if !ValidWindowID(s) {
		return fmt.Errorf("invalid window id %q (want @N)", s)
	}
	return nil
}

func CheckPaneID(s string) error {
	if !ValidPaneID(s) {
		return fmt.Errorf("invalid pane id %q (want %%N)", s)
	}
	return nil
}
