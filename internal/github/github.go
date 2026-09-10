// Package github reads GitHub through the gh CLI.
//
// The transport choice is the whole design. gh is already logged in on this
// Mac, in the system keyring, with the scopes Kevin granted it. Shelling out
// to it means remux never stores a token, never runs an OAuth flow, and never
// puts a credential on the phone - the phone talks to remux over the tailnet,
// remux talks to GitHub as Kevin's laptop. Losing the phone leaks nothing.
//
// This mirrors internal/tmux: a thin exec wrapper, validated arguments, and
// typed errors for the states the UI has to render differently.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// The states the UI draws differently. Everything else is a plain error and
// shows as the generic failure row.
var (
	// ErrNoAuth means gh has no usable credential: not logged in, or the
	// token was revoked. The screen tells Kevin to run `gh auth login` at
	// the laptop, because that is the only place it can be fixed.
	ErrNoAuth = errors.New("github: gh is not logged in")
	// ErrOffline means the request never reached github.com. The cached
	// payload stays on screen behind the stale banner.
	ErrOffline = errors.New("github: cannot reach github.com")
	// ErrNotFound covers a repo that was renamed, deleted, or made private
	// after it was added to the watchlist.
	ErrNotFound = errors.New("github: not found")
	// ErrRateLimited is separate from a generic failure because backing off
	// is the only correct response, and the poller needs to know.
	ErrRateLimited = errors.New("github: rate limited")
	// ErrNoGH means the binary is missing entirely.
	ErrNoGH = errors.New("github: gh is not installed")
)

// Client runs gh commands.
type Client struct {
	Bin     string        // gh binary, defaults to "gh"
	Timeout time.Duration // per-command timeout
}

func New() *Client { return &Client{Bin: "gh", Timeout: 15 * time.Second} }

func (c *Client) bin() string {
	if c.Bin == "" {
		return "gh"
	}
	return c.Bin
}

func (c *Client) timeout() time.Duration {
	if c.Timeout == 0 {
		return 15 * time.Second
	}
	return c.Timeout
}

// run executes gh and returns stdout.
//
// stdout is returned even on failure, because gh writes the response body
// there and the summary line to stderr. Most callers ignore it; graphql needs
// it, because a query that names one repo that no longer exists still returns
// correct data for every other repo in the same document.
func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()

	cmd := exec.CommandContext(ctx, c.bin(), args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb

	if err := cmd.Run(); err != nil {
		var ee *exec.Error
		if errors.As(err, &ee) {
			return nil, ErrNoGH
		}
		if ctx.Err() != nil {
			return nil, ErrOffline
		}
		return out.Bytes(), classify(errb.String(), args)
	}
	return out.Bytes(), nil
}

// classify turns gh's stderr into one of the typed errors above.
//
// gh has no machine-readable failure mode - no exit code per class, no
// structured error output - so string matching is the only option. The
// patterns are the ones gh 2.x actually prints; anything unrecognised falls
// through as a plain error rather than being guessed at.
func classify(stderr string, args []string) error {
	msg := strings.TrimSpace(stderr)
	low := strings.ToLower(msg)

	switch {
	case strings.Contains(low, "error connecting to"),
		strings.Contains(low, "dial tcp"),
		strings.Contains(low, "no such host"),
		strings.Contains(low, "connection refused"):
		return ErrOffline
	case strings.Contains(low, "http 401"),
		strings.Contains(low, "bad credentials"),
		strings.Contains(low, "authentication token"),
		strings.Contains(low, "gh auth login"):
		return ErrNoAuth
	case strings.Contains(low, "http 404"):
		return ErrNotFound
	case strings.Contains(low, "http 403") && strings.Contains(low, "rate limit"),
		strings.Contains(low, "http 429"):
		return ErrRateLimited
	}

	if msg == "" {
		msg = "unknown failure"
	}
	// Only the subcommand is worth naming. Full argv would put a query
	// string in the log line for no benefit.
	return fmt.Errorf("gh %s: %s", strings.Join(args[:min(2, len(args))], " "), firstLine(msg))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// api calls a REST endpoint and decodes the response into v.
func (c *Client) api(ctx context.Context, path string, v any) error {
	out, err := c.run(ctx, "api", "-H", "Accept: application/vnd.github+json", path)
	if err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("gh api %s: bad json: %w", path, err)
	}
	return nil
}

// urlQuery escapes a value for use inside a query string. gh passes the path
// through untouched, so anything a person typed has to be escaped here.
func urlQuery(s string) string { return url.QueryEscape(s) }

// graphql runs a GraphQL query and decodes the data object into v.
//
// vars are name/value pairs passed as GraphQL variables rather than pasted
// into the query text. Cursors in particular are opaque strings from GitHub
// and have no business being interpolated into a document.
//
// A query that names a repository which has been deleted or renamed comes back
// as a null field plus an errors array, and gh exits non-zero for it. The data
// is still there and still right for everything else, so it is decoded anyway:
// one dead entry in the watchlist must not blank the screen.
func (c *Client) graphql(ctx context.Context, query string, vars map[string]string, v any) error {
	args := []string{"api", "graphql", "-f", "query=" + query}
	for k, val := range vars {
		args = append(args, "-f", k+"="+val)
	}

	out, runErr := c.run(ctx, args...)
	var resp struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || len(resp.Data) == 0 {
		if runErr != nil {
			return runErr
		}
		return fmt.Errorf("gh api graphql: bad json: %w", err)
	}
	if v == nil {
		return nil
	}
	if err := json.Unmarshal(resp.Data, v); err != nil {
		return fmt.Errorf("gh api graphql: bad json: %w", err)
	}
	return nil
}

// ── argument validation ───────────────────────────────────────────────────
//
// os/exec never goes through a shell, so there is no injection risk in the
// usual sense. Names are validated anyway for two reasons gh cares about: a
// value starting with "-" would be read as a flag, and an unchecked name goes
// straight into a URL path or a GraphQL string literal.

var (
	ownerRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	nameRe  = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]{0,99}$`)
)

// SplitRepo validates and splits "owner/name".
func SplitRepo(full string) (owner, name string, err error) {
	owner, name, ok := strings.Cut(full, "/")
	if !ok || !ownerRe.MatchString(owner) || !nameRe.MatchString(name) || name == "." || name == ".." {
		return "", "", fmt.Errorf("invalid repo %q (want owner/name)", full)
	}
	return owner, name, nil
}

// ValidRepo reports whether full is a well-formed "owner/name".
func ValidRepo(full string) bool {
	_, _, err := SplitRepo(full)
	return err == nil
}
