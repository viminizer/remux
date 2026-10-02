package harness

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// The supervisor takes the Inbox first. Every needs-human item reaches it
// before Kevin: it fixes what the loops got stuck on, answers an agent's
// question only when the repo itself clearly decides it, and passes the rest
// on to him in plain English with a recommendation. An item it passed on
// carries the label escalated, and only then does Kevin get the push.

const superviseRules = `You are the supervisor of the agent loops on SLUG. You are running unattended. Kevin
owns this repo. English is his second language and he does not know how the loops work
inside, so anything you write for him uses short sentences and common words.

Item #N (KIND) has the label needs-human: a loop or an agent stopped and is waiting.
Read it and all its comments below. It is one of two kinds.

1. A loop problem. The loop wrote the question itself: a push failed, a merge conflict,
   tests fail, a worktree problem. Investigate and fix it. Work only in the item's own
   worktree (git worktree list shows it, under .claude/worktrees/), never in the repo's
   main checkout - Kevin works there. You may commit and push to the item's branch. Do
   not merge pull requests and do not push to the default branch.
2. A question from an agent. Answer it yourself ONLY when the issue, its comments, the
   decision log (` + DecisionLog + ` on the default branch), the docs or the code clearly
   decide it. Then post one comment that starts with "**Supervisor answer:**", gives the
   answer, and names the source that decided it.

If you fixed or answered it, end your run with a final message whose first line is
RESOLVED, followed by one line saying what you did.

Otherwise pass it to Kevin. Post one comment that starts with the line
` + QuestionMarker + `
then, in plain words: what is being decided, what each choice means for the product, and
which one you recommend and why. Then 2 or 3 options as a numbered list ("1. ..."), the
recommended one first, ending with "(recommended)". End your run with a final message whose
first line is ESCALATED.

Pass it on, never decide, when it is about product scope, money, privacy, changing the
project settings, or anything you are not sure about. Do not add or remove any labels:
the loop does that.`

func (l *Loop) superviseOnce(ctx context.Context) (bool, error) {
	var items []struct {
		Item
		kind string
	}
	for _, kind := range []string{"issue", "pr"} {
		var list []Item
		if err := l.gh.JSON(ctx, &list, kind, "list", "--state", "open", "--label", "needs-human",
			"--limit", "100", "--json", itemFields); err != nil {
			return false, err
		}
		for _, it := range list {
			items = append(items, struct {
				Item
				kind string
			}{it, kind})
		}
	}

	// Re-read before claiming, for the lag the build loop measured.
	var it *Item
	kind := ""
	for _, c := range items {
		if c.Has("escalated") || c.HasPrefix("wip:") {
			continue
		}
		fresh, err := l.read(ctx, c.kind, c.Number)
		if err != nil {
			return false, err
		}
		if fresh.State == "OPEN" && fresh.Has("needs-human") && !fresh.Has("escalated") && !fresh.HasPrefix("wip:") {
			it, kind = &fresh, c.kind
			break
		}
	}
	if it == nil {
		return false, nil
	}
	n := it.Number
	if err := l.gh.AddLabels(ctx, n, "wip:supervise"); err != nil {
		return false, err
	}
	defer l.gh.RemoveLabel(context.WithoutCancel(ctx), n, "wip:supervise")

	l.working(ctx, n, it.Title)
	log.Printf("── supervising #%d: %s", n, it.Title)

	thread, err := l.gh.Thread(ctx, kind, n)
	if err != nil {
		return false, err
	}
	what := "issue"
	if kind == "pr" {
		what = "pull request"
	}
	rules := strings.NewReplacer("SLUG", l.gh.Slug, "#N", fmt.Sprintf("#%d", n), "KIND", what).Replace(superviseRules)
	prompt := "# Supervisor rules\n\n" + rules + "\n"
	if s := strings.TrimSpace(l.set.Instructions); s != "" {
		prompt += "\n# Project defaults\n\n" + s + "\n"
	}
	prompt += fmt.Sprintf("\n# The %s\n\n%s\n", what, strings.TrimSpace(thread))

	_, last := l.agent(ctx, l.Repo, prompt)
	if l.limited() {
		return true, nil // still needs-human, not escalated
	}

	if verdict(last) == "RESOLVED" {
		next := "ready"
		if kind == "pr" {
			next = "needs-review"
		}
		if err := l.gh.AddLabels(ctx, n, next); err != nil {
			return true, err
		}
		l.gh.RemoveLabel(ctx, n, "needs-human")
		if kind == "issue" {
			l.mirrorN(ctx, n, "ready")
		}
		log.Printf("resolved #%d", n)
		return true, nil
	}
	// Anything but a clear RESOLVED goes to Kevin: a run that died or said
	// nothing must not leave the item silently parked.
	if err := l.gh.AddLabels(ctx, n, "escalated"); err != nil {
		return true, err
	}
	l.event(ctx, "stuck", n, it.Title)
	return true, nil
}

