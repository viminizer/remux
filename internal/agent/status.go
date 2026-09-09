// Package agent classifies what a tmux pane is currently doing.
//
// Everything here is a heuristic over rendered terminal output, and it is all
// in this one file on purpose: when Codex or Claude Code changes its UI, the
// patterns below are the only thing that needs re-tuning. A miss degrades to
// Unknown - it never breaks reading a pane or sending to it.
package agent

import (
	"regexp"
	"strings"
)

// Status is what the badge in the UI shows.
type Status string

const (
	// Waiting means an agent is blocked on a human answer. This is the
	// signal that sorts a pane to the top of the drawer and fires a push
	// notification, so false positives are more expensive here than
	// anywhere else.
	Waiting Status = "waiting"
	// Busy means the agent is working and will finish on its own.
	Busy Status = "busy"
	// Idle means an agent is running with an empty composer.
	Idle Status = "idle"
	// Shell means the pane is a plain shell, not an agent.
	Shell Status = "shell"
	// Unknown means nothing matched.
	Unknown Status = "unknown"
)

// tailLines is how much of the screen the classifier looks at. The state of a
// TUI agent is always in the last few lines; looking further back picks up
// stale prompts from earlier in the conversation and reports Waiting long
// after the question was answered.
const tailLines = 30

var shells = map[string]bool{
	"zsh": true, "bash": true, "fish": true, "sh": true,
	"dash": true, "ksh": true, "tcsh": true, "csh": true, "nu": true,
}

// claudeCmdRe matches Claude Code, which reports pane_current_command as its
// own version number (e.g. "2.1.263") rather than a program name.
var claudeCmdRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// IsAgent reports whether a pane command looks like a coding agent.
//
// Coverage is not uniform, and the difference matters. Codex and Claude Code
// are the two this file's idle and busy patterns were tuned against, with
// fixtures in testdata/ taken from real panes. aider, opencode and crush are
// recognised here but no idle or busy pattern targets them, so they will
// mostly report Waiting (those patterns are agent-agnostic - "[y/N]", "do you
// want") or Unknown.
//
// They stay on the list because this is also what decides whether the push
// watcher follows a pane at all (internal/push/watcher.go). Dropping them
// would silently stop notifying anyone running one, which is a worse answer
// than an honest Unknown.
func IsAgent(cmd string) bool {
	switch cmd {
	case "codex", "claude", "aider", "opencode", "crush":
		return true
	}
	return claudeCmdRe.MatchString(cmd)
}

// DisplayCommand is the name to show a person for a pane command.
//
// Claude Code reports its version as pane_current_command, so the raw value is
// "2.1.265" - accurate and meaningless in a sentence. Twin of displayCommand
// in web/src/types.ts.
func DisplayCommand(cmd string) string {
	if claudeCmdRe.MatchString(cmd) {
		return "claude"
	}
	return cmd
}

// IsShell reports whether a pane command is a plain shell.
func IsShell(cmd string) bool { return shells[cmd] }

// ── patterns ──────────────────────────────────────────────────────────────
//
// Order matters in Classify: waiting beats busy beats idle, because an agent
// that is asking a question often still has a spinner or a timer on screen.

// waitingRe: an agent is blocked on an answer.
var waitingRe = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bdo you want\b`),
	regexp.MustCompile(`(?i)\bdo you trust\b`),
	regexp.MustCompile(`^\s*❯\s*\d+\.`),          // Codex choice menu
	regexp.MustCompile(`^\s*[▸▶>]\s*\d+\.\s+\S`), // same, other glyphs
	regexp.MustCompile(`\[y/N\]|\[Y/n\]|\(y/n\)|\(Y/N\)`),
	regexp.MustCompile(`(?i)\byes, and don'?t ask again\b`),
	regexp.MustCompile(`(?i)\b(allow|approve) this (tool|command|edit|action)\b`),
	regexp.MustCompile(`(?i)\bwaiting for your (input|answer|approval)\b`),
	regexp.MustCompile(`(?i)\bpress\s+(enter|y)\s+to continue\b`),
}

// menuOptionRe matches one line of a numbered choice menu, e.g. "2. Yes, and
// don't ask again this session".
//
// On its own this is far too loose to be a signal: a real capture on this
// machine classified a pane as waiting because a paragraph contained
// "6390. No collision." Two guards fix that - the number must be a single
// digit, and a menu only counts when at least two options are on screen,
// because a real choice always offers more than one.
var menuOptionRe = regexp.MustCompile(`(?i)^\s*[1-9]\.\s+(yes|no|allow|approve|deny)\b`)

const minMenuOptions = 2

func numberedMenu(lines []string) bool {
	n := 0
	for _, l := range lines {
		if menuOptionRe.MatchString(l) {
			n++
		}
	}
	return n >= minMenuOptions
}

