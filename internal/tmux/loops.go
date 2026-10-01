package tmux

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// LoopPrefix marks the sessions the agent harness owns.
//
// A loop gets a detached session of its own rather than a pane in one of
// Kevin's: a new pane means split-window in a real session, which resizes the
// panes he is working in. A session nobody is attached to has no client, so
// creating it, writing options on it and killing it touches nothing of his.
// Every write below refuses any session without this prefix.
const LoopPrefix = "loop-"

var loopNameRe = regexp.MustCompile(`^loop-[a-z0-9][a-z0-9-]{0,60}$`)

// ValidLoopName reports whether name is a session the harness may write to.
func ValidLoopName(name string) bool { return loopNameRe.MatchString(name) }

// Loop is one harness loop as its session options describe it.
type Loop struct {
	Name         string `json:"name"`
	Pane         string `json:"pane"`
	Role         string `json:"role"`  // build or review
	Agent        string `json:"agent"` // claude or codex
	Repo         string `json:"repo"`  // local clone path
	Slug         string `json:"slug"`  // owner/name
	State        string `json:"state"` // idle, working, triaging, blocked
	Since        int64  `json:"since"` // unix seconds the state began
	Issue        int    `json:"issue"`
	Title        string `json:"title"`
	Scope        string `json:"scope"`
	Instructions string `json:"instructions"`
	InstrMode    string `json:"instrMode"` // add or replace
	Stop         string `json:"stop"`      // "after" once a stop is queued
	Note         string `json:"note"`
	Event        string `json:"-"`
}

// Options that hold free text from the phone or an error message. They are
// base64 so a newline cannot split the one-line-per-session listing below.
var encoded = map[string]bool{"@loop_instr": true, "@loop_note": true, "@loop_title": true}

var loopFields = []string{
	"#{session_name}", "#{pane_id}", "#{@loop_role}", "#{@loop_agent}", "#{@loop_repo}",
	"#{@loop_slug}", "#{@loop_state}", "#{@loop_since}", "#{@loop_issue}", "#{@loop_title}",
	"#{@loop_scope}", "#{@loop_instr}", "#{@loop_mode}", "#{@loop_stop}", "#{@loop_note}",
	"#{@loop_event}",
}

// Loops lists every harness session in one tmux call.
func (c *Client) Loops(ctx context.Context) ([]Loop, error) {
	out, err := c.run(ctx, "list-sessions", "-F", strings.Join(loopFields, fieldSep))
	if err == ErrNoServer {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var loops []Loop
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if l, ok := parseLoop(line); ok {
			loops = append(loops, l)
		}
	}
	return loops, nil
}

func parseLoop(line string) (Loop, bool) {
	f := strings.Split(line, fieldSep)
	if len(f) != len(loopFields) || !strings.HasPrefix(f[0], LoopPrefix) {
		return Loop{}, false
	}
	since, _ := strconv.ParseInt(f[7], 10, 64)
	issue, _ := strconv.Atoi(f[8])
	return Loop{
		Name: f[0], Pane: f[1], Role: f[2], Agent: f[3], Repo: f[4], Slug: f[5],
		State: f[6], Since: since, Issue: issue, Title: decode(f[9]), Scope: f[10],
		Instructions: decode(f[11]), InstrMode: f[12], Stop: f[13], Note: decode(f[14]),
		Event: f[15],
	}, true
}

// NewLoopSession starts argv in a new detached session. -d is what keeps it
// from attaching, exactly as in NewSession.
func (c *Client) NewLoopSession(ctx context.Context, name, dir string, argv []string) error {
	if !ValidLoopName(name) {
		return fmt.Errorf("tmux: %q is not a loop session name", name)
	}
	args := append([]string{"new-session", "-d", "-s", name, "-c", dir}, argv...)
	_, err := c.run(ctx, args...)
	return err
}

// SetLoopOption writes one @loop_* option on a loop session. Empty unsets it.
func (c *Client) SetLoopOption(ctx context.Context, name, option, value string) error {
	if !ValidLoopName(name) {
		return fmt.Errorf("tmux: %q is not a loop session name", name)
	}
	if !strings.HasPrefix(option, "@loop_") {
		return fmt.Errorf("tmux: %q is not a loop option", option)
	}
	if encoded[option] && value != "" {
		value = base64.StdEncoding.EncodeToString([]byte(value))
	}
	if strings.Contains(value, fieldSep) || strings.ContainsAny(value, "\n\r\x00") {
		return fmt.Errorf("tmux: %s carries a separator or control character", option)
	}
	// "=" makes the target an exact session name, not a prefix match: without
	// it, "loop-a" would also match a session called "loop-ab".
	if value == "" {
		_, err := c.run(ctx, "set-option", "-u", "-t", "="+name+":", option)
		return err
	}
	_, err := c.run(ctx, "set-option", "-t", "="+name+":", "--", option, value)
	return err
}

// LoopOption reads one @loop_* option back.
func (c *Client) LoopOption(ctx context.Context, name, option string) (string, error) {
	if !ValidLoopName(name) {
		return "", fmt.Errorf("tmux: %q is not a loop session name", name)
	}
	out, err := c.run(ctx, "display-message", "-p", "-t", "="+name+":", "#{"+option+"}")
	if err != nil {
		return "", err
	}
	v := strings.TrimRight(out, "\n")
	if encoded[option] {
		v = decode(v)
	}
	return v, nil
}

// KillLoop ends a loop session and the agent running in it.
func (c *Client) KillLoop(ctx context.Context, name string) error {
	if !ValidLoopName(name) {
		return fmt.Errorf("tmux: %q is not a loop session name", name)
	}
	_, err := c.run(ctx, "kill-session", "-t", "="+name)
	return err
}

func decode(s string) string {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return s
	}
	return string(b)
}
