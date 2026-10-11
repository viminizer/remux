package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelValidation(t *testing.T) {
	for _, c := range []struct {
		agent, model string
		want         bool
	}{
		{"claude", "", true}, {"claude", "sonnet", true}, {"claude", "opus", true},
		{"claude", "--bad", false}, {"codex", "sonnet", false}, {"codex", "", true},
	} {
		if ValidModel(c.agent, c.model) != c.want {
			t.Errorf("ValidModel(%q, %q)", c.agent, c.model)
		}
	}
}

func TestRunRecordsSurviveProcessFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	l := &Loop{Agent: "claude", Model: "sonnet", Role: "build", Repo: "/repo", Session: "loop-test", item: 42}
	code, _ := l.spawn(context.Background(), t.TempDir(), "private prompt", func(string) []string { return []string{"sh", "-c", "exit 7"} })
	if code != 7 {
		t.Fatalf("exit = %d", code)
	}
	path := filepath.Join(os.Getenv("HOME"), ".config", "remux", "loop-runs.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("records = %s", b)
	}
	for i, line := range lines {
		var r RunRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		if r.Item != 42 || r.Model != "sonnet" || r.Session != "loop-test" || r.Started.IsZero() {
			t.Errorf("record = %+v", r)
		}
		if i == 1 && (r.Event != "end" || r.ExitCode != 7) {
			t.Errorf("end = %+v", r)
		}
	}
	if strings.Contains(string(b), "private prompt") {
		t.Fatal("prompt leaked into telemetry")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("permissions = %v", info.Mode())
	}
}

func TestBuildModelNote(t *testing.T) {
	dir := t.TempDir()
	comments, calls := filepath.Join(dir, "comments"), filepath.Join(dir, "calls")
	t.Setenv("REMUX_TEST_COMMENTS", comments)
	t.Setenv("REMUX_TEST_CALLS", calls)
	bin := filepath.Join(dir, "gh")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nif [ \"$1\" = pr ]; then cat \"$REMUX_TEST_COMMENTS\"; else printf '%s\\n' \"$@\" >> \"$REMUX_TEST_CALLS\"; fi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := ghBin
	ghBin = bin
	t.Cleanup(func() { ghBin = old })
	os.WriteFile(comments, []byte(`{"comments":[]}`), 0o600)
	l := &Loop{Agent: "claude", Model: "sonnet", gh: GH{Slug: "o/r", Dir: dir}}
	pr := &Item{Number: 9, HeadOid: "abc123"}
	if err := l.noteBuild(context.Background(), 42, pr, "full"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"repos/o/r/issues/9/comments", "Claude · Sonnet", "Issue #42", "abc123", "medium effort"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in %s", want, b)
		}
	}
	os.WriteFile(comments, []byte(`{"comments":[{"body":"<!-- remux-build issue=42 scope=full model=sonnet head=abc123 -->"}]}`), 0o600)
	if err := l.noteBuild(context.Background(), 42, pr, "full"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(calls)
	if string(after) != string(b) {
		t.Fatal("duplicate model comment was posted")
	}
	l.Agent = "codex"
	if err := l.noteBuild(context.Background(), 42, pr, "full"); err != nil {
		t.Fatal(err)
	}
	after, _ = os.ReadFile(calls)
	if string(after) != string(b) {
		t.Fatal("Codex was attributed to Claude")
	}
}
