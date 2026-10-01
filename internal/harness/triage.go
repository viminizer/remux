package harness

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// Repos that already track work with labels of their own get the loops'
// state in their own words as well. "status:ready" means the same as
// "ready", and when a repo has the status:* labels below, the loops move an
// issue through them as they work it: in-progress when claimed, review when
// its pull request is up, blocked when it waits on something.
var readyLabels = []string{"ready", "status:ready"}

func isReady(it Item) bool {
	for _, l := range readyLabels {
		if it.Has(l) {
			return true
		}
	}
	return false
}

// readyIssues lists every open issue carrying any of the ready labels.
func (l *Loop) readyIssues(ctx context.Context) ([]Item, error) {
	seen := map[int]bool{}
	var all []Item
	for _, label := range readyLabels {
		items, err := l.gh.Issues(ctx, label)
		if err != nil && !strings.Contains(err.Error(), "not found") {
			return nil, err
		}
		for _, it := range items {
			if !seen[it.Number] {
				seen[it.Number] = true
				all = append(all, it)
			}
		}
	}
	return all, nil
}

// unready takes every ready label off an issue, so neither name puts it back
// in the queue.
func (l *Loop) unready(ctx context.Context, n int) {
	for _, label := range readyLabels {
		l.gh.RemoveLabel(ctx, n, label)
	}
}

// mirror sets the repo's own status label for an issue, if the repo has one
// by that name, and takes off the other status:* labels - they are one state,
// not a set. A repo without status labels is left alone.
func (l *Loop) mirror(ctx context.Context, it Item, state string) {
	want := "status:" + state
	if !l.labels[want] {
		return
	}
	for _, have := range it.Labels {
		if strings.HasPrefix(have.Name, "status:") && have.Name != want {
			l.gh.RemoveLabel(ctx, it.Number, have.Name)
		}
	}
	if !it.Has(want) {
		l.gh.AddLabels(ctx, it.Number, want)
	}
}

// mirrorN is mirror for when only the number is in hand.
func (l *Loop) mirrorN(ctx context.Context, n int, state string) {
	if it, err := l.gh.Issue(ctx, n); err == nil {
		l.mirror(ctx, it, state)
	}
}

// ── triage ────────────────────────────────────────────────────────────────

// TriageEvery spaces out triage runs. Triage is an agent run, so it costs
// money, and a repo where nothing can be started now will usually still be
// that way in a minute.
var TriageEvery = 30 * time.Minute

// TriageMax is how many issues one triage run may mark ready. Enough to keep
// the loops busy; small enough that a wrong guess stays a small mistake.
const TriageMax = 5

const triageRules = commonRules + `

Your job in this run: no open issue is marked ready, but a loop was started on this
repo, so Kevin wants its issues worked. Find the issues that can be started now and mark
them so the loops take them. Do not write any code and do not change any files.

1. List the open issues and the repo's labels:
   gh issue list --state open --limit 300 --json number,title,labels
   gh label list --limit 200
2. Learn how this repo tracks its work: its status labels, its areas, how it writes
   dependencies ("blocked by #12", "depends on", task lists, GitHub's blocked-by links:
   gh api repos/SLUG/issues/N/dependencies/blocked_by).
3. An issue can be started now when everything it depends on is closed or merged, and it
   is clear enough to build. Read the ones that look closest (gh issue view N --comments).
   Prefer the oldest and the ones other issues depend on.
4. For each one you choose, at most MAX, add the label ready. (This is the one run where
   adding ready is your job, not the loop's.) Then update its labels to
   match this repo's own format: for example, if the repo uses status:* labels, replace
   status:blocked with status:ready. Leave a one-line comment saying why it can start now.
5. If an issue is labeled blocked but its blockers are all done, that is the most common
   case: fix its labels the same way.
6. If nothing can be started, change nothing and say why in one short paragraph.`

// triage runs one agent pass that picks issues to mark ready. It reports
// whether it ran at all.
func (l *Loop) triage(ctx context.Context, scope string) bool {
	if time.Since(l.lastTriage) < TriageEvery || l.othersTriaging(ctx) {
		return false
	}
	var open []Item
	if err := l.gh.JSON(ctx, &open, "issue", "list", "--state", "open", "--limit", "1", "--json", "number"); err != nil || len(open) == 0 {
		return false
	}
	l.lastTriage = time.Now()
	l.state(ctx, "triaging", "")
	log.Printf("── no ready issues: looking for ones that can start")

	rules := strings.NewReplacer("SLUG", l.gh.Slug, "MAX", fmt.Sprint(TriageMax)).Replace(triageRules)
	var b strings.Builder
	b.WriteString("# Agent rules\n\n" + rules + "\n")
	if mode := l.get(ctx, "@loop_mode"); mode != "replace" && strings.TrimSpace(l.set.Instructions) != "" {
		b.WriteString("\n# Project defaults\n\n" + strings.TrimSpace(l.set.Instructions) + "\n")
	}
	if instr := strings.TrimSpace(l.get(ctx, "@loop_instr")); instr != "" {
		b.WriteString("\n# Session instructions\n\nOnly mark issues these allow:\n\n" + instr + "\n")
	}
	fmt.Fprintf(&b, "\n# This run\n\n- Repository: %s\n- Scope of the loop: %s\n", l.gh.Slug, scope)

	l.agent(ctx, l.Repo, b.String())
	l.loadLabels(ctx)
	return true
}

// othersTriaging reports whether another loop on the same repo is already
// triaging, so two build loops do not pay for the same pass.
func (l *Loop) othersTriaging(ctx context.Context) bool {
	loops, err := l.Tmux.Loops(ctx)
	if err != nil {
		return false
	}
	for _, o := range loops {
		if o.Name != l.Session && o.Slug == l.gh.Slug && o.State == "triaging" {
			return true
		}
	}
	return false
}

func (l *Loop) loadLabels(ctx context.Context) {
	var labels []struct {
		Name string `json:"name"`
	}
	if err := l.gh.JSON(ctx, &labels, "label", "list", "--limit", "300", "--json", "name"); err != nil {
		return
	}
	l.labels = map[string]bool{}
	for _, x := range labels {
		l.labels[x.Name] = true
	}
}
