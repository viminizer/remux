package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The default branch moves under the PR and conflicts, then the tests fail.
// Each goes to a fix run, and the PR still merges without Kevin.
func TestFinishRepairs(t *testing.T) {
	root := t.TempDir()
	repo, origin := filepath.Join(root, "repo"), filepath.Join(root, "origin.git")
	runGit(t, root, "init", "--bare", "-b", "main", origin)
	runGit(t, root, "init", "-b", "main", repo)
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "config", "user.email", "test@example.com")
	write := func(dir, name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(repo, "file", "base")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "base")
	runGit(t, repo, "remote", "add", "origin", origin)
	runGit(t, repo, "push", "origin", "main")
	runGit(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	wt := filepath.Join(repo, ".claude", "worktrees", "issue-1")
	runGit(t, repo, "worktree", "add", "-b", "issue-1", wt, "main")
	write(wt, "file", "PR work")
	runGit(t, wt, "commit", "-am", "PR work")
	runGit(t, wt, "push", "origin", "issue-1")
	// main moves on with a change to the same line.
	write(repo, "file", "main work")
	runGit(t, repo, "commit", "-am", "main work")
	runGit(t, repo, "push", "origin", "main")

	calls := filepath.Join(root, "calls")
	gh := filepath.Join(root, "gh")
	os.WriteFile(gh, []byte(`#!/bin/sh
echo "$*" >> `+calls+`
case "$1 $2" in
"pr view") echo '{"number":1,"state":"OPEN","headRefName":"issue-1"}' ;;
*) echo '[]' ;;
esac
`), 0o755)
	old := ghBin
	ghBin = gh
	defer func() { ghBin = old }()

	l := &Loop{Repo: repo, gh: GH{Dir: repo, Slug: "o/r"}, set: Settings{Test: "test -f fixed", Mode: "personal"}}
	var got []string
	fix := func(conflict, problem string) bool {
		switch {
		case conflict != "":
			got = append(got, "conflict")
			runGit(t, wt, "merge", "-X", "ours", "--no-edit", conflict)
		case strings.Contains(problem, "The tests fail"):
			got = append(got, "tests")
			write(wt, "fixed", "")
			runGit(t, wt, "add", ".")
			runGit(t, wt, "commit", "-m", "fix tests")
		default:
			t.Fatalf("unexpected fix: %q", problem)
		}
		return true
	}
	if err := l.finish(context.Background(), 1, "t", "issue-1", wt, fix); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "conflict,tests" {
		t.Errorf("fix runs = %v, want conflict then tests", got)
	}
	b, _ := os.ReadFile(calls)
	if !strings.Contains(string(b), "pr merge 1 --squash") || strings.Contains(string(b), "needs-human") {
		t.Errorf("gh calls:\n%s", b)
	}
}
