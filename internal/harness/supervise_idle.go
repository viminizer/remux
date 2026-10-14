package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/viminizer/remux/internal/tmux"
)

// Short gaps between jobs are normal. Investigation is a paid agent run,
// unlike the 30-second inbox/loop poll, so give an intervention time to work.
const idleGrace = 2 * time.Minute
const idleAuditEvery = 15 * time.Minute
const assignmentOption = "@loop_assignment"

func idleWorker(o tmux.Loop, slug string, now time.Time) bool {
	return o.Slug == slug && tmux.ValidLoopName(o.Name) && o.Stop == "" &&
		(o.Role == "build" || o.Role == "review") &&
		(o.State == "idle" || o.State == "blocked") && o.Since > 0 &&
		now.Sub(time.Unix(o.Since, 0)) >= idleGrace &&
		!strings.Contains(strings.ToLower(o.Note), "usage limit")
}

const idleSupervisorRules = `You supervise the existing loops on this repository. Investigate why the
listed workers are idle or blocked, and restore useful progress when possible.
Do not manufacture work just to keep an agent busy.

Read the loop roster, scoped queue and previous intervention below. Read each
affected issue/PR and its comments, actual merged contracts, runtime handoffs,
project decisions and worker instructions before acting. Capture a worker's
screen read-only when needed. Re-read live state before every intervention.

Choose actions by the actual cause:
- No retained unfinished work: leave the loop idle and explain that it is healthy.
- Wrong/stale queue labels or merged partial deliveries: verify remaining scope
  against code, then correct labels and requeue it. Never infer completion from
  an open/closed issue alone, or requeue an issue with an active PR/worker.
- Missing implementation prerequisite: find its existing issue first. If none
  exists, file a bounded prerequisite with concrete evidence and link the
  dependency. Mark it ready only when its own prerequisites are actually merged.
  Delegate it to an available builder with the matching scope and instructions.
- Awaiting review: make an eligible draft PR needs-review and delegate it to an
  available reviewer. Never merge or mark a draft ready yourself.
- Stale ownership: confirm twice, at least ten seconds apart, that the owner is
  gone or no longer on the item. Check for a still-running agent/process and
  preserved work before releasing its claim. Never touch an active claim.
- Build/conflict/worktree/runtime failure: repair only in the item's isolated
  worktree, or requeue/delegate that issue after removing the concrete blocker.
- A coding provider's expired login/quota: verify another supported provider's
  authenticated access and usage, ownership/handoff and preserved work. If that
  resolves the only operational gate, requeue and request a budget-checked worker
  on the available provider. Do not require Claude login for a Codex-authorized
  task. This never supplies missing business/runtime credentials.
- External dependency, business/runtime credentials or a real time gate: retain the block
  and record the exact prerequisite/deadline. Do not repeatedly retry it.

Preserve stop requests, ownership, worker scope/instructions, any project pause,
postponed/held scope, privacy and financial rules. Do not change worker models,
instructions or stop settings. Do not send keys, attach, select, resize, kill or
restart tmux sessions/processes. All tmux use in this run is READ-ONLY. Never
borrow credentials or bypass permission/migration fences. Project defaults and
explicit user delegation govern decisions as in normal supervision.

For every changed item, post a concise visible comment recording the diagnosis,
action and expected result. Verify GitHub accepted it. Give existing loops normal
ready/needs-review work; do not claim it on their behalf. End with a short summary
of each cause/action and remaining blocker. If nothing needs action, say so.

To delegate, put a separate line in your FINAL message for each assignment:
DELEGATE loop-session-name issue 123
DELEGATE loop-review-session-name pr 456
At most five assignments. The harness revalidates the worker and item's current
eligibility and sets a one-shot preferred task; normal claim locking still runs.
Do not assign active, stopped, limited, postponed or held work. Do not issue the
same assignment repeatedly: verify the previous action's result first.

Kevin additionally authorizes routine scope/capacity/model decisions without
another approval. If no suitable available worker exists, request a temporary
builder or reviewer for an independent task. Up to FIVE custom builders and
FIVE custom reviewers may run concurrently; start fewer when tasks overlap or
usage headroom is insufficient for the selected provider. These workers do one task and clean up their
own session afterward, retaining drafts and unfinished work. Do not create new
work just to fill five slots. Existing stop requests and ownership still apply.
Verify written prerequisites, current merged contracts, concrete file ownership
and eligible labels. Group work that touches the same owned files sequentially.
Backend prerequisites discovered during frontend work are real backend work:
do not call the frontend queue healthy merely because all idle builders are
frontend-only. An older "backend finished" note is not proof the new fixes shipped.

Prefer DELEGATE to an available worker with suitable scope and instructions.
When additional capacity or a different scope/provider is required, FINAL lines:
SPAWN build backend 123 normal
SPAWN build frontend 456 deep
SPAWN review full 789 normal
normal means bounded routine work; deep means sensitive financial/auth/schema or
large work needing extra budget and reasoning. Kevin's 2026-10-14 policy supersedes older project usage constraints: new Codex
workers may be created while actual weekly remaining usage is above 30%; do not
apply the former 50% guard or predicted concurrent-task reservations. Claude
builders may be created regardless of remaining usage, missing quota data or a
known usage reset wait. Claude itself still enforces its native limits. Claude
reviewers retain the existing quota checks. The harness enforces this policy and
CLI availability before selecting a model. Do not reject a Claude builder request
because of session/weekly quota or apply older project rules to this decision. Percentages cannot
guarantee a task's token cost; bound the task honestly. You must not bypass this
check by starting a session yourself. Leave a visible task comment recording why
the prerequisite or review needs capacity. Never silently take another owner's work.`

