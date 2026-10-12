package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequeueReplacementScopes(t *testing.T) {
	for _, branch := range []string{"issue-12", "issue-12-backend", "issue-12-frontend", "issue-123", "unrelated"} {
		t.Run(branch, func(t *testing.T) {
			root := t.TempDir()
			calls := filepath.Join(root, "calls")
			fake := filepath.Join(root, "gh")
			body := fmt.Sprintf(`#!/bin/sh
case "$1 $2" in
"issue view") echo '{"number":12,"state":"OPEN","labels":[]}' ;;
"pr list") echo '[{"state":"OPEN","headRefName":"%s"}]' ;;
"api -X") echo "$*" >> '%s' ;;
esac
`, branch, calls)
			if err := os.WriteFile(fake, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
			old := ghBin
			ghBin = fake
			defer func() { ghBin = old }()
			l := Loop{gh: GH{Dir: root}}
			did, err := l.requeueMergedPR(context.Background(), Item{Number: 1, State: "MERGED", Head: "issue-12-backend"})
			want := branch == "issue-123" || branch == "unrelated"
			if err != nil || did != want {
				t.Fatalf("requeued = %v, err = %v, want %v", did, err, want)
			}
			out, _ := os.ReadFile(calls)
			if strings.Contains(string(out), "labels[]=ready") != want {
				t.Fatalf("label calls = %q", out)
			}
		})
	}
}

func TestQueueLockAcrossWorktrees(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "base")
	wt := filepath.Join(t.TempDir(), "worker")
	runGit(t, repo, "worktree", "add", "-b", "worker", wt)
	ctx := context.Background()
	cleanup := Loop{Repo: repo}
	builder := Loop{Repo: wt}
	unlock, err := cleanup.queueLock(ctx)
	if err != nil || unlock == nil {
		t.Fatalf("cleanup lock: %v", err)
	}
	defer unlock()
	// A claimant and a recovery pass must yield before any external calls.
	if won, err := builder.claim(ctx, "issue", 12, "wip:backend"); err != nil || won {
		t.Fatalf("claim during cleanup = %v, %v", won, err)
	}
	if did, err := builder.resumeMergedPR(ctx, Item{State: "MERGED", Head: "issue-12-backend"}); err != nil || did {
		t.Fatalf("recovery during cleanup = %v, %v", did, err)
	}
}
