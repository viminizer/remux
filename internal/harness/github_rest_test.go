package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRESTFallbackAfterGraphLimit(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "gh")
	until := time.Now().Add(10 * time.Minute)
	script := fmt.Sprintf(`#!/bin/sh
echo "$*" >> '%s'
case "$1 $2" in
"issue view") echo 'GraphQL: API rate limit already exceeded' >&2; exit 1 ;;
"api graphql") printf 'HTTP/2.0 200 OK\nX-RateLimit-Remaining: 0\nX-RateLimit-Reset: %d\n\n'; echo '{"data":{"rateLimit":{"remaining":0,"resetAt":"%s"}}}' ;;
"api repos/o/r/issues/7") echo '{"number":7,"state":"open","title":"real issue","html_url":"https://github.com/o/r/issues/7","labels":[{"name":"ready"}]}' ;;
"api repos/o/r/pulls/8") echo '{"number":8,"state":"open","draft":true,"head":{"ref":"issue-7-backend","sha":"abc"},"labels":[{"name":"needs-review"}]}' ;;
*) echo 'unexpected command' >&2;exit 3 ;;
esac
`, log, until.Unix(), until.UTC().Format(time.RFC3339))
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	old := ghBin
	ghBin = bin
	defer func() { ghBin = old }()
	g := GH{Dir: dir, Slug: "o/r"}
	ctx := context.Background()
	it, err := g.Issue(ctx, 7)
	if err != nil || it.State != "OPEN" || !it.Has("ready") || it.URL != "https://github.com/o/r/issues/7" {
		t.Fatalf("issue=%+v err=%v", it, err)
	}
	pr, err := g.PR(ctx, 8)
	if err != nil || pr.HeadOid != "abc" || !pr.IsDraft || pr.Head != "issue-7-backend" {
		t.Fatalf("pr=%+v err=%v", pr, err)
	}
	b, _ := os.ReadFile(log)
	if strings.Contains(string(b), "pr view") {
		t.Fatal("exhausted GraphQL was retried", string(b))
	}
	g.setBudget("GraphQL", time.Now().Add(-time.Second))
	// Reset permits the next normal GraphQL command rather than sticky REST.
	_, _ = g.PR(ctx, 8)
	b, _ = os.ReadFile(log)
	if !strings.Contains(string(b), "pr view 8") {
		t.Fatal("GraphQL did not resume after reset")
	}
}

func TestRESTPaginationAndIssueFiltering(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "gh")
	first := []map[string]any{{"number": 1, "state": "open", "pull_request": map[string]any{"url": "pr"}}}
	for i := 2; i <= 100; i++ {
		first = append(first, map[string]any{"number": i, "state": "open", "labels": []any{map[string]any{"name": "ready"}}})
	}
	b, _ := json.Marshal(first)
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in
*'page=1&'*) echo '%s' ;;
*'page=2&'*) echo '[{"number":101,"state":"open","labels":[{"name":"ready"}]}]' ;;
*) exit 4 ;;
esac
`, b)
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	old := ghBin
	ghBin = bin
	defer func() { ghBin = old }()
	g := GH{Dir: dir, Slug: "o/r"}
	g.setBudget("GraphQL", time.Now().Add(time.Minute))
	items, err := g.Issues(context.Background(), "ready")
	if err != nil || len(items) != 100 || items[0].Number != 2 || items[99].Number != 101 {
		t.Fatalf("items=%v err=%v", items, err)
	}
}

func TestBothBudgetsWaitWithoutRequests(t *testing.T) {
	dir := t.TempDir()
	g := GH{Dir: dir, Slug: "o/r"}
	g.setBudget("GraphQL", time.Now().Add(time.Minute))
	g.setBudget("REST", time.Now().Add(time.Minute))
	_, err := g.PR(context.Background(), 8)
	if !githubLimited(err) {
		t.Fatalf("expected rate wait, got %v", err)
	}
	if rateError(fmt.Errorf("HTTP 403: Resource not accessible by integration")) {
		t.Fatal("permission error misclassified")
	}
	now := time.Now()
	until := retryAt(map[string]string{"retry-after": "20"}, now)
	if until.Sub(now) != 21*time.Second {
		t.Fatal("Retry-After ignored")
	}
}

func TestRESTMergeKeepsTestedHeadGuard(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "gh")
	calls := filepath.Join(dir, "calls")
	script := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> '%s'\necho '{\"merged\":false,\"message\":\"head mismatch\"}'\n", calls)
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	old := ghBin
	ghBin = bin
	defer func() { ghBin = old }()
	g := GH{Dir: dir, Slug: "o/r"}
	g.setBudget("GraphQL", time.Now().Add(time.Minute))
	_, err := g.run(context.Background(), "pr", "merge", "8", "--squash", "--match-head-commit", "tested-sha")
	if err == nil {
		t.Fatal("merge=false accepted")
	}
	b, _ := os.ReadFile(calls)
	if !strings.Contains(string(b), "sha=tested-sha") {
		t.Fatal("tested head guard lost")
	}
	_, err = g.run(context.Background(), "pr", "ready", "8")
	if !githubLimited(err) {
		t.Fatal("GraphQL-only ready transition must wait", err)
	}
}

func TestRecoveryScanSharedAcrossWorktrees(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "file"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "base")
	wt := filepath.Join(t.TempDir(), "worker")
	runGit(t, repo, "worktree", "add", "-b", "worker", wt)
	a, b := Loop{Repo: repo}, Loop{Repo: wt}
	ctx := context.Background()
	if ok, err := a.recoveryTurn(ctx, "backend"); err != nil || !ok {
		t.Fatal("first worker did not acquire scan", err)
	}
	if ok, err := b.recoveryTurn(ctx, "backend"); err != nil || ok {
		t.Fatal("duplicate expensive scan allowed", err)
	}
	b.lastRecovery = time.Time{}
	if ok, err := b.recoveryTurn(ctx, "frontend"); err != nil || !ok {
		t.Fatal("independent scope scan blocked", err)
	}
}

func TestSecondaryLimitAndPermissionDoNotFallBack(t *testing.T) {
	for _, message := range []string{"GraphQL: secondary rate limit", "GraphQL: Resource not accessible by integration"} {
		t.Run(message, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "gh")
			calls := filepath.Join(dir, "calls")
			script := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> '%s'\necho '%s' >&2\nexit 1\n", calls, message)
			if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			old := ghBin
			ghBin = bin
			defer func() { ghBin = old }()
			g := GH{Dir: dir, Slug: "o/r"}
			_, err := g.PR(context.Background(), 8)
			if err == nil {
				t.Fatal("failure lost")
			}
			b, _ := os.ReadFile(calls)
			if strings.Contains(string(b), "api ") {
				t.Fatal("unexpected alternate API request", string(b))
			}
		})
	}
}

func TestRESTReviewShapePreservesAuthorAndCommit(t *testing.T) {
	r := projectREST(map[string]any{"body": "REVIEW COMPLETE", "author_association": "OWNER", "commit_id": "abc", "user": map[string]any{"login": "kevin"}, "state": "COMMENTED"}, "body,author,authorAssociation,commit,state")
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var p priorReview
	if err = json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if p.Commit.OID != "abc" || p.AuthorAssociation != "OWNER" || !completedReview(p.Body) {
		t.Fatalf("review metadata lost: %+v", p)
	}
}

func TestRESTOrphanAdoptionKeepsCreationAge(t *testing.T) {
	dir := t.TempDir()
	bin, calls := filepath.Join(dir, "gh"), filepath.Join(dir, "calls")
	now := time.Now()
	script := fmt.Sprintf(`#!/bin/sh
