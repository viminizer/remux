package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viminizer/remux/internal/tmux"
)

func openItem(n int, labels ...string) Item {
	i := item(n, labels...)
	i.State = "OPEN"
	return i
}

func TestTriageShortlistHonorsScopeAndOwnership(t *testing.T) {
	issues := []Item{
		openItem(1, "ready", "done:backend"),
		openItem(2, "status:review", "done:frontend"),
		openItem(3, "blocked", "blocked:external"),
		openItem(4, "status:blocked", "type:release-review"),
		openItem(5, "ready", "done:full"),
		openItem(6, "wip:frontend"),
		openItem(7, "by:loop-builder"),
		openItem(8, "needs-human"),
		openItem(9, "escalated"),
		openItem(10, "postponed"),
		openItem(11, "status:postponed"),
		openItem(12, "post-deployment"),
		openItem(13, "queue:handoff-held"),
		openItem(14, "type:tracker"),
		openItem(15, "type:specification"),
		{Number: 16, State: "CLOSED"},
	}
	got := triageCandidates(issues, "backend")
	if len(got) != 2 || got[0].Number != 2 || got[1].Number != 3 {
		t.Fatalf("backend shortlist = %+v", got)
	}
	got = triageCandidates(issues, "frontend")
	if len(got) != 2 || got[0].Number != 1 {
		t.Fatalf("frontend shortlist = %+v", got)
	}
}

// Replay the actual failure: ninety-five generic ready issues are all
// complete for backend, while a partial review issue needs a continuation.
func TestTriageUsesScopedSnapshot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	prompt := filepath.Join(root, "prompt")
	t.Setenv("REMUX_TEST_PROMPT", prompt)
	issuesFile := filepath.Join(root, "issues")
	t.Setenv("REMUX_TEST_ISSUES", issuesFile)
	var issues []Item
	for n := 1; n <= 95; n++ {
		issues = append(issues, openItem(n, "ready", "done:backend"))
	}
	issues = append(issues, openItem(120, "status:review", "done:frontend"))
	data, _ := json.Marshal(issues)
	os.WriteFile(issuesFile, data, 0o600)
	for name, script := range map[string]string{
		"gh":     "#!/bin/sh\ncase \"$1 $2\" in\n'issue list') cat \"$REMUX_TEST_ISSUES\" ;;\n*) echo '[]' ;;\nesac\n",
		"tmux":   "#!/bin/sh\nexit 0\n",
		"claude": "#!/bin/sh\ncat > \"$REMUX_TEST_PROMPT\"\necho triaged\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := ghBin
	ghBin = filepath.Join(root, "gh")
	t.Cleanup(func() { ghBin = old })
	l := &Loop{Agent: "claude", Model: "sonnet", Repo: root, Session: "loop-test", gh: GH{Dir: root, Slug: "o/r"}, Tmux: &tmux.Client{Bin: filepath.Join(root, "tmux")}, set: Settings{Instructions: "Preserve finance."}}
	if !l.triage(context.Background(), "backend") {
		t.Fatal("unfinished review work was not triaged")
	}
	b, err := os.ReadFile(prompt)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Generic ready labels across all scopes: 95", "Eligible ready issues for this scope: 0", "Unfinished issues to inspect for this scope: 1", `"number":120`, "after a merged partial PR", "open PR or active worker", "actual merged implementation and contract", "Preserve finance."} {
		if !strings.Contains(string(b), want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if strings.Contains(string(b), `"number":1,`) {
		t.Fatal("completed backend issue leaked into shortlist")
	}
	if l.triage(context.Background(), "backend") {
		t.Fatal("triage ignored cooldown")
	}
	os.Remove(prompt)
	data, _ = json.Marshal(issues[:95])
	os.WriteFile(issuesFile, data, 0o600)
	l.lastTriage = l.lastTriage.Add(-TriageEvery)
	if l.triage(context.Background(), "backend") {
		t.Fatal("triage paid for a run with only completed backend issues")
	}
	if _, err := os.Stat(prompt); !os.IsNotExist(err) {
		t.Fatal("agent launched with no unfinished scope work")
	}
}

func TestTriagePromptKeepsSessionRestrictions(t *testing.T) {
	l := &Loop{set: Settings{Instructions: "Default scope."}}
	p := l.triagePrompt("backend", []Item{openItem(12, "status:review")}, "Only billing.", "replace")
	if strings.Contains(p, "Default scope.") || !strings.Contains(p, "Only billing.") {
		t.Fatal("session override lost")
	}
}
