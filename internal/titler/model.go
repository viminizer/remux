package titler

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Runner asks a model one question and returns what it said.
type Runner interface {
	Run(ctx context.Context, prompt string) (string, error)
	Name() string
}

// modelTimeout bounds one batched call. Measured, `claude -p --model haiku`
// takes about 4.6s for a trivial prompt and roughly 3s of that is CLI startup
// rather than inference - which is also why the whole workspace goes in one
// call. A batch of ten panes costs about what one pane costs.
const modelTimeout = 90 * time.Second

// Chain is the fallback order from the issue. Nothing here pins a model id
// beyond haiku: `codex exec` uses whatever it is configured with, because
// there is no `codex models list` to check against and a hardcoded "cheapest"
// id would quietly rot.
//
// Every tier runs with its tools off, and that is not belt-and-braces. The
// prompt is built out of the rendered screens of twenty panes - text those
// agents pulled from the web, from repos, from issue trackers - and it is fed
// to an agentic CLI on stdin. Without these flags a pane displaying a crafted
// instruction block is a direct path from "something one of Kevin's agents
// read" to "a second agent ran a tool". Naming a pane needs no tools at all,
// so there is nothing to trade away.
//
// Tier 4 is not in this list. It is not a model at all - it is the last user
// line off the pane's own screen - so it lives in title.go where the screen
// is. What matters is that it cannot fail, so the chain always terminates and
// a pane is never left blank or, worse, stale but confident.
func Chain() []Runner {
	return []Runner{
		&cmdRunner{label: "claude haiku", bin: "claude", args: []string{"-p", "--tools", "", "--model", "haiku"}},
		&cmdRunner{label: "claude default", bin: "claude", args: []string{"-p", "--tools", ""}},
		&cmdRunner{label: "codex", bin: "codex", args: []string{"exec", "--sandbox", "read-only"}},
	}
}

type cmdRunner struct {
	label string
	bin   string
	args  []string
}

func (r *cmdRunner) Name() string { return r.label }

// Run feeds the prompt on stdin rather than as an argument. Both CLIs accept
// either, and a workspace of twenty screens is far past the size where an
// argument list is a sensible place to put text.
func (r *cmdRunner) Run(ctx context.Context, prompt string) (string, error) {
	bin, err := lookPath(r.bin)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, modelTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, r.args...)
	// Somewhere with nothing in it. Unset, the child inherits remux's working
	// directory, which under launchd is "/" and in development is whatever
	// repo it was started from - so a CLI that reads project config, or is
	// talked into looking around, starts inside real work.
	cmd.Dir = os.TempDir()
	cmd.Stdin = strings.NewReader(prompt)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %v: %s", r.label, err, firstLine(errb.String()))
	}
	s := strings.TrimSpace(out.String())
	if s == "" {
		return "", fmt.Errorf("%s: empty output", r.label)
	}
	return s, nil
}

// lookPath finds a CLI that a login shell would find but a LaunchAgent would
// not.
//
// remux ships as a LaunchAgent, and launchd hands a process a PATH of
// /usr/bin:/bin:/usr/sbin:/sbin. Neither claude nor codex is installed there.
// Without this the whole chain would fail on the machine it was written for
// and succeed in every terminal it was tested from, which is the same shape of
// bug as the tab separator in tmux's format output.
func lookPath(bin string) (string, error) {
	if p, err := exec.LookPath(bin); err == nil {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	for _, dir := range []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, ".npm-global", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
	} {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, bin)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found on PATH", bin)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// ── the away gate ─────────────────────────────────────────────────────────

// awayAfter is how long without a key or a mouse event counts as "not at the
// desk". The activity gate already stops remux capturing panes nothing is
// writing to; this catches the case it misses, which is an agent grinding
// unattended overnight. Its window is genuinely busy, so the gate lets it
// through every pass, and each pass would be a model call.
//
// The trade the issue accepts: a long unattended run gets its name only after
// Kevin comes back, within one interval. He was not reading it while he slept.
const awayAfter = 15 * time.Minute

// away reports whether the laptop has been untouched for longer than
// awayAfter.
//
// It fails open on purpose. Every way this can go wrong - ioreg missing, the
// output reshaped by an OS update, a number that will not parse - means "I do
// not know", and the cost of guessing "away" wrongly is a feature that
// silently stops working. The cost of guessing "here" wrongly is a few cents.
func away() bool {
	idle, ok := hidIdle()
	return ok && idle > awayAfter
}

// hidIdle reads HIDIdleTime, nanoseconds since the last key or mouse event.
func hidIdle() (time.Duration, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// -r is not optional. Without it `-d 1` truncates the registry one level
	// below Root and never reaches IOHIDSystem at all: the command prints a
	// single "+-o Root" line with no properties, HIDIdleTime is never found,
	// and away() returns false forever - which silently deleted the only cost
	// control this feature has. `ioreg -c IOHIDSystem -r -d 1` is the form
	// that prints the key.
	out, err := exec.CommandContext(ctx, "ioreg", "-c", "IOHIDSystem", "-r", "-d", "1").Output()
	if err != nil {
		return 0, false
	}
	// One key per HID entry; the smallest is the most recent input across all
	// of them, and any input at all means he is here.
	best, found := time.Duration(0), false
	for _, line := range strings.Split(string(out), "\n") {
		i := strings.Index(line, "\"HIDIdleTime\"")
		if i < 0 {
			continue
		}
		j := strings.Index(line[i:], "=")
		if j < 0 {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(line[i+j+1:]), 10, 64)
		if err != nil {
			continue
		}
		d := time.Duration(n) * time.Nanosecond
		if !found || d < best {
			best, found = d, true
		}
	}
	return best, found
}