echo "$*" >> '%s'
case "$2" in
repos/o/r/pulls\?*) echo '[{"number":8,"state":"open","draft":true,"head":{"ref":"issue-7-backend"},"labels":[],"created_at":"%s"},{"number":9,"state":"open","draft":true,"head":{"ref":"issue-9-backend"},"labels":[],"created_at":"%s"}]' ;;
-X) echo '{}' ;;
*) exit 3 ;;
esac
`, calls, now.UTC().Format(time.RFC3339), now.Add(-2*OrphanAge).UTC().Format(time.RFC3339))
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	old := ghBin
	ghBin = bin
	defer func() { ghBin = old }()
	g := GH{Dir: dir, Slug: "o/r"}
	g.setBudget("GraphQL", now.Add(time.Minute))
	l := Loop{gh: g}
	l.adoptOrphans(context.Background())
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "issues/8/labels") {
		t.Fatal("fresh draft was adopted while its builder is still settling", string(b))
	}
	if !strings.Contains(string(b), "issues/9/labels") {
		t.Fatal("old orphan was not adopted", string(b))
	}
}

func TestRESTSecondaryLimitPausesBothTransports(t *testing.T) {
	for _, graphLimited := range []bool{false, true} {
		t.Run(fmt.Sprint(graphLimited), func(t *testing.T) {
			dir := t.TempDir()
			bin, calls := filepath.Join(dir, "gh"), filepath.Join(dir, "calls")
			script := fmt.Sprintf(`#!/bin/sh
echo "$*" >> '%s'
printf 'HTTP/2.0 403 Forbidden\nRetry-After: 120\nX-RateLimit-Remaining: 100\n\n'
echo '{"message":"You have exceeded a secondary rate limit"}'
echo 'HTTP 403: You have exceeded a secondary rate limit' >&2
exit 1
`, calls)
			if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			old := ghBin
			ghBin = bin
			defer func() { ghBin = old }()
			g := GH{Dir: dir, Slug: "o/r"}
			graphReset := time.Now().Add(10 * time.Minute)
			if graphLimited {
				g.setBudget("GraphQL", graphReset)
			}
			before := time.Now()
			if err := g.Comment(context.Background(), 8, "review handoff"); !githubLimited(err) {
				t.Fatal("secondary limit must defer", err)
			}
			budget := g.budget()
			if budget.Graph.Before(before.Add(120*time.Second)) || budget.REST.Before(before.Add(120*time.Second)) {
				t.Fatalf("both transports must respect Retry-After: %+v", budget)
			}
			if graphLimited && budget.Graph.Before(graphReset) {
				t.Fatal("secondary pause shortened an existing primary cooldown")
			}
			if _, err := g.PR(context.Background(), 8); !githubLimited(err) {
				t.Fatal("PR polling must wait", err)
			}
			b, _ := os.ReadFile(calls)
			if len(strings.Split(strings.TrimSpace(string(b)), "\n")) != 1 {
				t.Fatal("polling issued a request during secondary cooldown", string(b))
			}
		})
	}
}
