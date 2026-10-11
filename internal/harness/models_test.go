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