func (l *Loop) superviseIdleOnce(ctx context.Context) (bool, error) {
	loops, err := l.Tmux.Loops(ctx)
	if err != nil {
		return false, err
	}
	now := time.Now()
	var idle []tmux.Loop
	for _, o := range loops {
		if idleWorker(o, l.gh.Slug, now) {
			idle = append(idle, o)
		}
	}
	if now.Sub(l.lastIdleAudit) < idleAuditEvery {
		return false, nil
	}
	workers := 0
	for _, o := range loops {
		if o.Slug == l.gh.Slug && (o.Role == "build" || o.Role == "review") && o.Stop == "" {
			workers++
		}
	}
	if workers == 0 {
		return false, nil
	} // Honor a deliberately stopped project.

	var issues, prs []Item
	if err := l.gh.JSON(ctx, &issues, "issue", "list", "--state", "open", "--limit", "500", "--json", itemFields); err != nil {
		return false, err
	}
	if err := l.gh.JSON(ctx, &prs, "pr", "list", "--state", "open", "--limit", "500", "--json", itemFields+",isDraft,headRefName,headRefOid"); err != nil {
		return false, err
	}

	// Complete queues need no AI. Keep owned items in the roster/PR snapshot,
	// while showing each builder only its unfinished scope shortlist.
	scoped := map[string][]Item{"backend": triageCandidates(issues, "backend"), "frontend": triageCandidates(issues, "frontend")}
	on := map[string]int{}
	for _, o := range loops {
		on[o.Name] = o.Issue
	}
	stale := staleIn(append(append([]Item{}, issues...), prs...), on)
	needsAudit := len(stale) > 0
	// Ready prerequisites in a missing scope are a capacity gap. All existing
	// loops may be busy: temporary builders can still make independent progress.
	for _, scope := range []string{"backend", "frontend"} {
		if len(candidates(issues, scope)) > 0 {
			needsAudit = true
		}
	}
	for _, p := range prs {
		if reviewEligible(p) {
			needsAudit = true
		}
	}
	for _, o := range idle {
		if o.State == "blocked" {
			needsAudit = true
		}
		if o.Role == "review" {
			for _, p := range prs {
				if !p.HasPrefix("wip:") && !p.HasPrefix("by:") && !p.Has("postponed") && !p.Has("status:postponed") && !p.Has("queue:handoff-held") {
					needsAudit = true
				}
			}
		} else {
			scope := o.Scope
			if scope == "" {
				scope = "full"
			}
			scoped[scope] = triageCandidates(issues, scope)
			if len(scoped[scope]) > 0 {
				needsAudit = true
			}
		}
	}
	l.lastIdleAudit = now
	if !needsAudit {
		log.Printf("supervisor: %d idle loops; no unfinished scope work or review queue", len(idle))
		return false, nil
	}
	// JSON keeps multiline worker instructions intact without interpolating
	// them into shell commands. Other repos' loops never reach this prompt.
	var roster []tmux.Loop
	for _, o := range loops {
		if o.Slug == l.gh.Slug {
			roster = append(roster, o)
		}
	}
	snapshot, err := json.MarshalIndent(struct {
		Loops  []tmux.Loop       `json:"loops"`
		Issues map[string][]Item `json:"unfinishedByScope"`
		PRs    []Item            `json:"openPRs"`
		Stale  map[string]Item   `json:"possibleStaleClaims"`
	}{roster, scoped, prs, stale}, "", "  ")
	if err != nil {
		return false, err
	}
	prompt := idleSupervisorRules + "\nRepository: " + l.gh.Slug + "\n"
	if l.get(ctx, "@loop_mode") != "replace" {
		prompt += "\n# Project defaults\n" + l.set.Instructions
	}
	prompt += "\n# Supervisor instructions\n" + l.get(ctx, "@loop_instr")
	prompt += "\n# Live roster and queue\n" + string(snapshot)
	prompt += "\n# Previous intervention (verify its result before retrying)\n" + l.idleAuditSummary
	l.state(ctx, "triaging", "Investigating idle/blocked loops")
	l.recordOutcome("supervisor-investigation", 0, 0)
	code, summary := l.agent(ctx, l.Repo, prompt)
	if l.limited() {
		return true, nil
	}
	l.idleAuditSummary = tail(summary, 80)
	log.Printf("supervisor idle investigation: %s", l.idleAuditSummary)
	if code != 0 {
		return true, fmt.Errorf("idle investigation exited %d: %s", code, tail(summary, 10))
	}
	if err := l.delegate(ctx, summary); err != nil {
		return true, err
	}
	if err := l.spawnWorkers(ctx, summary); err != nil {
		return true, err
	}
	return true, nil
}

