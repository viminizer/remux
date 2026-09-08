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
func IsAgent(cmd string) bool {
	switch cmd {
	case "codex", "claude", "aider", "opencode", "crush":
		return true
	}
	return claudeCmdRe.MatchString(cmd)
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
	regexp.MustCompile(`(?i)^\s*\d+\.\s+(yes|no|allow|approve|deny)\b`),
	regexp.MustCompile(`(?i)\b(allow|approve) this (tool|command|edit|action)\b`),
	regexp.MustCompile(`(?i)\bwaiting for your (input|answer|approval)\b`),
	regexp.MustCompile(`(?i)\bpress\s+(enter|y)\s+to continue\b`),
}

// busyRe: the agent is running and does not need anything.
var busyRe = []*regexp.Regexp{
	regexp.MustCompile(`(?i)esc to interrupt`),
	regexp.MustCompile(`(?i)ctrl\+c to (stop|interrupt)`),
	regexp.MustCompile(`\(\d+s\s*·`), // "(12s · esc to interrupt)"
	regexp.MustCompile(`(?i)^\s*[▸▶✻✳*]\s*(working|thinking|running|generating)\b`),
	regexp.MustCompile(`(?i)\b(thinking|working|running)…`),
	regexp.MustCompile(`[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]`), // braille spinner
}

// idleRe: an agent is up with an empty composer waiting for a new prompt.
//
// Codex prints a placeholder; Claude Code draws an empty "❯" box between two
// horizontal rules and a status line under it. Both forms are matched because
// the two agents render nothing alike.
var idleRe = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ask codex to do anything`),
	regexp.MustCompile(`(?i)^\s*[›>]\s*ask\b`),
	regexp.MustCompile(`(?i)try "[^"]+"`), // Claude Code hint line
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

	if matchAny(waitingRe, lines, joined) {
		return Waiting
	}
	if matchAny(busyRe, lines, joined) {
		return Busy
	}
	if matchAny(idleRe, lines, joined) {
		return Idle
	}
	if IsAgent(cmd) {
		// A known agent whose screen matched nothing. It is running, so
		// "idle" would be a guess and "shell" would be wrong.
		return Unknown
	}
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
