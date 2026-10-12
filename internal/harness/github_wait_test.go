package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMergeRateWaitKeepsReviewQueue(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	origin := filepath.Join(root, "origin.git")
	runGit(t, root, "init", "--bare", "-b", "main", origin)
	runGit(t, root, "init", "-b", "main", repo)
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "file"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "base")
	runGit(t, repo, "remote", "add", "origin", origin)
	runGit(t, repo, "push", "origin", "main")
	runGit(t, repo, "checkout", "-b", "work")
	runGit(t, repo, "push", "origin", "work")
	calls := filepath.Join(root, "calls")
	bin := filepath.Join(root, "gh")
	until := time.Now().Add(time.Minute)
	script := fmt.Sprintf(`#!/bin/sh
echo "$*" >> '%s'
case "$1 $2" in
"pr merge") echo 'GraphQL: API rate limit already exceeded' >&2;exit 1 ;;
"api graphql") echo '{"data":{"rateLimit":{"remaining":0,"resetAt":"%s"}}}' ;;
"api -X") printf 'HTTP/2.0 403 Forbidden\nX-RateLimit-Remaining: 0\nX-RateLimit-Reset: %d\n\n';echo 'API rate limit exceeded' >&2;exit 1 ;;
*) echo '{}' ;;
esac
`, calls, until.UTC().Format(time.RFC3339), until.Unix())
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	old := ghBin
	ghBin = bin
	defer func() { ghBin = old }()
	l := Loop{Repo: repo, gh: GH{Dir: repo, Slug: "o/r"}, set: Settings{Mode: "personal", Test: "true"}}
	_, _, err := l.check(context.Background(), 8, "test", "work", repo)
	if !githubLimited(err) {
		t.Fatalf("quota failure must defer: %v", err)
	}
	b, _ := os.ReadFile(calls)
	if strings.Contains(string(b), "DELETE") || strings.Contains(string(b), "needs-human") {
		t.Fatal("waiting PR was removed from review queue or escalated", string(b))
	}
}
