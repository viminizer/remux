package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// TestTimeout caps the test command a pull request has to pass.
var TestTimeout = 30 * time.Minute

// MergeRetry is the wait between merge attempts while GitHub is still working
// out whether a just-pushed PR can merge.
var MergeRetry = 15 * time.Second

func (l *Loop) reviewOnce(ctx context.Context) (bool, error) {
	prs, err := l.gh.PRs(ctx, "needs-review")
	if err != nil {
		return false, err
	}
	// Re-read before claiming, for the same lag the build loop measured.
	var pr *Item
	for _, p := range prs {
		if p.HasPrefix("wip:") || p.HasPrefix("by:") || p.Has("needs-human") {
			continue
		}
		fresh, err := l.gh.PR(ctx, p.Number)
		if err != nil {
			return false, err
		}
		if fresh.State == "OPEN" && fresh.Has("needs-review") && !fresh.HasPrefix("wip:") && !fresh.HasPrefix("by:") && !fresh.Has("needs-human") {
			pr = &fresh
			break
		}
	}
	if pr == nil {
		return false, nil
	}
	n := pr.Number
	// Several review loops can share a repo, so a PR is claimed the same way
	// the build loops claim an issue.
	won, err := l.claim(ctx, "pr", n, "wip:review")
	if err != nil || !won {
		return !won && err == nil, err
	}
	defer func() {
		c := context.WithoutCancel(ctx)
		l.gh.RemoveLabel(c, n, "wip:review")
		l.gh.RemoveLabel(c, n, "by:"+l.Session)
	}()

	l.working(ctx, n, pr.Title)
	log.Printf("── review PR #%d: %s", n, pr.Title)

	if _, err := l.git(ctx, l.Repo, "fetch", "--quiet", "origin", pr.Head); err != nil {
		return true, l.stuck(ctx, n, pr.Title, "Could not fetch the branch: "+err.Error())
	}
	wt, err := l.worktree(ctx, pr.Head, "origin/"+pr.Head)
	if err != nil {
		return true, l.stuck(ctx, n, pr.Title, "Could not create the worktree: "+err.Error())
	}
	// A worktree left from the build run may be behind what was pushed since.
	if _, err := l.git(ctx, wt, "merge", "--ff-only", "--quiet", "origin/"+pr.Head); err != nil {
		return true, l.stuck(ctx, n, pr.Title, "The local branch has moved away from the pushed one: "+err.Error())
	}
	// Bring the branch up to date with the default branch first. The build
	// loops open many PRs from the same base, and once one merges the rest
	// stop merging cleanly - measured on educenter, 6 of the first 8 reviews
	// ended at "the merge commit cannot be cleanly created". A clean merge is
	// done here; a conflict becomes the first part of the review.
	conflict := ""
	if def, err := l.defaultBranch(ctx); err == nil {
		if _, err := l.git(ctx, wt, "merge", "--no-edit", "--quiet", def); err != nil {
			l.git(ctx, wt, "merge", "--abort")
			conflict = def
		}
	}
	thread, err := l.gh.Thread(ctx, "pr", n)
	if err != nil {
		return false, err
	}

	_, summary := l.agent(ctx, wt, Prompt(Run{
		Role: "review", Slug: l.gh.Slug, Number: n, Branch: pr.Head, Worktree: wt,
		Scope: "review", Mode: l.set.Mode, Settings: l.set, Thread: thread, Conflict: conflict,
		Instructions: l.get(ctx, "@loop_instr"), InstrMode: l.get(ctx, "@loop_mode"),
	}))

	if l.limited() {
		return true, nil // still needs-review; tried again after the reset
	}
	// The review is posted by the loop, not left to the agent, so every
	// reviewed PR says what the review found - "no real issues" too. It goes
	// in as a GitHub review, so it sits in the PR's review timeline.
	if summary == "" {
		summary = "The review finished, but the agent left no summary. See the loop's log."
	}
	if err := l.gh.Review(ctx, n, summary); err != nil {
		log.Printf("review comment: %v", err)
	}

	after, err := l.gh.PR(ctx, n)
	if err != nil {
		return true, err
	}
	if after.Has("needs-human") {
		l.gh.RemoveLabel(ctx, n, "needs-review")
		l.event(ctx, "stuck", n, pr.Title)
		return true, nil
	}
	return true, l.finish(ctx, n, pr.Title, pr.Head, wt)
}