func reviewEligible(it Item) bool {
	return it.State == "OPEN" && it.Has("needs-review") && !it.HasPrefix("wip:") &&
		!it.HasPrefix("by:") && !it.Has("needs-human") && !it.Has("escalated") && !queueExcluded(it)
}

// Assignments are preferences, never authorization or claims. A stale/invalid
// preference is discarded; workers continue their ordinary queue afterward.
func (l *Loop) assignedItem(ctx context.Context, kind, scope string) (*Item, error) {
	value := l.get(ctx, assignmentOption)
	if value == "" {
		return nil, nil
	}
	k, number, ok := strings.Cut(value, ":")
	n, err := strconv.Atoi(number)
	if !ok || k != kind || err != nil || n <= 0 {
		l.opt(ctx, assignmentOption, "")
		return nil, nil
	}
	it, err := l.read(ctx, kind, n)
	if err != nil {
		return nil, err
	}
	if (kind == "issue" && it.State == "OPEN" && free(it, scope)) || (kind == "pr" && reviewEligible(it)) {
		return &it, nil
	}
	l.opt(ctx, assignmentOption, "")
	log.Printf("discarded ineligible supervisor assignment %s", value)
	return nil, nil
}

func (l *Loop) clearAssignment(ctx context.Context, kind string, n int) {
	if l.get(ctx, assignmentOption) == fmt.Sprintf("%s:%d", kind, n) {
		l.opt(ctx, assignmentOption, "")
	}
}

func (l *Loop) delegate(ctx context.Context, summary string) error {
	usedLoops, usedItems := map[string]bool{}, map[string]bool{}
	// Preserve preferences left by the previous intervention. Another idle
	// worker must not receive the same item while its first delegate wakes up.
	current, err := l.Tmux.Loops(ctx)
	if err != nil {
		return err
	}
	for _, o := range current {
		if o.Slug != l.gh.Slug || (o.Role != "build" && o.Role != "review") {
			continue
		}
		pending, err := l.Tmux.LoopOption(ctx, o.Name, assignmentOption)
		if err != nil {
			return err
		}
		if pending != "" {
			usedItems[pending] = true
		}
	}
	for _, line := range controlLines(summary) {
		f := strings.Fields(line)
		if len(f) != 4 || f[0] != "DELEGATE" || len(usedLoops) >= 5 {
			continue
		}
		name, kind := f[1], f[2]
		n, err := strconv.Atoi(f[3])
		if err != nil || n <= 0 || !tmux.ValidLoopName(name) || (kind != "issue" && kind != "pr") {
			continue
		}
		key := fmt.Sprintf("%s:%d", kind, n)
		if usedLoops[name] || usedItems[key] {
			continue
		}
		loops, err := l.Tmux.Loops(ctx)
		if err != nil {
			return err
		}
		for _, o := range loops {
			if o.Name != name || !idleWorker(o, l.gh.Slug, time.Now()) ||
				(kind == "issue" && o.Role != "build") || (kind == "pr" && o.Role != "review") {
				continue
			}
			pending, err := l.Tmux.LoopOption(ctx, name, assignmentOption)
			if err != nil {
				return err
			}
			if pending != "" {
				continue
			}
			it, err := l.read(ctx, kind, n)
			if err != nil {
				return err
			}
			scope := o.Scope
			if scope == "" {
				scope = "full"
			}
			if (kind == "issue" && (it.State != "OPEN" || !free(it, scope))) || (kind == "pr" && !reviewEligible(it)) {
				continue
			}
			if err := l.Tmux.SetLoopOption(ctx, name, assignmentOption, key); err != nil {
				return err
			}
			usedLoops[name], usedItems[key] = true, true
			log.Printf("supervisor delegated %s to %s", key, name)
			l.recordOutcome("delegated", n, 0)
		}
	}
	return nil
}

// Examples quoted inside Markdown fences are not executable requests.
func controlLines(summary string) []string {
	var out []string
	fenced := false
	for _, line := range strings.Split(summary, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") || strings.HasPrefix(strings.TrimSpace(line), "~~~") {
			fenced = !fenced
			continue
		}
		if !fenced {
			out = append(out, line)
		}
	}
	return out
}
