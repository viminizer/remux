package harness

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The loops make a worktree per issue and per review, and not every one ends
// in a merge that cleans it up: a run that stops on a blocker keeps its
// worktree to resume from, and agents have made their own. Measured on
// educenter after two days: 29 worktrees, 3.3 GB, 25 of them belonging to
// nothing open.
//
// sweep removes a worktree under .claude/worktrees only when losing it loses
// nothing: untouched for SweepIdle, no uncommitted changes, and its work safe
// on GitHub - every commit pushed, or its pull request merged or closed.
// Anything else stays.

// SweepEvery spaces out the sweeps; SweepIdle is how long a worktree must sit
// untouched, longer than any agent run, so one in use is never taken.
var (
	SweepEvery = time.Hour
	SweepIdle  = 2 * time.Hour
)

type worktree struct{ path, branch string }

func (l *Loop) sweep(ctx context.Context) {
	if time.Since(l.lastSweep) < SweepEvery {
		return
	}
	l.lastSweep = time.Now()
	if _, err := l.git(ctx, l.Repo, "fetch", "--quiet", "--prune", "origin"); err != nil {
		return
	}
	done := l.finishedBranches(ctx)
	removed := 0
	for _, wt := range l.worktrees(ctx) {
		if why := l.keep(ctx, wt, done); why != "" {
			continue
		}
		if _, err := l.git(ctx, l.Repo, "worktree", "remove", wt.path); err != nil {
			log.Printf("sweep: %v", err)
			continue
		}
		if wt.branch != "" {
			l.git(ctx, l.Repo, "branch", "-D", wt.branch)
		}
		removed++
	}
	l.git(ctx, l.Repo, "worktree", "prune")
	if removed > 0 {
		log.Printf("sweep: removed %d finished worktrees", removed)
	}
}

// keep says why a worktree must stay, or "" when it can go.
func (l *Loop) keep(ctx context.Context, wt worktree, done map[string]bool) string {
	if time.Since(lastTouched(wt.path)) < SweepIdle {
		return "in use"
	}
	if dirty, err := l.git(ctx, wt.path, "status", "--porcelain"); err != nil || dirty != "" {
		return "uncommitted changes"
	}
	if wt.branch != "" && done[wt.branch] {
		return ""
	}
	if ahead, err := l.git(ctx, wt.path, "log", "--oneline", "HEAD", "--not", "--remotes"); err != nil || ahead != "" {
		return "commits not on GitHub"
	}
	return ""
}

// worktrees lists the loop-made worktrees, never the main checkout.
func (l *Loop) worktrees(ctx context.Context) []worktree {
	out, err := l.git(ctx, l.Repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	// git prints resolved paths, so a repo reached through a symlink (macOS's
	// /var is one) must be resolved too, or nothing would match.
	repo := l.Repo
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	root := filepath.Join(repo, ".claude", "worktrees") + string(filepath.Separator)
	var all []worktree
	var cur *worktree
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			if cur != nil && strings.HasPrefix(cur.path, root) {
				all = append(all, *cur)
			}
			cur = &worktree{path: p}
		}
		if b, ok := strings.CutPrefix(line, "branch refs/heads/"); ok && cur != nil {
			cur.branch = b
		}
	}
	if cur != nil && strings.HasPrefix(cur.path, root) {
		all = append(all, *cur)
	}
	return all
}

// finishedBranches are the head branches of merged or closed pull requests.
func (l *Loop) finishedBranches(ctx context.Context) map[string]bool {
	var prs []struct {
		Head  string `json:"headRefName"`
		State string `json:"state"`
	}
	done := map[string]bool{}
	if err := l.gh.JSON(ctx, &prs, "pr", "list", "--state", "all", "--limit", "1000",
		"--json", "headRefName,state"); err != nil {
		return done
	}
	open := map[string]bool{}
	for _, p := range prs {
		if p.State == "OPEN" {
			open[p.Head] = true
		} else {
			done[p.Head] = true
		}
	}
	// A branch with a new open PR after an old merged one is still in use.
	for b := range open {
		delete(done, b)
	}
	return done
}

// lastTouched is the newest of the worktree's own files that git writes on
// every commit, checkout or edit-and-stage.
func lastTouched(path string) time.Time {
	var t time.Time
	gitdir := filepath.Join(path, ".git")
	if b, err := os.ReadFile(gitdir); err == nil {
		if d, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: "); ok {
			gitdir = d
		}
	}
	for _, p := range []string{path, filepath.Join(gitdir, "index"), filepath.Join(gitdir, "HEAD"), filepath.Join(gitdir, "logs", "HEAD")} {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(t) {
			t = fi.ModTime()
		}
	}
	return t
}
