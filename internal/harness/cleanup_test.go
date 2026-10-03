package harness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanupMergedPR(t *testing.T) {
	for _, tc := range []struct {
		name, change string
		wantGone     bool
	}{
		{"merged head", "", true},
		{"dirty worktree", "dirty", false},
		{"new commit after merge", "commit", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			repo := filepath.Join(root, "repo")
			origin := filepath.Join(root, "origin.git")
			runGit(t, root, "init", "--bare", origin)
			runGit(t, root, "init", "-b", "main", repo)
			runGit(t, repo, "config", "user.name", "Test")
			runGit(t, repo, "config", "user.email", "test@example.com")
			if err := os.WriteFile(filepath.Join(repo, "file"), []byte("base"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, repo, "add", ".")
			runGit(t, repo, "commit", "-m", "base")
			runGit(t, repo, "remote", "add", "origin", origin)
			runGit(t, repo, "push", "origin", "main")
			runGit(t, repo, "checkout", "-b", "issue-9000000")
			if err := os.WriteFile(filepath.Join(repo, "file"), []byte("PR work"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, repo, "commit", "-am", "PR work")
			head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
			runGit(t, repo, "push", "origin", "issue-9000000", "HEAD:refs/pull/9000000/head")
			runGit(t, repo, "checkout", "main")
			wt := filepath.Join(repo, ".claude", "worktrees", "issue-9000000")
			runGit(t, repo, "worktree", "add", wt, "issue-9000000")
			switch tc.change {
			case "dirty":
				if err := os.WriteFile(filepath.Join(wt, "unsaved"), []byte("keep"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "commit":
				if err := os.WriteFile(filepath.Join(wt, "later"), []byte("keep"), 0o644); err != nil {
					t.Fatal(err)
				}
				runGit(t, wt, "add", ".")
				runGit(t, wt, "commit", "-m", "later work")
			}
			bin := filepath.Join(root, "bin")
			if err := os.Mkdir(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"gh", "tmux"} {
				body := "#!/bin/sh\nexit 0\n"
				if name == "gh" {
					body = "#!/bin/sh\nprintf '[]\\n'\n"
				}
				if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			old := ghBin
			ghBin = filepath.Join(bin, "gh")
			defer func() { ghBin = old }()
			l := Loop{Repo: repo, Session: "loop-test", gh: GH{Dir: repo, Slug: "test/repo"}}
			pr := Item{Number: 9000000, State: "MERGED", Head: "issue-9000000", HeadOid: head}
			err := l.cleanupMergedPR(context.Background(), pr)
			if tc.wantGone && err != nil {
				t.Fatal(err)
			}
			if !tc.wantGone && err == nil {
				t.Fatal("cleanup removed work that was not in the merged PR")
			}
			_, statErr := os.Stat(wt)
			if (statErr == nil) == tc.wantGone {
				t.Fatalf("worktree presence = %v, want gone = %v", statErr == nil, tc.wantGone)
			}
			_, remoteErr := exec.Command("git", "--git-dir", origin, "show-ref", "--verify", "refs/heads/issue-9000000").CombinedOutput()
			if (remoteErr != nil) != tc.wantGone {
				t.Fatalf("remote branch presence = %v, want gone = %v", remoteErr == nil, tc.wantGone)
			}
		})
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}
