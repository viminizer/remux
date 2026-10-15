package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIssueNumber(t *testing.T) {
	for name, want := range map[string]int{
		"educenter-2059-pg": 2059, ".educenter-2018": 2018, "issue-245": 245, "issue-246-backend": 246,
		"1968-backend": 1968, "educenter-pr1945.json": 1945, "educenter187-check.log": 187,
		"1258-20261010-a876a06b": 1258, "educenter-shared": 0, "educenter-supervise-1016": 0,
		"educenter-supervision-20261014": 0, "observations": 0, ".educenter-evidence": 0,
	} {
		if got := issueNumber(name, "educenter"); got != want {
			t.Errorf("%s: got %d, want %d", name, got, want)
		}
	}
}

func TestLeftoverPaths(t *testing.T) {
	home, tmp, repo := t.TempDir(), t.TempDir(), t.TempDir()
	sweepHome, sweepTmp = home, tmp
	t.Cleanup(func() { sweepHome, _ = os.UserHomeDir(); sweepTmp = "/tmp" })
	share := filepath.Join(home, ".local", "share", "educenter")
	for _, d := range []string{
		filepath.Join(share, "observations", "2059-backend"),
		filepath.Join(share, "observations", "notes"), // names no issue, inside a looked-into folder: left alone
		filepath.Join(share, "issue-245"),
		filepath.Join(home, ".educenter-2018"),
		filepath.Join(home, ".educenter-shared", "logs"), // holds nothing numbered: offered whole
		filepath.Join(home, ".other-1"),
		filepath.Join(tmp, "educenter-supervise-state"),
		filepath.Join(repo, ".claude", "worktrees", "issue-7-frontend"),
		filepath.Join(repo, ".claude", "worktrees", "issue-8-frontend"),
	} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(tmp, "educenter-176-pr.md"), nil, 0o600)
	os.WriteFile(filepath.Join(tmp, "unrelated.txt"), nil, 0o600)
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(filepath.Join(home, ".educenter-2018"), old, old)

	registered := map[string]bool{filepath.Join(repo, ".claude", "worktrees", "issue-8-frontend"): true}
	got := map[string]leftover{}
	for _, x := range leftoverPaths(repo, "educenter", registered, time.Now()) {
		got[strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(x.ID, home), tmp), repo)] = x
	}
	want := map[string]int{
		"/.local/share/educenter/observations/2059-backend": 2059, "/.local/share/educenter/issue-245": 245,
		"/.educenter-2018": 2018, "/.educenter-shared": 0, "/educenter-supervise-state": 0,
		"/educenter-176-pr.md": 176, "/.claude/worktrees/issue-7-frontend": 7,
	}
	if len(got) != len(want) {
		t.Errorf("got %v", got)
	}
	for p, n := range want {
		if x, ok := got[p]; !ok || x.Issue != n {
			t.Errorf("%s: got %+v, want issue %d", p, x, n)
		}
	}
	if !got["/.educenter-2018"].old || got["/.local/share/educenter/issue-245"].old {
		t.Errorf("age is read from the modification time")
	}
}

func TestLeftoverProcessesAndContainers(t *testing.T) {
	dirs := []leftover{{Kind: "path", ID: "/h/.educenter-2018", Issue: 2018}, {Kind: "path", ID: "/r/.claude/worktrees/issue-225-frontend", Issue: 225}}
	ps := strings.Join([]string{
		"14374 /usr/bin/java -Dserver.port=18018 -jar /h/.educenter-2018/educenter-2018.jar",
		"7450 node /r/.claude/worktrees/issue-225-frontend/node_modules/.bin/vite --port 5225",
		"8000 java -jar /h/.educenter-20180/x.jar", // a longer name is another folder
		"66623 codex exec -C /r/.claude/worktrees/issue-225-frontend -o x",
		"53213 /Users/mac/.local/bin/remux loop --repo /h/.educenter-2018",
		"99 java -jar /h/.educenter-2018/self.jar",
	}, "\n")
	got := leftoverProcesses(ps, dirs, 99)
	if len(got) != 2 || got[0].ID != "14374" || got[0].Issue != 2018 || got[1].ID != "7450" || got[1].Issue != 225 {
		t.Errorf("processes: %+v", got)
	}

	docker := "educenter-2059-pg\tUp 7 minutes\tpostgres:18.6\neducenter-shared-pg\tUp 2 days\tpostgres:18.6\npulse-redis-1\tCreated\tredis:8\n"
	cs := leftoverContainers(docker, "educenter")
	if len(cs) != 2 || cs[0].Issue != 2059 || cs[1].Issue != 0 {
		t.Errorf("containers: %+v", cs)
	}
}

func TestJudgeAndAgentAnswers(t *testing.T) {
	items := []leftover{
		{Kind: "path", ID: "/a", Issue: 1, old: true},
		{Kind: "path", ID: "/b", Issue: 2, old: true},
		{Kind: "path", ID: "/c", Issue: 3},
		{Kind: "container", ID: "x-4-pg", Issue: 4},
		{Kind: "process", ID: "5", Issue: 1},
		{Kind: "path", ID: "/shared"},
		{Kind: "container", ID: "x-shared-pg"},
	}
	decided, ask := judge(items, map[int]bool{1: true, 3: true, 4: true}, map[int]bool{4: true})
	remove := map[string]bool{}
	for _, v := range decided {
		remove[v.ID] = v.Remove
	}
	want := map[string]bool{"/a": true, "/b": false, "/c": false, "x-4-pg": false, "5": true}
	for id, r := range want {
		if remove[id] != r {
			t.Errorf("%s: remove=%v, want %v", id, remove[id], r)
		}
	}
	if len(ask) != 2 {
		t.Fatalf("ask: %+v", ask)
	}

	out := "REMOVE path /shared - old notes\n`KEEP container x-shared-pg - shared database`\nREMOVE path /etc - not offered\n"
	vs := parseDecisions(out, ask)
	if len(vs) != 2 || !vs[0].Remove || vs[0].ID != "/shared" || vs[1].Remove {
		t.Errorf("decisions: %+v", vs)
	}
}

func TestTrashKeepsThenPurges(t *testing.T) {
	home, src := t.TempDir(), t.TempDir()
	sweepHome = home
	t.Cleanup(func() { sweepHome, _ = os.UserHomeDir() })
	p := filepath.Join(src, "issue-9", "locked")
	os.MkdirAll(p, 0o700)
	os.Chmod(p, 0) // agents leave folders like this

	week := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	if err := toTrash(filepath.Join(src, "issue-9"), week); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(src, "issue-9")); !os.IsNotExist(err) {
		t.Fatal("the folder should have moved")
	}
	purgeTrash(week.Add(6 * 24 * time.Hour))
	if entries, _ := os.ReadDir(trashDir()); len(entries) != 1 {
		t.Fatal("a folder younger than TrashKeep stays")
	}
	purgeTrash(week.Add(8 * 24 * time.Hour))
	if entries, _ := os.ReadDir(trashDir()); len(entries) != 0 {
		t.Fatalf("an old holding folder goes, even with locked folders inside: %v", entries)
	}
}
