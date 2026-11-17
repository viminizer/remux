package sv

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The model ceiling: no supervisor or worker may run a model stronger than
// the machine's own defaults. Kevin's rule, 2026-11-17. It is checked here,
// in code, before a session starts - a rule in a prompt can be ignored.

// DefaultRanking is written to models.json the first time it is read, so
// Kevin can edit it. Weakest first. It stays as Kevin leaves it: nothing in
// remux updates it.
var DefaultRanking = map[string][]string{
	"claude": {"haiku", "sonnet", "opus"},
	"codex":  {"gpt-6-luna", "gpt-6.1-terra", "gpt-6.1-sol"},
}

// Home is the user's home directory. A variable so tests can move it.
var Home = func() string { h, _ := os.UserHomeDir(); return h }

// Ranking reads models.json, creating it with the default on first use.
func Ranking() map[string][]string {
	path := filepath.Join(Root(), "models.json")
	var r map[string][]string
	if err := readJSON(path, &r); err == nil && len(r) > 0 {
		return r
	}
	if os.MkdirAll(Root(), 0o700) == nil {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			writeJSON(path, DefaultRanking)
		}
	}
	return DefaultRanking
}

// Default is the CLI's configured model: "model" in ~/.claude/settings.json,
// or the top-level model in ~/.codex/config.toml. Read on every check, so a
// change of default moves the ceiling with it.
func Default(agent string) string {
	switch agent {
	case "claude":
		var s struct {
			Model string `json:"model"`
		}
		readJSON(filepath.Join(Home(), ".claude", "settings.json"), &s)
		return s.Model
	case "codex":
		f, err := os.Open(filepath.Join(Home(), ".codex", "config.toml"))
		if err != nil {
			return ""
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(line, "[") {
				return "" // top-level keys come before the first table
			}
			if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "model" {
				return strings.Trim(strings.TrimSpace(v), `"'`)
			}
		}
	}
	return ""
}

// rank is the model's place in the ranking, or -1. "opus[1m]" and
// "claude-opus-5-5" both rank as opus: the longest entry the name contains.
func rank(agent, model string) int {
	model = strings.ToLower(model)
	if i := strings.Index(model, "["); i >= 0 {
		model = model[:i]
	}
	best, bestLen := -1, 0
	for i, m := range Ranking()[agent] {
		m = strings.ToLower(m)
		if model == m {
			return i
		}
		if strings.Contains(model, m) && len(m) > bestLen {
			best, bestLen = i, len(m)
		}
	}
	return best
}

// CheckModel refuses a model stronger than the default, any Astra model, and
// any model the ranking does not know. An empty model means the default.
func CheckModel(agent, model string) error {
	if agent != "claude" && agent != "codex" {
		return fmt.Errorf("agent must be claude or codex, not %q", agent)
	}
	def := Default(agent)
	if strings.Contains(strings.ToLower(model+" "+def), "astra") {
		return errors.New("Astra models are never used")
	}
	if model == "" {
		return nil
	}
	if def == "" {
		return fmt.Errorf("cannot read the default %s model, so no other model can be checked against it; leave the model empty", agent)
	}
	ceil, want := rank(agent, def), rank(agent, model)
	if ceil < 0 {
		return fmt.Errorf("the default %s model %q is not in %s; add it there", agent, def, filepath.Join(Root(), "models.json"))
	}
	if want < 0 {
		return fmt.Errorf("%q is not in %s", model, filepath.Join(Root(), "models.json"))
	}
	if want > ceil {
		return fmt.Errorf("%q is stronger than the default %q", model, def)
	}
	return nil
}

// Allowed lists the models a worker may use for agent, weakest first.
func Allowed(agent string) []string {
	ceil := rank(agent, Default(agent))
	var out []string
	for i, m := range Ranking()[agent] {
		if i <= ceil && !strings.Contains(strings.ToLower(m), "astra") {
			out = append(out, m)
		}
	}
	return out
}

// ModelsReport is what `remux sv models` prints.
func ModelsReport() string {
	var b strings.Builder
	for _, a := range []string{"claude", "codex"} {
		fmt.Fprintf(&b, "%s: default %q (the ceiling); allowed: %s\n", a, Default(a), strings.Join(Allowed(a), ", "))
	}
	b.WriteString("Effort: supervisor high, workers medium.\n")
	return b.String()
}

// marshal is json.Marshal for values that cannot fail.
func marshal(v any) string { b, _ := json.Marshal(v); return string(b) }