// busyRe: the agent is running and does not need anything.
var busyRe = []*regexp.Regexp{
	regexp.MustCompile(`(?i)esc to interrupt`),
	regexp.MustCompile(`(?i)ctrl\+c to (stop|interrupt)`),
	// The elapsed timer, the most reliable structural signal Claude Code
	// emits. It must accept a full duration, not just seconds: the old
	// `\(\d+s` matched "(12s ·" and missed "(1m 5s ·", so it went blind after
	// the first minute - precisely the tasks where a wrong verdict costs most,
	// because those are the ones you walk away from and get pushed about.
	regexp.MustCompile(`\((?:\d+h\s*)?(?:\d+m\s*)?\d+s\s*·`),
	// A spinner glyph, one word, an ellipsis. Matching the word itself is a
	// losing game - Claude Code invents a new gerund every render
	// ("Manifesting", "Effecting", "Wandering"), and a fixed list can only
	// ever trail it. The shape is what stays constant.
	regexp.MustCompile(`^\s*[▸▶✻✳✢✽✶*]\s*[A-Za-z]+…`),
	regexp.MustCompile(`(?i)\b(thinking|working|running)…`),
	regexp.MustCompile(`[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]`), // braille spinner
}

// idleRe: an agent is up with an empty composer waiting for a new prompt.
//
// Codex prints a placeholder; Claude Code draws an empty "❯" box between two
// horizontal rules and a status line under it. Both forms are matched because
// the two agents render nothing alike.
//
// None of these prove idleness on their own - Claude Code's footer chrome is
// on screen while it works too - so Classify only trusts them once every busy
// signal has failed. See the `idle` term there.
//
// `try "[^"]+"` used to live here and was removed: scored against 24 live
// agent panes on this machine it matched nothing that was idle, and did match
// ordinary prose discussing a quoted string. It was the loosest pattern in the
// file and it was carrying a verdict.
var idleRe = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ask codex to do anything`),
	regexp.MustCompile(`(?i)^\s*[›>]\s*ask\b`),
	regexp.MustCompile(`(?i)\? for shortcuts`),
	regexp.MustCompile(`(?i)shift\+tab to cycle`), // Claude Code mode line
	regexp.MustCompile(`(?i)^\s*--\s*INSERT\s*--`),
	regexp.MustCompile(`(?i)^\s*[│|]\s*[>❯]\s*[│|]?\s*$`), // empty composer box
	regexp.MustCompile(`^\s*❯\s*$`),                       // Claude Code empty prompt
}

// Classify decides what a pane is doing.
//
// cmd is pane_current_command, title is pane_title, screen is the rendered
// output (ANSI escapes may still be present - they are stripped here).
func Classify(cmd, title, screen string) Status {
	if IsShell(cmd) {
		return Shell
	}

	lines := tail(StripANSI(screen), tailLines)
	joined := strings.Join(lines, "\n")

	waiting := matchAny(waitingRe, lines, joined) || numberedMenu(lines)
	busy := matchAny(busyRe, lines, joined)
	// Idle is the one verdict that needs a negative term. The others are
	// established by evidence; idle markers are footer chrome that an agent
	// keeps on screen while it works, so "composer looks empty" only means
	// idle when nothing says otherwise. Writing that here rather than relying
	// on the order of three returns is what makes it testable - and the order
	// alone is what let a working pane report idle once the timer pattern
	// stopped matching past one minute.
	idle := matchAny(idleRe, lines, joined) && !busy && !waiting

	switch {
	case waiting:
		return Waiting
	case busy:
		return Busy
	case idle:
		return Idle
	}
	// Anything else, agent or not, is Unknown. For a known agent that means a
	// screen no pattern recognised: "idle" would be a guess and "shell" would
	// be wrong.
	return Unknown
}

// matchAny tries anchored patterns line by line and unanchored ones against
// the whole block, so `^` means "start of a line on screen".
func matchAny(pats []*regexp.Regexp, lines []string, joined string) bool {
	for _, re := range pats {
		if strings.HasPrefix(re.String(), "^") || strings.Contains(re.String(), `(?i)^`) {
			for _, l := range lines {
				if re.MatchString(l) {
					return true
				}
			}
			continue
		}
		if re.MatchString(joined) {
			return true
		}
	}
	return false
}

// tail returns the last n non-empty-trailing lines of s.
func tail(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	// Trailing blank lines carry no state and would push the real content
	// out of the window.
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	lines = lines[:end]
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// ── ANSI stripping ────────────────────────────────────────────────────────

var (
	csiRe = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	oscRe = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	escRe = regexp.MustCompile(`\x1b[@-Z\\-_]`)
)

// StripANSI removes escape sequences so the patterns above match text rather
// than colour codes.
func StripANSI(s string) string {
	s = oscRe.ReplaceAllString(s, "")
	s = csiRe.ReplaceAllString(s, "")
	s = escRe.ReplaceAllString(s, "")
	return s
}

// Label is the human wording the drawer subtitle uses.
func Label(s Status) string {
	switch s {
	case Waiting:
		return "needs an answer"
	case Busy:
		return "working"
	case Idle:
		return "idle"
	case Shell:
		return "shell"
	}
	return "unknown"
}
