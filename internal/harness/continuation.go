package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
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

// Called with queueLock held, after branch cleanup has completed.
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
	prs, err := l.gh.PRs(ctx, "")
	if err != nil {
		return false, err
	}
	for _, open := range prs {
		if source, _, ok := branchWork(open.Head); ok && source == n {
			return false, nil
		}
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
// This sweep repairs old work. Normal merge completion requeues immediately.
// Share the expensive history scan across builders of the same repo/scope.
const RecoveryEvery = 5 * time.Minute

func (l *Loop) recoveryTurn(ctx context.Context, scope string) (bool, error) {
	if time.Since(l.lastRecovery) < RecoveryEvery {
		return false, nil
	}
	unlock, err := l.queueLock(ctx)
	if err != nil || unlock == nil {
		return false, err
	}
	defer unlock()
	dir, err := l.git(ctx, l.Repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return false, err
	}
	path := filepath.Join(dir, "remux-recovery-times.json")
	times := map[string]time.Time{}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if len(b) > 0 {
		if err = json.Unmarshal(b, &times); err != nil {
			return false, err
		}
	}
	if times == nil {
		times = map[string]time.Time{}
	}
	if time.Since(times[scope]) < RecoveryEvery {
		l.lastRecovery = time.Now()
		return false, nil
	}
	times[scope] = time.Now()
	b, err = json.Marshal(times)
	if err != nil {
		return false, err
	}
	f, err := os.CreateTemp(dir, "remux-recovery-times-*.tmp")
	if err != nil {
		return false, err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return false, err
	}
	if err = f.Close(); err != nil {
		return false, err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return false, err
	}
	l.lastRecovery = time.Now()
	return true, nil
}

func (l *Loop) recoverMergedWork(ctx context.Context, scope string) (bool, error) {
	turn, err := l.recoveryTurn(ctx, scope)
	if err != nil || !turn {
		return false, err
	}
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
		"--json", "number,state,headRefName,headRefOid"); err != nil {
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
		did, err := l.resumeMergedPR(ctx, pr)
		if err != nil {
			return changed, fmt.Errorf("resume #%d: %w", n, err)
		}
		changed = changed || did
	}
	return changed, nil
}
