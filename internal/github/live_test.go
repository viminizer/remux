package github

import (
	"context"
	"os"
	"testing"
	"time"
)

// The tests in this file talk to github.com through the real gh binary. They
// are the only way to know the JSON shapes above still match, and they are
// skipped by default because they need a network and a logged-in gh.
//
//	REMUX_GH_LIVE=1 go test ./internal/github/ -run Live -v
func live(t *testing.T) (*Client, context.Context) {
	t.Helper()
	if os.Getenv("REMUX_GH_LIVE") == "" {
		t.Skip("set REMUX_GH_LIVE=1 to run against github.com")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return New(), ctx
}

func TestLiveViewer(t *testing.T) {
	c, ctx := live(t)
	login, err := c.Viewer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if login == "" {
		t.Fatal("empty login")
	}
	t.Logf("viewer: %s", login)
}

func TestLiveRepos(t *testing.T) {
	c, ctx := live(t)
	repos, err := c.Repos(ctx, []string{
		"viminizer/remux",
		"apache/shardingsphere",
		"viminizer/definitely-not-a-real-repo-xyz",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 3 {
		t.Fatalf("got %d repos, want 3", len(repos))
	}
	for _, r := range repos {
		t.Logf("%-45s issues=%-4d prs=%-3d missing=%v", r.Full, r.Issues, r.PRs, r.Missing)
	}
	// One dead entry must not take the other two down with it.
	if repos[0].Missing || repos[1].Missing {
		t.Error("a real repo came back missing")
	}
	if !repos[2].Missing {
		t.Error("a deleted repo should come back missing, not error")
	}
}

func TestLiveIssuesAndPRs(t *testing.T) {
	c, ctx := live(t)
	const repo = "apache/shardingsphere" // big enough to exercise paging

	page, err := c.Issues(ctx, repo, IssuesAll, "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s page 1: %d issues, next=%q", repo, len(page.Issues), page.Next)
	if len(page.Issues) == 0 {
		t.Fatal("no issues on page 1")
	}
	if page.Next == "" {
		t.Error("a repo with 100+ open issues should have a second page")
	}
	// The whole reason this went to GraphQL: a full page is a full page of
	// issues, with no pull requests eating the slots.
	if len(page.Issues) != PageSize {
		t.Errorf("page 1 returned %d rows, want a full %d", len(page.Issues), PageSize)
	}
	for _, is := range page.Issues[:min(3, len(page.Issues))] {
		t.Logf("  #%-6d %-60.60s by %s", is.Number, is.Title, is.Author)
	}

	prs, err := c.PRs(ctx, repo, 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %d open PRs", repo, len(prs))
	for _, p := range prs[:min(3, len(prs))] {
		t.Logf("  #%-6d checks=%-8s review=%-18s conflicts=%v +%d-%d",
			p.Number, p.Checks, p.Review, p.Conflicts, p.Additions, p.Deletions)
	}
}

// The Repos count and the two list calls have to agree, or the chip on a card
// says one thing and the list under it says another.
func TestLiveCountsAgree(t *testing.T) {
	c, ctx := live(t)
	const repo = "viminizer/remux"

	repos, err := c.Repos(ctx, []string{repo})
	if err != nil {
		t.Fatal(err)
	}
	prs, err := c.PRs(ctx, repo, 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("graphql says %d issues / %d prs; pr list returned %d",
		repos[0].Issues, repos[0].PRs, len(prs))
	if repos[0].PRs != len(prs) {
		t.Errorf("PR count %d != listed PRs %d", repos[0].PRs, len(prs))
	}
}

// The repo screen opens on this view, so it is the one that has to be right.
func TestLiveIssuesMine(t *testing.T) {
	c, ctx := live(t)
	login, err := c.Viewer(ctx)
	if err != nil {
		t.Fatal(err)
	}

	page, err := c.Issues(ctx, "viminizer/shortlist", IssuesMine, login, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("mine: %d issues", len(page.Issues))
	for _, is := range page.Issues {
		t.Logf("  #%-5d %-50.50s assignees=%v comments=%d", is.Number, is.Title, is.Assignees, is.Comments)
		if is.Author != login && !contains(is.Assignees, login) {
			t.Errorf("#%d is neither mine nor assigned to me", is.Number)
		}
	}

	if len(page.Issues) == 0 {
		return
	}
	full, err := c.Issue(ctx, "viminizer/shortlist", page.Issues[0].Number)
	if err != nil {
		t.Fatal(err)
	}
	if full.Number != page.Issues[0].Number || full.Title == "" {
		t.Errorf("detail mismatch: %+v", full)
	}
	t.Logf("detail #%d: %d bytes of body", full.Number, len(full.Body))
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
