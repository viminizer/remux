package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
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
var TriageEvery = 5 * time.Minute

// TriageMax is how many issues one triage run may mark ready. Enough to keep
// the loops busy; small enough that a wrong guess stays a small mistake.
const TriageMax = 5

const triageRules = commonRules + `

Your job in this run: no issue is eligible for THIS LOOP'S SCOPE. Other scopes may
have many ready issues; ready items with done:SCOPE are not work for this loop.
Find the issues that can be started now and mark
them so the loops take them. Do not write any code and do not change any files.

1. Start with the scope-filtered shortlist and dispatch counts below. A generic
   ready label is not evidence that THIS scope has work. Do not stop because
   completed items still have ready labels. Verify each selected issue's fresh
   state and labels before changing anything. Read the repo's labels:
   gh label list --limit 200
2. Learn how this repo tracks its work: its status labels, its areas, how it writes
   dependencies ("blocked by #12", "depends on", task lists, GitHub's blocked-by links:
   gh api repos/SLUG/issues/N/dependencies/blocked_by).
3. Read the ones that look closest (gh issue view N --comments). A prerequisite can be
   satisfied for this scope while its issue remains open for another scope: verify its
   actual merged implementation and contract, not just the issue state or a done label.
   Inspect unfinished status:review issues in the shortlist first: after a merged partial PR, remaining
   work may need requeueing. Never requeue work with an open PR or active worker.
   Prefer the oldest and the ones other issues depend on.
4. For each one you choose, at most MAX, add the label ready. (This is the one run where
   adding ready is your job, not the loop's.) Then update its labels to
   match this repo's own format: for example, if the repo uses status:* labels, replace
   status:blocked with status:ready. Leave a one-line comment saying why it can start now.
5. Preserve postponed, post-deployment and held work. Preserve real external/time gates,
   including blocked:external; verify the prerequisite/deadline and runtime handoff
   before removing a block. Closed dependency edges alone do not resolve a time gate.
6. If nothing can be started, change nothing and name the shortlisted issues you
   checked and their concrete remaining blockers. Do not report ready items with
   done:SCOPE as an available queue. Never add done:SCOPE during triage.`

// Blocked items stay in this shortlist so the agent can verify changed
// prerequisites and elapsed time gates. Completed, deferred and owned work
// cannot become triage candidates just because a generic ready label remains.
func triageCandidates(open []Item, scope string) []Item {
	var out []Item
	for _, it := range open {
		if it.State != "OPEN" || it.Has("done:"+scope) || it.Has("done:full") ||
			it.HasPrefix("wip:") || it.HasPrefix("by:") || it.Has("needs-human") || it.Has("escalated") ||
			it.Has("postponed") || it.Has("status:postponed") || it.Has("post-deployment") ||
			it.Has("queue:handoff-held") || it.Has("type:tracker") || it.Has("type:specification") || it.Has("type:release-review") {
			continue
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool {
		// Partial deliveries are the first place to look when a scoped
		// queue is empty, rather than retreading ready work for other scopes.
		ri, rj := out[i].Has("status:review"), out[j].Has("status:review")
		if ri != rj {
			return ri
		}
		bi, bj := out[i].Has("blocker"), out[j].Has("blocker")
		if bi != bj {
			return bi
		}
		return out[i].Number < out[j].Number
	})
	return out
}

func (l *Loop) triagePrompt(scope string, open []Item, instr, mode string) string {
	rules := strings.NewReplacer("SLUG", l.gh.Slug, "MAX", fmt.Sprint(TriageMax), "SCOPE", scope).Replace(triageRules)
	var b strings.Builder
	b.WriteString("# Agent rules\n\n" + rules + "\n")
	if mode != "replace" && strings.TrimSpace(l.set.Instructions) != "" {
		b.WriteString("\n# Project defaults\n\n" + strings.TrimSpace(l.set.Instructions) + "\n")
	}
	if instr = strings.TrimSpace(instr); instr != "" {
		b.WriteString("\n# Session instructions\n\nOnly mark issues these allow:\n\n" + instr + "\n")
	}
	ready := 0
	for _, it := range open {
		if isReady(it) {
			ready++
		}
	}
	shortlist := triageCandidates(open, scope)
	fmt.Fprintf(&b, "\n# This run\n\n- Repository: %s\n- Scope of the loop: %s\n- Open issues: %d\n- Generic ready labels across all scopes: %d\n- Eligible ready issues for this scope: %d\n- Unfinished issues to inspect for this scope: %d\n",
		l.gh.Slug, scope, len(open), ready, len(candidates(open, scope)), len(shortlist))
	type entry struct {
		Number int      `json:"number"`
		Title  string   `json:"title"`
		Labels []string `json:"labels"`
	}
	entries := make([]entry, 0, len(shortlist))
	for _, it := range shortlist {
		labels := make([]string, 0, len(it.Labels))
		for _, label := range it.Labels {
			labels = append(labels, label.Name)
		}
		entries = append(entries, entry{it.Number, it.Title, labels})
	}
	data, _ := json.Marshal(entries) // entries contain only strings and integers
	fmt.Fprintf(&b, "\n# Scope-filtered shortlist\n\n```json\n%s\n```\n", data)
	return b.String()
}

// triage runs one agent pass that picks issues to mark ready. It reports
// whether it ran at all.
func (l *Loop) triage(ctx context.Context, scope string) bool {
	if time.Since(l.lastTriage) < TriageEvery || l.othersTriaging(ctx) {
		return false
	}
	var open []Item
	if err := l.gh.JSON(ctx, &open, "issue", "list", "--state", "open", "--limit", "1000", "--json", itemFields); err != nil || len(triageCandidates(open, scope)) == 0 {
		return false
	}
	l.lastTriage = time.Now()
	l.state(ctx, "triaging", "")
	log.Printf("── no eligible %s issues: inspecting %d unfinished issues", scope, len(triageCandidates(open, scope)))
	l.agent(ctx, l.Repo, l.triagePrompt(scope, open, l.get(ctx, "@loop_instr"), l.get(ctx, "@loop_mode")))
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
