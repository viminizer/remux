package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/viminizer/remux/internal/config"
)

// noteBuild identifies this Claude contribution, even when Codex later fixes
// the PR. It is a run note rather than a claim that one model did all work.
func (l *Loop) noteBuild(ctx context.Context, issue int, pr *Item, scope string) error {
	if l.Agent != "claude" {
		return nil
	}
	model := map[string]string{"sonnet": "Sonnet", "opus": "Opus", "": "CLI default"}[l.Model]
	marker := fmt.Sprintf("<!-- remux-build issue=%d scope=%s model=%s head=%s -->", issue, scope, l.Model, pr.HeadOid)
	var thread struct {
		Comments []struct {
			Body string `json:"body"`
		} `json:"comments"`
	}
	if err := l.gh.JSON(ctx, &thread, "pr", "view", fmt.Sprint(pr.Number), "--json", "comments"); err != nil {
		return err
	}
	for _, c := range thread.Comments {
		if strings.Contains(c.Body, marker) {
			return nil
		}
	}
	body := fmt.Sprintf("%s\n**Remux build run: Claude · %s** (medium effort)\n\nIssue #%d · scope `%s` · commit `%s`.\n\nThis identifies the build run; subsequent Codex review and repair may change the PR.", marker, model, issue, scope, pr.HeadOid)
	return l.gh.Comment(ctx, pr.Number, body)
}

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
