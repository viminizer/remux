package titler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Answer is what one tier came back with: the text, and what the call cost.
//
// The cost is carried out of the runner rather than worked out later because
// only the runner can know it. Two calls with byte-identical prompts measured
// $0.0374 and $0.0030 here - the first wrote the CLI's 18k-token system
// preamble into the prompt cache and the second read it back. Any estimate
// built from prompt size is blind to that, and wrong by more than ten times in
// the direction that matters.
type Answer struct {
	Text string
	// USD is what the call cost at list price, as the CLI reported it.
	// Meaningless unless Metered.
	USD float64
	// Metered is whether a number came back at all. A tier that cannot report
	// one still costs money, and saying "0" would quietly understate the bill;
	// the screen says "not metered" instead.
	Metered bool
	// Tokens, for the run detail. Cache reads are the bulk of a warm call and
	// are what makes it cheap, so they are worth keeping apart from input.
	In, Out, CacheRead, CacheWrite int
}

// Runner asks a model one question and returns what it said.
type Runner interface {
	Run(ctx context.Context, prompt string) (Answer, error)
	Name() string
}

// modelTimeout bounds one batched call.
//
// Ninety seconds was set against a 4.6s measurement on a trivial prompt, and
// on the real workspace it was not close to enough. Twenty-two panes measured
// 76s, eight panes measured 63s, and both have been seen past ninety - so the
// cost is barely about the prompt at all. Most of it is `claude -p` starting
// up and then queueing behind everything else on a laptop where twenty agents
// share one account, which is a number remux cannot predict and should not try
// to.
//
// Being killed at the timeout is the worst outcome available: the prompt was
// paid for, nothing comes back, tier 2 re-sends the same thing to a more
// expensive model, and the chain ends up backing off - which then makes every
// pane due at once, so the retry is a bigger call that fails the same way. The
// log had twenty-two of those. A slot held longer costs nothing by comparison:
// naming is on a ninety-second cooldown regardless, and one call in flight
// only delays the next batch by a tick.
const modelTimeout = 150 * time.Second

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
		&cmdRunner{label: "claude haiku", bin: "claude", json: true,
			args: []string{"-p", "--tools", "", "--model", "haiku", "--output-format", "json"}},
		&cmdRunner{label: "claude default", bin: "claude", json: true,
			args: []string{"-p", "--tools", "", "--output-format", "json"}},
		&cmdRunner{label: "codex", bin: "codex", args: []string{"exec", "--sandbox", "read-only"}},
	}
}

type cmdRunner struct {
	label string
	bin   string
	args  []string
	// json says the CLI answers with claude's envelope rather than bare text.
	// It is what buys the measured cost; codex has no equivalent, so tier 3
	// reports its answer and no number.
	json bool
}

func (r *cmdRunner) Name() string { return r.label }

// Run feeds the prompt on stdin rather than as an argument. Both CLIs accept
// either, and a workspace of twenty screens is far past the size where an
// argument list is a sensible place to put text.
func (r *cmdRunner) Run(ctx context.Context, prompt string) (Answer, error) {
	bin, err := lookPath(r.bin)
	if err != nil {
		return Answer{}, err
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
		return Answer{}, fmt.Errorf("%s: %v: %s", r.label, err, firstLine(errb.String()))
	}
	s := strings.TrimSpace(out.String())
	if s == "" {
		return Answer{}, fmt.Errorf("%s: empty output", r.label)
	}
	if !r.json {
		return Answer{Text: s}, nil
	}
	a, err := parseEnvelope(s)
	if err != nil {
		return Answer{}, fmt.Errorf("%s: %v", r.label, err)
	}
	return a, nil
}

// envelope is the part of `claude -p --output-format json` this reads.
//
// Deliberately a subset. The real object has two dozen keys and gains more
// with each release; naming a pane needs the answer, whether it failed, and
// the bill. Everything else is left to be ignored by encoding/json.
type envelope struct {
	Result  string  `json:"result"`
	IsError bool    `json:"is_error"`
	CostUSD float64 `json:"total_cost_usd"`
	Usage   struct {
		Input      int `json:"input_tokens"`
		Output     int `json:"output_tokens"`
		CacheRead  int `json:"cache_read_input_tokens"`
		CacheWrite int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

// parseEnvelope turns the CLI's JSON into an Answer.
//
// A CLI that answers with something other than the envelope is a failure, not
// a free call: falling back to treating the raw stdout as the title would put
// a line of JSON on a pane, which is exactly the kind of confident wrong
// answer tier 4 exists to avoid.
func parseEnvelope(s string) (Answer, error) {
	var e envelope
	if err := json.Unmarshal([]byte(s), &e); err != nil {
		return Answer{}, fmt.Errorf("unreadable json answer: %v", err)
	}
	if e.IsError {
		return Answer{}, fmt.Errorf("reported an error: %s", firstLine(e.Result))
	}
	text := strings.TrimSpace(e.Result)
	if text == "" {
		return Answer{}, fmt.Errorf("json answer had no result")
	}
	return Answer{
		Text:       text,
		USD:        e.CostUSD,
		Metered:    true,
		In:         e.Usage.Input,
		Out:        e.Usage.Output,
		CacheRead:  e.Usage.CacheRead,
		CacheWrite: e.Usage.CacheWrite,
	}, nil
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

// HIDAway reports whether this Mac's keyboard and mouse have been untouched
// for longer than awayAfter.
//
// It answers one question - "is he at this machine" - and that is not the same
// as "is anyone reading this", which is the question the naming pass actually
// has. remux exists for the times Kevin is away from the laptop and using the
// phone, so on its own this gate switches the feature off at precisely the
// moment he wants it. main.go composes it with api.Server.Watching for that
// reason; this stays the backstop it was written to be, for an agent grinding
// unattended with nobody looking at all.
//
// It fails open on purpose. Every way this can go wrong - ioreg missing, the
// output reshaped by an OS update, a number that will not parse - means "I do
// not know", and the cost of guessing "away" wrongly is a feature that
// silently stops working. The cost of guessing "here" wrongly is a few cents.
func HIDAway() bool {
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
	// and HIDAway returns false forever - which silently deleted the only cost
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
