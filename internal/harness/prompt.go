package harness

import (
	"fmt"
	"strings"
)

// QuestionMarker starts every needs-human comment, so the phone can find the
// question among the other comments and show its options as buttons.
const QuestionMarker = "<!-- remux:question -->"

// Shared by both roles: section 09 of the design.
const commonRules = `You are running unattended inside an agent loop. Nobody is watching.

- Never ask questions and never wait for an answer. If something is unclear, pick the
  safest option and write the assumption down in the pull request.
- Avoid subagents. Do the work yourself, in this one run. Use a subagent only when the
  task truly cannot be done without one, never by default for a fix or an issue.
- Never create git worktrees or new branches. Work in the worktree and on the branch
  you are given; the loop makes and removes those.
- Stay inside the session instructions and the scope below.
- Real decisions (data shape, library choice, API design, merging two conflicting
  approaches) follow this order, and stop at the first one that answers:
  1. The decision log (` + DecisionLog + `). Already decided? Follow it.
  2. This codebase. An existing pattern? Use it.
  3. The reference projects, if any are listed. Look at each one once.
  4. The internet: official docs, well-known open source, engineering blog posts.
     A few searches, then decide.
  5. Senior engineer judgment: two or three options with tradeoffs, pick one.
  Record each real decision in two places: a "## Decisions" section in the pull request
  (choice, reason, source), and one short entry appended to ` + DecisionLog + `.
  Naming a variable is not a decision.
- Only ask a human when code cannot fix the problem. Then add one comment that starts
  with the line ` + QuestionMarker + `, then ONE short question (it is read on a phone),
  then 2 or 3 options as a numbered list ("1. ..."), then add the label needs-human,
  and stop.
- Do not add or remove the labels ready, needs-review or wip:*. The loop does that.`

const buildRules = commonRules + `

Your job in this run: one issue, in the worktree you are in, on the branch you are on.

- Make the change. Run the tests. Commit, push the branch, and open a DRAFT pull request
  with gh pr create --draft. The body says "Closes #N" when this run finishes the issue,
  or "Refs #N" when it only does part of it. Include "## Decisions" and "## Assumptions"
  sections when you have any.
- Part done: comment on the issue what you did and what is left, then add the label
  done:SCOPE.
- Nothing to do for this scope: add the label done:SCOPE with a one-line comment and stop.
- A blocker is a new issue, not a reason to stop, and never something you fix in this run:
  1. gh issue create --label ready --label blocker, with a clear title and body.
  2. Link it: id=$(gh api repos/SLUG/issues/NEW --jq .id) then
     gh api -X POST repos/SLUG/issues/N/dependencies/blocked_by -F issue_id=$id
  3. Add the label blocked to issue #N, comment which issue blocks it, and stop.
  Blockers are one level deep: if issue #N itself has the label blocker, do not open
  another blocker. Ask a human instead.`

const reviewRules = commonRules + `

Your job in this run: review pull request #N once. You are on its branch, in its worktree.

- Read the diff against the base branch and the issue it closes.
- Fix only real bugs: wrong behaviour, crashes, data loss, security holes, and anything
  that goes against the decision log. Ignore style, naming and small things.
- One pass only. Commit your fixes and push the branch. Do not loop on fixes.
- Do not post a review comment yourself. End your run with a short summary of the
  review: what you found and what you fixed, or "No real issues found." The loop posts
  your final message on the pull request.
- Do not merge and do not mark the pull request ready. The loop does that after the
  tests pass.`

const fixRules = commonRules + `

Your job in this run: fix pull request #N. A code review already ran, and its findings
are below. You are on the pull request's branch, in its worktree.

- Do not review the diff again. Work only on the review findings and the conflict, if
  there is one.
- Check each finding against the code. Fix it when it is a real bug; skip it when it is
  wrong.
- One pass only. Commit your fixes and push the branch. Do not loop on fixes.
- Do not post comments yourself. End your run with a short summary: what you fixed, and
  what you skipped and why. The loop posts your final message on the pull request.
- Do not merge and do not mark the pull request ready. The loop does that after the
  tests pass.`

// Run is everything a prompt needs to know about this one run.
type Run struct {
	Role         string // build, review or fix
	Slug         string
	Number       int
	Branch       string
	Worktree     string
	Scope        string
	Mode         string // personal or company
	DependsOn    int    // company mode: the blocker PR this branch is built on
	Conflict     string // review: the default branch this branch conflicts with
	Findings     string // review: what codex review found, for this run to fix
	Settings     Settings
	Instructions string
	InstrMode    string // add or replace
	Thread       string // the issue or pull request, comments included
}

// Prompt builds the run's prompt in the order section 05 fixes: the agent
// rules, the project defaults, the session instructions, then the issue.
func Prompt(r Run) string {
	rules := buildRules
	switch r.Role {
	case "review":
		rules = reviewRules
	case "fix":
		rules = fixRules
	}
	rules = strings.NewReplacer("SCOPE", r.Scope, "SLUG", r.Slug, "#N", fmt.Sprintf("#%d", r.Number),
		"issues/N/", fmt.Sprintf("issues/%d/", r.Number)).Replace(rules)

	var b strings.Builder
	b.WriteString("# Agent rules\n\n" + rules + "\n")

	if r.InstrMode != "replace" && strings.TrimSpace(r.Settings.Instructions) != "" {
		b.WriteString("\n# Project defaults\n\n" + strings.TrimSpace(r.Settings.Instructions) + "\n")
	}
	if strings.TrimSpace(r.Instructions) != "" {
		b.WriteString("\n# Session instructions\n\n" + strings.TrimSpace(r.Instructions) + "\n")
	}

	b.WriteString("\n# This run\n\n")
	fmt.Fprintf(&b, "- Repository: %s\n- Worktree: %s\n- Branch: %s\n- Scope: %s\n- Mode: %s\n",
		r.Slug, r.Worktree, r.Branch, r.Scope, r.Mode)
	if r.Settings.Test != "" {
		fmt.Fprintf(&b, "- Tests: %s\n", r.Settings.Test)
	}
	if len(r.Settings.References) > 0 {
		fmt.Fprintf(&b, "- Reference projects (read-only): %s\n", strings.Join(r.Settings.References, ", "))
	}
	if r.DependsOn > 0 {
		fmt.Fprintf(&b, "- This branch is built on top of pull request #%d, which is not merged yet. "+
			"Open your pull request against the default branch and write \"Depends on PR #%d\" in its body.\n",
			r.DependsOn, r.DependsOn)
	}

	if r.Conflict != "" {
		fmt.Fprintf(&b, "- This branch conflicts with %s. Before you review, run git merge %s, resolve "+
			"every conflict keeping the intent of both sides, run the tests, and commit the merge. "+
			"Resolving it is part of this review, not a reason to stop.\n", r.Conflict, r.Conflict)
	}

	if strings.TrimSpace(r.Findings) != "" {
		b.WriteString("\n# Review findings\n\n" + strings.TrimSpace(r.Findings) + "\n")
	}

	what := "issue"
	if r.Role == "review" || r.Role == "fix" {
		what = "pull request"
	}
	fmt.Fprintf(&b, "\n# The %s\n\n%s\n", what, strings.TrimSpace(r.Thread))
	return b.String()
}