func (l *Loop) read(ctx context.Context, kind string, n int) (Item, error) {
	if kind == "pr" {
		return l.gh.PR(ctx, n)
	}
	return l.gh.Issue(ctx, n)
}

// verdict is the first word of the agent's final message.
func verdict(last string) string {
	f := strings.Fields(last)
	if len(f) == 0 {
		return ""
	}
	return strings.ToUpper(strings.Trim(f[0], "*:#. "))
}

// supervised reports whether a supervise loop runs on this repo. While one
// does, the other loops leave the "stuck" push to it: Kevin hears about an
// item only once the supervisor has passed it on.
func (l *Loop) supervised(ctx context.Context) bool {
	loops, err := l.Tmux.Loops(ctx)
	if err != nil {
		return false
	}
	for _, o := range loops {
		if o.Role == "supervise" && o.Slug == l.gh.Slug {
			return true
		}
	}
	return false
}

// Briefing is the system prompt for the supervisor Kevin talks to from the
// phone. repos is one "owner/name  /local/path" line per harness repo.
func Briefing(repos []string) string {
	list := "(none yet)"
	if len(repos) > 0 {
		list = strings.Join(repos, "\n")
	}
	return `You are the supervisor of Kevin's agent loops (the remux agent harness). Kevin talks to you
from his phone. English is his second language: answer in short sentences with common words,
no jargon, and keep answers short. Lead with the answer.

What the loops are: Claude and Codex take GitHub issues on their own. A build loop takes one
ready issue per run, works in a git worktree under .claude/worktrees/, and opens a draft PR.
The review loop reviews each needs-review PR once, merges the default branch in first, runs
the repo's test command, then merges (personal mode) or hands the PR to a supervisor
(company mode). A supervise loop takes needs-human items first and only passes real
decisions to Kevin. Labels are the state: ready (or status:ready), wip:<scope>,
done:<scope>, blocker, blocked, needs-review, needs-human, escalated. Each repo keeps
` + SettingsFile + ` (mode, test command, references, instructions) and ` + DecisionLog + `.

Repos with loops:
` + list + `

How to look:
- Loops are detached tmux sessions named loop-*:
  tmux list-sessions -F '#{session_name} role=#{@loop_role} state=#{@loop_state} issue=#{@loop_issue}' | grep loop-
  @loop_title, @loop_instr and @loop_note are base64.
- A loop's log is its own screen: tmux capture-pane -p -t '=loop-NAME:' -S -500
- GitHub: gh issue list / gh pr list -R owner/name --label <label>, gh issue view N --comments.

What you may do: read anything; fix stuck items the way the supervise loop would (in the
item's worktree, never in a repo's main checkout); move labels to put an item back in the
queue; stop a loop with: tmux set-option -t '=loop-NAME:' @loop_stop after.

Never run tmux attach, switch-client, select-window, select-pane or resize anything: those
move Kevin's own screen. Never write to a tmux session that is not named loop-*. Never merge
to a default branch yourself. Ask Kevin before anything you cannot undo.`
}
