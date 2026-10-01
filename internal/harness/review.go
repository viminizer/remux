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

func (l *Loop) reviewOnce(ctx context.Context) (bool, error) {
	prs, err := l.gh.PRs(ctx, "needs-review")
	if err != nil {
		return false, err
	}
	var pr *Item
	for i, p := range prs {
		if !p.HasPrefix("wip:") && !p.Has("needs-human") {
			pr = &prs[i]
			break
		}
	}
	if pr == nil {
		return false, nil
	}
	n := pr.Number
	if err := l.gh.AddLabels(ctx, n, "wip:review"); err != nil {
		return false, err
	}
	defer l.gh.RemoveLabel(context.WithoutCancel(ctx), n, "wip:review")

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
	thread, err := l.gh.Thread(ctx, "pr", n)
	if err != nil {
		return false, err
	}

	l.agent(ctx, wt, Prompt(Run{
		Role: "review", Slug: l.gh.Slug, Number: n, Branch: pr.Head, Worktree: wt,
		Scope: "review", Mode: l.set.Mode, Settings: l.set, Thread: thread,
		Instructions: l.get(ctx, "@loop_instr"), InstrMode: l.get(ctx, "@loop_mode"),
	}))

	after, err := l.gh.Issue(ctx, n)
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
	if _, err := l.git(ctx, wt, "push", "--quiet", "origin", "HEAD:"+branch); err != nil {
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

	// The worktree goes first: a branch that is checked out somewhere cannot
	// be deleted, and --delete-branch would fail on it.
	if err := l.removeWorktree(ctx, wt, branch); err != nil {
		return l.stuck(ctx, n, title, "Could not remove the worktree before merging: "+err.Error())
	}
	// --match-head-commit: merge exactly what was tested, not whatever was
	// pushed in between.
	if _, err := l.gh.run(ctx, "pr", "merge", fmt.Sprint(n), "--squash", "--delete-branch",
		"--match-head-commit", sha); err != nil {
		return l.stuck(ctx, n, title, "Could not merge: "+err.Error())
	}
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
