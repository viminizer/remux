package harness

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/viminizer/remux/internal/config"
	"github.com/viminizer/remux/internal/tmux"
)

type usageMetric struct {
	Label     string   `json:"label"`
	Remaining *float64 `json:"remaining_percent"`
}
type providerUsage struct {
	Provider string `json:"provider"`
	Account  *struct {
		ID     string `json:"id"`
		Active bool   `json:"is_active"`
	} `json:"account"`
	Metrics []usageMetric `json:"metrics"`
	Spend   struct {
		Reached bool `json:"reached"`
	} `json:"spend_control"`
}
type spawnChoice struct {
	Agent, Model, Effort, Key string
	Remaining, Reserve        float64
}

// Codex stops new workers at 30% weekly remaining. Claude builders have no
// quota gate; Claude reviewers retain their shared-headroom checks.
func chooseSpawn(usage []providerUsage, loops []tmux.Loop, role, class string, held map[string]bool) (spawnChoice, bool) {
	return chooseSpawnWithCodexFloor(usage, loops, role, class, held, 30)
}

func chooseSpawnWithCodexFloor(usage []providerUsage, loops []tmux.Loop, role, class string, held map[string]bool, codexFloor float64) (spawnChoice, bool) {
	floor, reserve, effort := 25.0, 10.0, "medium"
	if class == "deep" {
		floor, reserve, effort = 40, 20, "high"
	}
	var best spawnChoice
	for _, p := range usage {
		agent := strings.ToLower(p.Provider)
		if (agent != "claude" && agent != "codex") || p.Spend.Reached {
			continue
		}
		if p.Account != nil && !p.Account.Active {
			continue
		}
		if agent == "codex" && (p.Account == nil || !p.Account.Active) {
			continue
		}
		model := ""
		if agent == "claude" {
			model = "sonnet"
			if class == "deep" {
				model = "opus"
			}
		}
		remaining, session, weekly := 100.0, false, false
		unrestrictedClaude := agent == "claude" && role == "build"
		valid := true
		for _, m := range p.Metrics {
			label := strings.ToLower(m.Label)
			global := label == "weekly" || label == "session" || label == "5h" || label == "5-hour"
			specific := model != "" && strings.Contains(label, model)
			if !global && !specific {
				continue
			}
			if m.Remaining == nil || math.IsNaN(*m.Remaining) || *m.Remaining < 0 || *m.Remaining > 100 {
				valid = false
				break
			}
			if agent != "codex" || label == "weekly" {
				remaining = min(remaining, *m.Remaining)
			}
			weekly = weekly || label == "weekly"
			session = session || label == "session" || label == "5h" || label == "5-hour"
		}
		// Claude needs both short and long windows. Codex plans may expose only
		// a weekly window; an absent supported provider is never "100% left".
		if !unrestrictedClaude && (!valid || !weekly || (agent == "claude" && !session)) {
			continue
		}
		account := "default"
		if p.Account != nil {
			account = p.Account.ID
		}
		key := fmt.Sprintf("%x", sha256.Sum256([]byte(agent+":"+account)))
		if !unrestrictedClaude && held[key] {
			continue
		}
		for _, o := range loops {
			if agent == "claude" && !unrestrictedClaude && o.Custom && (o.BudgetKey == key || o.BudgetKey == "" && o.Agent == agent) {
				remaining -= max(10, o.Reserve)
			}
			// A manual worker's known provider limit also excludes the provider.
			if !unrestrictedClaude && o.Agent == agent && o.State == "blocked" && strings.Contains(strings.ToLower(o.Note), "usage limit") {
				valid = false
			}
		}
		eligible, allowance := valid && remaining >= floor, reserve
		if agent == "codex" {
			eligible, allowance = valid && weekly && remaining > codexFloor, 0
		}
		if unrestrictedClaude {
			eligible, allowance = true, 0
			if !valid || !weekly || !session {
				remaining = 0 // Usage is unknown; eligibility does not assume a full quota.
			}
		}
		if eligible && (best.Agent == "" || remaining > best.Remaining) {
			best = spawnChoice{agent, model, effort, key, remaining, allowance}
		}
	}
	return best, best.Agent != ""
}

// Called only for an actual spawn request, never as an automatic quota watcher.
// tokscale handles each provider's native authentication without exposing tokens.
func readSpawnUsage(ctx context.Context) ([]providerUsage, error) {
	bin, err := exec.LookPath("bunx")
	if err != nil {
		home, e := os.UserHomeDir()
		if e != nil {
			return nil, e
		}
		bin = filepath.Join(home, ".bun", "bin", "bunx")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "tokscale@latest", "usage", "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("usage unavailable; no custom worker started")
	}
	var result []providerUsage
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("unrecognized usage response; no custom worker started")
	}
	return result, nil
}

func spawnHolds() map[string]bool {
	result := map[string]bool{}
	dir, err := config.Dir()
	if err != nil {
		return result
	}
	files, _ := filepath.Glob(filepath.Join(dir, "spawn-limit-*.json"))
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var until time.Time
		// Corrupt markers fail closed until corrected; normal markers expire.
		key := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "spawn-limit-"), ".json")
		if json.Unmarshal(b, &until) != nil || until.After(time.Now()) {
			result[key] = true
		}
	}
	return result
}

func (l *Loop) rememberSpawnLimit() {
	if len(l.BudgetKey) != 64 || strings.Trim(l.BudgetKey, "0123456789abcdef") != "" {
		return
	}
	dir, err := config.EnsureDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, "spawn-limit-"+l.BudgetKey+".json")
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX) != nil {
		return
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	until := l.limit
	if b, err := os.ReadFile(path); err == nil {
		var previous time.Time
		if json.Unmarshal(b, &previous) == nil && previous.After(until) {
			until = previous
		}
	}
	b, _ := json.Marshal(until)
	// Each task holds the same account until its native reset. This file is
	// evidence from a failed run, not a fabricated usage prediction.
	f, err := os.CreateTemp(dir, "spawn-limit-*.tmp")
	if err != nil {
		return
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return
	}
	if f.Close() != nil {
		return
	}
	os.Rename(name, path)
}
