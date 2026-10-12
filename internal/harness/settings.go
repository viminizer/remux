// Package harness runs the agent loops: Claude and Codex take GitHub issues,
// open pull requests, and Codex reviews and merges them. See
// docs/agent-harness.html for the design.
//
// A loop is one `remux loop` process inside a detached loop-* tmux session.
// It keeps its state in that session's options, so the server reads it the
// same way it reads everything else - from tmux - and nothing sits between.
package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// The two per-project files, both committed to the repo they describe.
const (
	SettingsFile = ".remux/harness.json"
	DecisionLog  = ".remux/decisions.md"
	// InstructionDir holds a repo's own session instructions, one file per
	// scope and role, so starting a loop does not mean pasting prose.
	InstructionDir = ".remux/instructions"
)

// InstructionFile is where a repo keeps the instructions for one scope and
// role: "frontend-build.md" for a frontend builder, "review.md" for any
// reviewer. Both parts are already validated by the caller - a scope is
// lowercase letters, digits and dashes, a role is one of a fixed set - so
// neither can escape the directory.
func InstructionFile(scope, role string) string {
	if scope != "" && scope != "full" {
		return filepath.Join(InstructionDir, scope+"-"+role+".md")
	}
	return filepath.Join(InstructionDir, role+".md")
}

// Instructions reads a repo's own session instructions for one scope and role.
// It prefers the scope-specific file and falls back to the role's own, so a
// review loop shares one file across scopes while builders differ by scope.
// The returned name is the file it used, for reporting; both are empty when the
// repo keeps no such file and the caller's own text should stand.
func Instructions(repo, scope, role string) (text, name string) {
	candidates := []string{InstructionFile(scope, role)}
	if other := InstructionFile("", role); other != candidates[0] {
		candidates = append(candidates, other)
	}
	for _, candidate := range candidates {
		b, err := os.ReadFile(filepath.Join(repo, candidate))
		if err != nil || len(bytes.TrimSpace(b)) == 0 {
			continue
		}
		return string(b), candidate
	}
	return "", ""
}

// Settings is the per-project file: the mode and layer 1 of the instructions.
type Settings struct {
	// Mode is "personal" (Codex merges) or "company" (nothing merges; the
	// supervisor reviews).
	Mode string `json:"mode"`
	// Test is the shell command that must pass before a pull request is
	// merged or handed to the supervisor. Without one nothing can merge.
	Test string `json:"test"`
	// References are local read-only clones the agents compare against when
	// they make a real decision.
	References []string `json:"references"`
	// Instructions are the project defaults, read before every run.
	Instructions string `json:"instructions"`
	// Supervisor is the company-mode fallback reviewer, used when the repo
	// has no CODEOWNERS and its history names nobody.
	Supervisor string `json:"supervisor"`
	// Balanced routes each PR by its changes; fixed keeps the loop's selected agent.
	ReviewPolicy string `json:"review_policy,omitempty"`
}

// LoadSettings reads the settings file from a repo. A repo without one runs
// as personal with no test command - which means nothing will merge until
// one is written.
func LoadSettings(repo string) (Settings, error) {
	s := Settings{Mode: "personal"}
	b, err := os.ReadFile(filepath.Join(repo, SettingsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return ParseSettings(b)
}

// ParseSettings reads the settings file's contents.
func ParseSettings(b []byte) (Settings, error) {
	s := Settings{Mode: "personal"}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("%s: %w", SettingsFile, err)
	}
	if s.Mode != "personal" && s.Mode != "company" {
		return s, fmt.Errorf("%s: mode must be \"personal\" or \"company\", not %q", SettingsFile, s.Mode)
	}
	if s.ReviewPolicy != "" && s.ReviewPolicy != "balanced" && s.ReviewPolicy != "fixed" {
		return s, fmt.Errorf("%s: review_policy must be balanced or fixed", SettingsFile)
	}
	return s, nil
}

const exampleSettings = `{
  "mode": "personal",
  "test": "",
  "references": [],
  "instructions": "",
  "supervisor": ""
}
`

const exampleLog = `# Decisions

One short entry per real decision, newest last. Agents read this before they
decide anything, and follow it.

<!-- ## YYYY-MM-DD - title
Choice: ...
Reason: ...
Source: ... -->
`

// WriteStarterFiles creates the settings file and decision log when they are
// missing, and leaves existing ones alone. It returns the files it wrote.
func WriteStarterFiles(repo string) ([]string, error) {
	var wrote []string
	for name, body := range map[string]string{SettingsFile: exampleSettings, DecisionLog: exampleLog} {
		p := filepath.Join(repo, name)
		if _, err := os.Stat(p); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return wrote, err
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return wrote, err
		}
		wrote = append(wrote, name)
	}
	return wrote, nil
}