// finish is everything after the review that must not depend on the agent
// having done it right: the work is pushed, the tests pass, and only then does
// the pull request move on - merged in personal mode, handed to the supervisor
// in company mode.
func (l *Loop) finish(ctx context.Context, n int, title, branch, wt string) error {
	if dirty, _ := l.git(ctx, wt, "status", "--porcelain"); dirty != "" {
		return l.stuck(ctx, n, title, "The review left uncommitted changes in `"+wt+"`.")
	}
	// main may have moved again during the review.
	if def, err := l.defaultBranch(ctx); err == nil {
		if _, err := l.git(ctx, wt, "merge-base", "--is-ancestor", def, "HEAD"); err != nil {
			if _, err := l.git(ctx, wt, "merge", "--no-edit", "--quiet", def); err != nil {
				l.git(ctx, wt, "merge", "--abort")
				return l.stuck(ctx, n, title, "The branch conflicts with "+def+" and the review did not resolve it: "+err.Error())
			}
		}
	}
	// With lease: the branch is the loop's own, and an agent may have
	// rebased it, but a push from anywhere else since the fetch still wins.
	if _, err := l.git(ctx, wt, "push", "--quiet", "--force-with-lease", "origin", "HEAD:"+branch); err != nil {
		return l.stuck(ctx, n, title, "Could not push the branch: "+err.Error())
	}
	if l.set.Test == "" {
		return l.stuck(ctx, n, title, "There is no test command in "+SettingsFile+
			", so nothing can merge. Add one, then resume.")
	}
	if out, err := l.test(ctx, wt); err != nil {
		return l.stuck(ctx, n, title, "Tests still fail after the review:\n\n```\n"+tail(out, 30)+"\n```")
	}
	sha, err := l.git(ctx, wt, "rev-parse", "HEAD")
	if err != nil {
		return err
	}

	if _, err := l.gh.run(ctx, "pr", "ready", fmt.Sprint(n)); err != nil && !strings.Contains(err.Error(), "already") {
		return l.stuck(ctx, n, title, "Could not mark the pull request ready: "+err.Error())
	}
	l.gh.RemoveLabel(ctx, n, "needs-review")

	if l.set.Mode == "company" {
		if who := l.supervisor(ctx); who != "" {
			if _, err := l.gh.run(ctx, "pr", "edit", fmt.Sprint(n), "--add-reviewer", who); err != nil {
				log.Printf("could not request a review from %s: %v", who, err)
			}
		}
		l.event(ctx, "sent", n, title)
		return nil
	}

	// --match-head-commit: merge exactly what was tested, not whatever was
	// pushed in between. Retried, because GitHub works out whether a PR can
	// merge after each push, and asking straight away can find it unsure.
	var err2 error
	for try := 0; try < 4; try++ {
		if _, err2 = l.gh.run(ctx, "pr", "merge", fmt.Sprint(n), "--squash", "--match-head-commit", sha); err2 == nil ||
			!strings.Contains(err2.Error(), "not mergeable") {
			break
		}
		sleep(ctx, MergeRetry)
	}
	if err2 != nil {
		return l.stuck(ctx, n, title, "Could not merge: "+err2.Error())
	}
	// Cleanup only after the merge, so a failed merge keeps its worktree.
	if err := l.removeWorktree(ctx, wt, branch); err != nil {
		log.Printf("cleanup: %v", err)
	}
	l.git(ctx, l.Repo, "push", "--quiet", "origin", "--delete", branch)
	l.event(ctx, "merged", n, title)
	return nil
}

func (l *Loop) test(ctx context.Context, wt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, TestTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", l.set.Test)
	cmd.Dir = wt
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// supervisor names who reviews in company mode. An empty answer with no error
// means CODEOWNERS covers it: GitHub asks the owners itself when a draft is
// marked ready.
func (l *Loop) supervisor(ctx context.Context) string {
	for _, p := range []string{"CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"} {
		if _, err := os.Stat(filepath.Join(l.Repo, p)); err == nil {
			return ""
		}
	}
	me, _ := l.gh.run(ctx, "api", "user", "-q", ".login")
	if who := recentReviewer(l.history(ctx), strings.TrimSpace(string(me))); who != "" {
		return who
	}
	return l.set.Supervisor
}

type mergedPR struct {
	MergedBy struct {
		Login string `json:"login"`
	} `json:"mergedBy"`
	Reviews []struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
	} `json:"reviews"`
}

func (l *Loop) history(ctx context.Context) []mergedPR {
	out, err := l.gh.run(ctx, "pr", "list", "--state", "merged", "--limit", "20", "--json", "mergedBy,reviews")
	if err != nil {
		return nil
	}
	var prs []mergedPR
	json.Unmarshal(out, &prs)
	return prs
}

// recentReviewer is whoever reviewed or merged the most recent pull requests,
// newest first, never Kevin himself.
func recentReviewer(prs []mergedPR, me string) string {
	for _, p := range prs {
		for i := len(p.Reviews) - 1; i >= 0; i-- {
			if who := p.Reviews[i].Author.Login; who != "" && who != me {
				return who
			}
		}
		if who := p.MergedBy.Login; who != "" && who != me {
			return who
		}
	}
	return ""
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
