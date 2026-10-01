package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func item(n int, labels ...string) Item {
	it := Item{Number: n}
	for _, l := range labels {
		it.Labels = append(it.Labels, struct {
			Name string `json:"name"`
		}{l})
	}
	return it
}

func TestCandidates(t *testing.T) {
	issues := []Item{
		item(3, "ready"),
		item(1, "ready", "wip:backend"),      // another loop has it
		item(2, "ready", "done:full"),        // this scope already did its part
		item(7, "ready", "blocker"),          // blockers jump the queue
		item(5, "ready", "blocked"),          // waiting on a blocker
		item(4, "ready", "needs-human"),      // waiting on Kevin
		item(6, "ready", "blocker", "wip:x"), // a claimed blocker is still claimed
	}
	if got := candidates(issues, "full"); len(got) != 2 || got[0].Number != 7 || got[1].Number != 3 {
		t.Fatalf("candidates = %+v, want the free blocker #7 then #3", got)
	}
	if got := candidates([]Item{item(9, "ready", "done:backend"), item(8, "ready")}, "backend"); len(got) != 1 || got[0].Number != 8 {
		t.Fatalf("candidates skipped wrong: %+v", got)
	}
	if got := candidates([]Item{item(1, "ready", "wip:full"), item(2)}, "full"); len(got) != 0 {
		t.Fatalf("candidates = %+v, want nothing", got)
	}
}

func TestPRFor(t *testing.T) {
	prs := []Item{
		{Number: 20, Body: "Refs #12, part one"},
		{Number: 21, Body: "Closes #1. Fixes the thing.\nSee #100"},
	}
	if p := prFor(prs, 1); p == nil || p.Number != 21 {
		t.Errorf("prFor(1) = %+v, want #21", p)
	}
	if p := prFor(prs, 12); p == nil || p.Number != 20 {
		t.Errorf("prFor(12) = %+v, want #20", p)
	}
	// #10 is a prefix of #100, and must not match it.
	if p := prFor(prs, 10); p != nil {
		t.Errorf("prFor(10) = %+v, want nothing", p)
	}
}

func TestRecentReviewer(t *testing.T) {
	var a, b mergedPR
	a.MergedBy.Login = "kevin"
	b.MergedBy.Login = "boss"
	b.Reviews = append(b.Reviews, struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
	}{})
	b.Reviews[0].Author.Login = "lead"
	if who := recentReviewer([]mergedPR{a, b}, "kevin"); who != "lead" {
		t.Errorf("recentReviewer = %q, want lead (a review beats a merge, and never Kevin)", who)
	}
	if who := recentReviewer([]mergedPR{a}, "kevin"); who != "" {
		t.Errorf("recentReviewer = %q, want empty", who)
	}
}

// The order is the design: rules, project defaults, session instructions, issue.
func TestPromptOrder(t *testing.T) {
	r := Run{
		Role: "build", Slug: "o/r", Number: 12, Branch: "issue-12-backend", Scope: "backend",
		Mode: "personal", Settings: Settings{Instructions: "Never touch payments."},
		Instructions: "Only the backend.", InstrMode: "add", Thread: "title: Fix login",
	}
	p := Prompt(r)
	order := []string{"# Agent rules", "Never touch payments.", "Only the backend.", "Fix login"}
	last := -1
	for _, s := range order {
		i := strings.Index(p, s)
		if i <= last {
			t.Fatalf("%q is out of order in:\n%s", s, p)
		}
		last = i
	}
	for _, want := range []string{"done:backend", "Closes #12", "issues/12/dependencies/blocked_by", "repos/o/r/"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	if strings.Contains(p, "SCOPE") || strings.Contains(p, "SLUG") || strings.Contains(p, "#N ") {
		t.Errorf("prompt still has placeholders:\n%s", p)
	}

	r.InstrMode = "replace"
	if strings.Contains(Prompt(r), "Never touch payments.") {
		t.Error("replace must drop the project defaults")
	}
	r.Role = "review"
	if !strings.Contains(Prompt(r), "review pull request #12 once") {
		t.Error("review prompt has the wrong rules")
	}
}

func TestCommand(t *testing.T) {
	l := &Loop{Agent: "claude", Repo: "/r", set: Settings{Test: "go test ./...", Allow: []string{"Bash(make:*)"}}}
	c := strings.Join(l.command("/r/wt"), " ")
	for _, want := range []string{"claude -p", "acceptEdits", "Bash(go test ./...)", "Bash(make:*)"} {
		if !strings.Contains(c, want) {
			t.Errorf("claude command %q is missing %q", c, want)
		}
	}
	if strings.Contains(c, "dangerously") {
		t.Error("the loops must never bypass permissions")
	}
	l.Agent = "codex"
	c = strings.Join(l.command("/r/wt"), " ")
	for _, want := range []string{"codex exec", "workspace-write", "--add-dir /r/.git", "-C /r/wt"} {
		if !strings.Contains(c, want) {
			t.Errorf("codex command %q is missing %q", c, want)
		}
	}
	if strings.Contains(c, "dangerously") {
		t.Error("the loops must never bypass the sandbox")
	}
}

func TestSettings(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadSettings(dir)
	if err != nil || s.Mode != "personal" {
		t.Fatalf("no file: %+v, %v", s, err)
	}
	wrote, err := WriteStarterFiles(dir)
	if err != nil || len(wrote) != 2 {
		t.Fatalf("WriteStarterFiles = %v, %v", wrote, err)
	}
	if again, _ := WriteStarterFiles(dir); len(again) != 0 {
		t.Errorf("second run overwrote %v", again)
	}
	if _, err := LoadSettings(dir); err != nil {
		t.Fatalf("starter file does not load: %v", err)
	}
	os.WriteFile(filepath.Join(dir, SettingsFile), []byte(`{"mode":"yolo"}`), 0o644)
	if _, err := LoadSettings(dir); err == nil {
		t.Error("an unknown mode must be refused")
	}
}
