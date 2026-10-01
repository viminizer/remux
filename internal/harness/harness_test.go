package harness

import (
	"context"
	"os"
	"os/exec"
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
	l := &Loop{Agent: "claude", Repo: "/r", set: Settings{References: []string{"/ref"}}}
	if c := strings.Join(l.command("/r/wt"), " "); c != "claude -p --dangerously-skip-permissions --add-dir /ref" {
		t.Errorf("claude command = %q", c)
	}
	l.Agent = "codex"
	if c := strings.Join(l.command("/r/wt"), " "); c != "codex exec --dangerously-bypass-approvals-and-sandbox -C /r/wt -" {
		t.Errorf("codex command = %q", c)
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

func TestStatusReadyCountsAsReady(t *testing.T) {
	got := candidates([]Item{item(4, "status:ready"), item(5, "status:blocked"), item(6, "ready")}, "full")
	if len(got) != 2 || got[0].Number != 4 || got[1].Number != 6 {
		t.Fatalf("candidates = %+v, want #4 and #6", got)
	}
}

// fakeGH stands in for the gh binary and records each call, one per line.
func fakeGH(t *testing.T) func() []string {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "gh")
	os.WriteFile(bin, []byte("#!/bin/sh\necho \"$*\" >> "+log+"\necho '[]'\n"), 0o755)
	old := ghBin
	ghBin = bin
	t.Cleanup(func() { ghBin = old })
	return func() []string {
		b, _ := os.ReadFile(log)
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

func TestMirror(t *testing.T) {
	calls := fakeGH(t)
	l := &Loop{gh: GH{Slug: "o/r"}, labels: map[string]bool{"status:in-progress": true, "status:blocked": true}}
	l.mirror(context.Background(), item(7, "type:work", "status:blocked"), "in-progress")
	got := strings.Join(calls(), "\n")
	for _, want := range []string{"DELETE repos/o/r/issues/7/labels/status:blocked", "POST repos/o/r/issues/7/labels -f labels[]=status:in-progress"} {
		if !strings.Contains(got, want) {
			t.Errorf("calls are missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "type:work") {
		t.Errorf("only status labels may be touched:\n%s", got)
	}

	// A repo without that status label is left alone.
	calls2 := fakeGH(t)
	l.labels = map[string]bool{}
	l.mirror(context.Background(), item(8, "status:blocked"), "review")
	if c := calls2(); len(c) != 1 || c[0] != "" {
		t.Errorf("a repo without status:review got calls: %q", c)
	}
}

// The loop must see settings pushed to the default branch even when its
// checkout is behind and has never pulled them.
func TestSettingsComeFromOrigin(t *testing.T) {
	dir := t.TempDir()
	git := func(in string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = in
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	origin, a, b := filepath.Join(dir, "origin.git"), filepath.Join(dir, "a"), filepath.Join(dir, "b")
	git(dir, "init", "-q", "--bare", "-b", "main", origin)
	git(dir, "clone", "-q", origin, a)
	git(a, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "first")
	git(a, "push", "-q", "origin", "main")
	git(dir, "clone", "-q", origin, b) // the stale checkout

	os.MkdirAll(filepath.Join(a, ".remux"), 0o755)
	os.WriteFile(filepath.Join(a, SettingsFile), []byte(`{"mode":"company","test":"make check"}`), 0o644)
	git(a, "add", ".")
	git(a, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "settings")
	git(a, "push", "-q", "origin", "main")

	l := &Loop{Repo: b}
	s, err := l.loadSettings(context.Background())
	if err != nil || s.Mode != "company" || s.Test != "make check" {
		t.Fatalf("loadSettings = %+v, %v; want the pushed settings", s, err)
	}
}
