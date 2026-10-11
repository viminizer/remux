package harness

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/viminizer/remux/internal/config"
)

// ValidModel deliberately limits the trial to Claude aliases. Codex keeps
// its own configuration; an empty model preserves existing CLI defaults.
func ValidModel(agent, model string) bool {
	return model == "" || agent == "claude" && (model == "sonnet" || model == "opus")
}

// RunRecord contains no prompts or agent output. Model is the requested
// alias, not a claim about the exact backend model or billed usage.
type RunRecord struct {
	Started    time.Time `json:"started"`
	Event      string    `json:"event"`
	Session    string    `json:"session"`
	Repo       string    `json:"repo"`
	Role       string    `json:"role"`
	Agent      string    `json:"agent"`
	Model      string    `json:"model"`
	Item       int       `json:"item"`
	PR         int       `json:"pr,omitempty"`
	Stage      string    `json:"stage"`
	ExitCode   int       `json:"exitCode"`
	DurationMS int64     `json:"durationMs"`
}

func (l *Loop) recordRun(started time.Time, event string, args []string, code int, ms int64) {
	model := l.Model
	if model == "" {
		model = "cli-default"
	}
	stage := "agent"
	if len(args) > 2 && args[0] == "codex" && args[2] == "review" {
		stage = "review"
	}
	r := RunRecord{Started: started.UTC(), Event: event, Session: l.Session, Repo: l.Repo,
		Role: l.Role, Agent: l.Agent, Model: model, Item: l.item, Stage: stage, ExitCode: code, DurationMS: ms}
	l.record(r)
}

func (l *Loop) record(r RunRecord) {
	log.Printf("run %s: role=%s item=%d agent=%s model=%s stage=%s exit=%d duration_ms=%d", r.Event, r.Role, r.Item, r.Agent, r.Model, r.Stage, r.ExitCode, r.DurationMS)
	dir, err := config.EnsureDir()
	if err == nil {
		err = appendRun(filepath.Join(dir, "loop-runs.jsonl"), r)
	}
	if err != nil {
		log.Printf("run log: %v", err)
	}
}

func (l *Loop) recordOutcome(event string, n, pr int) {
	model := l.Model
	if model == "" {
		model = "cli-default"
	}
	l.record(RunRecord{Started: time.Now().UTC(), Event: event, Session: l.Session, Repo: l.Repo, Role: l.Role, Agent: l.Agent, Model: model, Item: n, PR: pr})
}

func appendRun(path string, r RunRecord) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}
