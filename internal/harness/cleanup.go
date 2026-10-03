package harness

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// cleanupMerged revisits merged PRs. A PR can be merged by a person or another
// loop after this review loop has handed it off, leaving its branch behind.
func (l *Loop) cleanupMerged(ctx context.Context) error {
	lock, err := os.OpenFile(filepath.Join(l.Repo, ".git", "remux-cleanup.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil // another review loop is doing this sweep
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	var prs []Item
	if err := l.gh.JSON(ctx, &prs, "pr", "list", "--state", "merged", "--limit", "1000",
		"--json", "number,state,headRefName,headRefOid"); err != nil {
		return err
	}
	branches, err := l.git(ctx, l.Repo, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return err
	}
	local := make(map[string]bool)
	for _, branch := range strings.Split(branches, "\n") {
		local[branch] = true
	}
	remote, err := l.git(ctx, l.Repo, "ls-remote", "--heads", "origin")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(remote, "\n") {
		if _, branch, ok := strings.Cut(line, "refs/heads/"); ok {
			local[branch] = true
		}
	}
	seen := make(map[string]bool)
	for _, pr := range prs {
		if !local[pr.Head] || seen[pr.Head] {
			continue
		}
		seen[pr.Head] = true
		if err := l.cleanupMergedPR(ctx, pr); err != nil {
			log.Printf("cleanup PR #%d (%s): %v", pr.Number, pr.Head, err)
		}
	}
	return nil
}

// cleanupMergedPR removes only branch content that is included in the merged
// PR head. It leaves dirty worktrees and branches with later commits intact.
func (l *Loop) cleanupMergedPR(ctx context.Context, pr Item) error {
	if pr.State != "MERGED" || pr.Head == "" || pr.HeadOid == "" {
		return fmt.Errorf("PR #%d has no confirmed merged head", pr.Number)
	}
	if open, err := l.gh.PRForBranch(ctx, pr.Head); err != nil {
		return err
	} else if open != nil {
		return nil // a branch name can be reused for a new PR
	}
	if active, err := l.branchActive(ctx, pr); err != nil {
		return err
	} else if active {
		return nil
	}
	// GitHub keeps refs/pull/N/head after merge. Fetching it lets us check a
	// squash-merged branch without assuming its commits are on default.
	if _, err := l.git(ctx, l.Repo, "fetch", "--quiet", "origin", fmt.Sprintf("refs/pull/%d/head", pr.Number)); err != nil {
		return err
	}
	if head, err := l.git(ctx, l.Repo, "rev-parse", "FETCH_HEAD"); err != nil || head != pr.HeadOid {
		return fmt.Errorf("merged PR #%d head changed", pr.Number)
	}
	branchRef := "refs/heads/" + pr.Head
	local, _ := l.git(ctx, l.Repo, "rev-parse", "--verify", branchRef)
	if local != "" {
		if _, err := l.git(ctx, l.Repo, "merge-base", "--is-ancestor", local, pr.HeadOid); err != nil {
			return fmt.Errorf("local branch %s has commits outside PR #%d", pr.Head, pr.Number)
		}
		if wt := l.findWorktree(ctx, pr.Head); wt != "" {
			root := filepath.Join(l.Repo, ".claude", "worktrees")
			root, err := filepath.EvalSymlinks(root)
			if err != nil {
				return err
			}
			wt, err = filepath.EvalSymlinks(wt)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, wt)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("worktree %s is outside the loop directory", wt)
			}
			dirty, err := l.git(ctx, wt, "status", "--porcelain")
			if err != nil || dirty != "" {
				return fmt.Errorf("worktree %s has unsaved changes", wt)
			}
			if _, err := l.git(ctx, l.Repo, "worktree", "remove", wt); err != nil {
				return err
			}
		}
		if _, err := l.git(ctx, l.Repo, "branch", "-D", pr.Head); err != nil {
			return err
		}
		log.Printf("removed merged PR #%d local branch and worktree: %s", pr.Number, pr.Head)
	}
	remote, err := l.git(ctx, l.Repo, "ls-remote", "--heads", "origin", branchRef)
	if err != nil || remote == "" {
		return err
	}
	sha := strings.Fields(remote)[0]
	if _, err := l.git(ctx, l.Repo, "fetch", "--quiet", "origin", branchRef); err != nil {
		return err
	}
	if fetched, err := l.git(ctx, l.Repo, "rev-parse", "FETCH_HEAD"); err != nil || fetched != sha {
		return fmt.Errorf("remote branch %s changed during cleanup", pr.Head)
	}
	if _, err := l.git(ctx, l.Repo, "merge-base", "--is-ancestor", sha, pr.HeadOid); err != nil {
		return fmt.Errorf("remote branch %s has commits outside PR #%d", pr.Head, pr.Number)
	}
	// The lease refuses to delete a branch that changed after our check.
	if _, err := l.git(ctx, l.Repo, "push", "--quiet", "--force-with-lease="+branchRef+":"+sha,
		"origin", ":"+branchRef); err != nil {
		return err
	}
	log.Printf("removed merged PR #%d remote branch: %s", pr.Number, pr.Head)
	return nil
}

func (l *Loop) branchActive(ctx context.Context, pr Item) (bool, error) {
	cmd := exec.CommandContext(ctx, "tmux", "list-sessions", "-F",
		"#{session_name} #{@loop_role} #{@loop_state} #{@loop_issue}")
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	issue := 0
	if n, _, ok := strings.Cut(strings.TrimPrefix(pr.Head, "issue-"), "-"); ok && strings.HasPrefix(pr.Head, "issue-") {
		issue, _ = strconv.Atoi(n)
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 4 || fields[0] == l.Session || !strings.HasPrefix(fields[0], "loop-") || fields[2] == "idle" {
			continue
		}
		n, _ := strconv.Atoi(fields[3])
		if n == pr.Number || (issue != 0 && n == issue) {
			return true, nil
		}
	}
	return false, nil
}
