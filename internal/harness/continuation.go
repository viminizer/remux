package harness

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"time"
)

var issueBranch = regexp.MustCompile(`^issue-([1-9][0-9]*)(?:-([a-z0-9]+(?:-[a-z0-9]+)*))?$`)

func branchWork(branch string) (int, string, bool) {
	m := issueBranch.FindStringSubmatch(branch)
	if m == nil {
		return 0, "", false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, "", false
	}
	scope := m[2]
	if scope == "" {
		scope = "full"
	}
	return n, scope, true
}

func queueExcluded(it Item) bool {
	return it.Has("blocked") || it.Has("status:blocked") || it.Has("blocked:external") ||
		it.Has("postponed") || it.Has("status:postponed") || it.Has("post-deployment") ||
		it.Has("queue:handoff-held") || it.Has("type:tracker") || it.Has("type:release-review") || it.Has("type:specification")
}

func continuable(it Item, scope string) bool {
	return it.State == "OPEN" && !it.Has("done:"+scope) && !it.Has("done:full") &&
		!queueExcluded(it) && !it.Has("needs-human") && !it.Has("escalated") &&
		!it.HasPrefix("wip:") && !it.HasPrefix("by:")
}

// Only the machine-owned issue branch identifies the source issue. References
// in a PR body often identify prerequisites, not work that should be requeued.
func (l *Loop) requeueMergedPR(ctx context.Context, pr Item) (bool, error) {
	n, scope, ok := branchWork(pr.Head)
	if pr.State != "MERGED" || !ok {
		return false, nil
	}
	it, err := l.gh.Issue(ctx, n)
	if err != nil {
		return false, err
	}
	if !continuable(it, scope) || isReady(it) {
		return false, nil
	}
	if open, err := l.gh.PRForBranch(ctx, pr.Head); err != nil {
		return false, err
	} else if open != nil {
		return false, nil
	}
	// Do not override a block/claim added while GitHub was being queried.
	it, err = l.gh.Issue(ctx, n)
	if err != nil || !continuable(it, scope) {
		return false, err
	}
	if err := l.gh.AddLabels(ctx, n, "ready"); err != nil {
		return false, err
	}
	l.mirror(ctx, it, "ready")
	l.recordOutcome("requeued", n, pr.Number)
	log.Printf("requeued unfinished %s work on #%d after merged PR #%d", scope, n, pr.Number)
	return true, nil
}

// Repair review-state work left by older binaries or merges outside a loop.
// This sweep runs only when no eligible work exists, at most once a minute.
func (l *Loop) recoverMergedWork(ctx context.Context, scope string) (bool, error) {
	if time.Since(l.lastRecovery) < time.Minute {
		return false, nil
	}
	l.lastRecovery = time.Now()
	issues, err := l.gh.Issues(ctx, "status:review")
	if err != nil {
		return false, err
	}
	eligible := make(map[int]bool)
	for _, it := range issues {
		if continuable(it, scope) {
			eligible[it.Number] = true
		}
	}
	if len(eligible) == 0 {
		return false, nil
	}
	var merged []Item
	if err := l.gh.JSON(ctx, &merged, "pr", "list", "--state", "merged", "--limit", "1000",
		"--json", "number,state,headRefName"); err != nil {
		return false, err
	}
	seen := make(map[int]bool)
	changed := false
	for _, pr := range merged {
		n, builtScope, ok := branchWork(pr.Head)
		if !ok || builtScope != scope || !eligible[n] || seen[n] {
			continue
		}
		seen[n] = true
		did, err := l.requeueMergedPR(ctx, pr)
		if err != nil {
			return changed, fmt.Errorf("resume #%d: %w", n, err)
		}
		changed = changed || did
	}
	return changed, nil
}
