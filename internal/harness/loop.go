package harness

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/viminizer/remux/internal/tmux"
)

// Loop is one running loop: one role, one agent, one repo.
type Loop struct {
	Role    string // build or review
	Agent   string // claude or codex
	Repo    string // the main checkout of the repo
	Session string // its loop-* tmux session
	Tmux    *tmux.Client

	gh  GH
	set Settings
}

// Idle is how long a loop waits before looking again when there is no work.
var Idle = time.Minute

// RunTimeout caps one agent run, so a hung agent cannot hold an issue forever.
var RunTimeout = 90 * time.Minute

// Run loops until a stop is queued or ctx ends. A failed iteration is not the
// end: the loop shows it as blocked and tries again, because the usual causes
// (GitHub unreachable, gh logged out) fix themselves or get fixed at the laptop.
func (l *Loop) Run(ctx context.Context) error {
	slug, err := Slug(ctx, l.Repo)
	if err != nil {
		return l.fail(ctx, fmt.Errorf("not a GitHub repo: %w", err))
	}
	l.gh = GH{Slug: slug, Dir: l.Repo}
	l.opt(ctx, "@loop_slug", slug)
	if err := l.gh.EnsureLabels(ctx); err != nil {
		return l.fail(ctx, err)
	}

	for ctx.Err() == nil {
		if l.get(ctx, "@loop_stop") != "" {
			log.Printf("stop requested, exiting")
			return nil
		}
		l.set, err = LoadSettings(l.Repo)
		if err != nil {
			l.state(ctx, "blocked", err.Error())
			sleep(ctx, Idle)
			continue
		}

		var did bool
		if l.Role == "review" {
			did, err = l.reviewOnce(ctx)
		} else {
			l.unblock(ctx)
			did, err = l.buildOnce(ctx)
		}
		switch {
		case err != nil:
			log.Printf("error: %v", err)
			l.state(ctx, "blocked", err.Error())
			sleep(ctx, Idle)
		case !did:
			l.state(ctx, "idle", "")
			sleep(ctx, Idle)
		}
	}
	return nil
}

// fail is for errors the loop cannot retry its way out of. It stays up and
// says why, rather than exiting and taking its session - and the reason -
// with it.
func (l *Loop) fail(ctx context.Context, err error) error {
	log.Printf("cannot run: %v", err)
	l.state(ctx, "failed", err.Error())
	<-ctx.Done()
	return err
}

func (l *Loop) scope(ctx context.Context) string {
	if s := l.get(ctx, "@loop_scope"); s != "" {
		return s
	}
	return "full"
}

// ── build ─────────────────────────────────────────────────────────────────

// pick chooses the next issue for this scope: blockers first, then oldest.
func pick(issues []Item, scope string) *Item {
	sort.SliceStable(issues, func(i, j int) bool {
		bi, bj := issues[i].Has("blocker"), issues[j].Has("blocker")
		if bi != bj {
			return bi
		}
		return issues[i].Number < issues[j].Number
	})
	for i, it := range issues {
		if it.HasPrefix("wip:") || it.Has("done:"+scope) || it.Has("blocked") || it.Has("needs-human") {
			continue
		}
		return &issues[i]
	}
	return nil
}

func (l *Loop) buildOnce(ctx context.Context) (bool, error) {
	scope := l.scope(ctx)
	issues, err := l.gh.Issues(ctx, "ready")
	if err != nil {
		return false, err
	}
	it := pick(issues, scope)
	if it == nil {
		return false, nil
	}
	n := it.Number
	// Claiming by label is not atomic: two loops can grab the same issue. The
	// design accepts that until it actually happens.
	if err := l.gh.AddLabels(ctx, n, "wip:"+scope); err != nil {
		return false, err
	}
	defer l.gh.RemoveLabel(context.WithoutCancel(ctx), n, "wip:"+scope)

	l.working(ctx, n, it.Title)
	log.Printf("── issue #%d: %s", n, it.Title)

	branch := fmt.Sprintf("issue-%d", n)
	if scope != "full" {
		branch += "-" + scope
	}
	base, dependsOn, err := l.base(ctx, n)
	if err != nil {
		return true, l.stuck(ctx, n, it.Title, "Could not find the base branch: "+err.Error())
	}
	wt, err := l.worktree(ctx, branch, base)
	if err != nil {
		return true, l.stuck(ctx, n, it.Title, "Could not create the worktree: "+err.Error())
	}
	thread, err := l.gh.Thread(ctx, "issue", n)
	if err != nil {
		return false, err
	}

	code := l.agent(ctx, wt, Prompt(Run{
		Role: "build", Slug: l.gh.Slug, Number: n, Branch: branch, Worktree: wt, Scope: scope,
		Mode: l.set.Mode, DependsOn: dependsOn, Settings: l.set, Thread: thread,
		Instructions: l.get(ctx, "@loop_instr"), InstrMode: l.get(ctx, "@loop_mode"),
	}))

	// What the agent left behind decides what happens next. Labels are read
	// fresh: the agent changed them during the run.
	after, err := l.gh.Issue(ctx, n)
	if err != nil {
		return true, err
	}
	pr, err := l.gh.PRForBranch(ctx, branch)
	if err != nil {
		return true, err
	}
	switch {
	case after.Has("needs-human"):
		l.gh.RemoveLabel(ctx, n, "ready")
		l.event(ctx, "stuck", n, it.Title)
	case after.Has("blocked"):
		l.gh.RemoveLabel(ctx, n, "ready")
	case pr != nil:
		if err := l.gh.AddLabels(ctx, pr.Number, "needs-review"); err != nil {
			return true, err
		}
		// A partial PR leaves the issue ready for the loops with other scopes.
		if !after.Has("done:" + scope) {
			l.gh.RemoveLabel(ctx, n, "ready")
		}
	case after.Has("done:" + scope):
		l.removeWorktree(ctx, wt, branch)
	case after.State == "CLOSED":
	default:
		return true, l.stuck(ctx, n, it.Title, fmt.Sprintf(
			"The run ended (exit code %d) without a pull request, a done:%s label, a blocker or a question. "+
				"The worktree is kept at `%s`.", code, scope, wt))
	}
	return true, nil
}

// base is where a new branch starts. Normally the default branch. In company
// mode a blocker is never merged by an agent, so an issue it unblocked is built
// on top of the blocker's branch instead, and says so in its pull request.
func (l *Loop) base(ctx context.Context, n int) (string, int, error) {
	def, err := l.defaultBranch(ctx)
	if err != nil || l.set.Mode != "company" {
		return def, 0, err
	}
	blockers, err := l.gh.BlockedBy(ctx, n)
	if err != nil {
		return def, 0, err
	}
	prs, err := l.gh.PRs(ctx, "")
	if err != nil {
		return def, 0, err
	}
	for _, b := range blockers {
		if b.State == "closed" || b.State == "CLOSED" {
			continue
		}
		if pr := prFor(prs, b.Number); pr != nil {
			return "origin/" + pr.Head, pr.Number, nil
		}
	}
	return def, 0, nil
}

// prFor finds the open pull request that closes or refers to issue n.
func prFor(prs []Item, n int) *Item {
	ref := "#" + strconv.Itoa(n)
	for i, p := range prs {
		for _, w := range strings.Fields(p.Body) {
			if strings.TrimRight(w, ".,);") == ref {
				return &prs[i]
			}
		}
	}
	return nil
}

// unblock moves a blocked issue back to ready once nothing blocks it any more.
// In company mode a blocker whose pull request passed review counts as done:
// the supervisor has it, and the blocked issue is built on top of its branch.
func (l *Loop) unblock(ctx context.Context) {
	blocked, err := l.gh.Issues(ctx, "blocked")
	if err != nil || len(blocked) == 0 {
		return
	}
	var prs []Item
	if l.set.Mode == "company" {
		prs, _ = l.gh.PRs(ctx, "")
	}
	for _, it := range blocked {
		blockers, err := l.gh.BlockedBy(ctx, it.Number)
		if err != nil || len(blockers) == 0 {
			continue
		}
		free := true
		for _, b := range blockers {
			if b.State == "closed" || b.State == "CLOSED" {
				continue
			}
			if pr := prFor(prs, b.Number); pr != nil && !pr.IsDraft && !pr.Has("needs-review") {
				continue
			}
			free = false
		}
		if free {
			log.Printf("unblocking #%d", it.Number)
			l.gh.RemoveLabel(ctx, it.Number, "blocked")
			l.gh.AddLabels(ctx, it.Number, "ready")
		}
	}
}

// ── worktrees ─────────────────────────────────────────────────────────────

func (l *Loop) git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("git %s: %s", args[0], firstLine(errb.String(), err))
	}
	return strings.TrimSpace(out.String()), nil
}

func (l *Loop) defaultBranch(ctx context.Context) (string, error) {
	if _, err := l.git(ctx, l.Repo, "fetch", "--quiet", "origin"); err != nil {
		return "", err
	}
	ref, err := l.git(ctx, l.Repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "origin/main", nil
	}
	return ref, nil
}

// worktree returns the worktree for branch, creating it when needed. An
// existing one is reused: a resumed issue picks up where its last run stopped.
func (l *Loop) worktree(ctx context.Context, branch, base string) (string, error) {
	if wt := l.findWorktree(ctx, branch); wt != "" {
		return wt, nil
	}
	wt := filepath.Join(l.Repo, ".claude", "worktrees", branch)
	if _, err := l.git(ctx, l.Repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		_, err := l.git(ctx, l.Repo, "worktree", "add", wt, branch)
		return wt, err
	}
	if _, err := l.git(ctx, l.Repo, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch); err == nil {
		base = "origin/" + branch
	}
	_, err := l.git(ctx, l.Repo, "worktree", "add", "--no-track", "-b", branch, wt, base)
	return wt, err
}

// findWorktree returns the path of the worktree that has branch checked out.
func (l *Loop) findWorktree(ctx context.Context, branch string) string {
	out, err := l.git(ctx, l.Repo, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	var path string
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			path = p
		}
		if line == "branch refs/heads/"+branch {
			return path
		}
	}
	return ""
}

func (l *Loop) removeWorktree(ctx context.Context, wt, branch string) error {
	if _, err := l.git(ctx, l.Repo, "worktree", "remove", wt); err != nil {
		return err
	}
	_, err := l.git(ctx, l.Repo, "branch", "-D", branch)
	return err
}

// ── the agent ─────────────────────────────────────────────────────────────

// claudeTools is the default allowlist. The loops run without permission
// prompts, so Claude gets a fixed set of tools instead of bypass mode.
var claudeTools = []string{
	"Read", "Edit", "Write", "Glob", "Grep", "WebSearch", "WebFetch",
	"Bash(git:*)", "Bash(gh:*)", "Bash(ls:*)", "Bash(cat:*)", "Bash(head:*)", "Bash(tail:*)",
	"Bash(wc:*)", "Bash(grep:*)", "Bash(rg:*)", "Bash(find:*)", "Bash(mkdir:*)", "Bash(diff:*)",
}

// command builds the agent's command line. The prompt goes in on stdin.
func (l *Loop) command(wt string) []string {
	switch l.Agent {
	case "codex":
		// workspace-write limits writes to the worktree. The repo's .git sits
		// outside it, and commits need it; gh needs the network.
		args := []string{"codex", "exec", "-s", "workspace-write",
			"-c", "sandbox_workspace_write.network_access=true",
			"--add-dir", filepath.Join(l.Repo, ".git"), "-C", wt}
		return append(args, "-")
	default:
		tools := append([]string(nil), claudeTools...)
		if l.set.Test != "" {
			tools = append(tools, "Bash("+l.set.Test+")")
		}
		tools = append(tools, l.set.Allow...)
		args := []string{"claude", "-p", "--permission-mode", "acceptEdits",
			"--allowedTools", strings.Join(tools, ",")}
		for _, r := range l.set.References {
			args = append(args, "--add-dir", expandHome(r))
		}
		return args
	}
}

// agent runs one agent process in wt and returns its exit code. Its output
// goes to the loop's own pane, which is where the phone reads the log from.
func (l *Loop) agent(ctx context.Context, wt, prompt string) int {
	ctx, cancel := context.WithTimeout(ctx, RunTimeout)
	defer cancel()
	argv := l.command(wt)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = wt
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("agent: %v", err)
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		return -1
	}
	return 0
}

// ── state ─────────────────────────────────────────────────────────────────

func (l *Loop) opt(ctx context.Context, option, value string) {
	if err := l.Tmux.SetLoopOption(ctx, l.Session, option, value); err != nil {
		log.Printf("state: %v", err)
	}
}

func (l *Loop) get(ctx context.Context, option string) string {
	v, _ := l.Tmux.LoopOption(ctx, l.Session, option)
	return v
}

// state records what the loop is doing. The clock restarts only on a change,
// so "idle for 2h" means two hours, not one minute since the last look.
func (l *Loop) state(ctx context.Context, state, note string) {
	if l.get(ctx, "@loop_state") != state {
		l.opt(ctx, "@loop_since", strconv.FormatInt(time.Now().Unix(), 10))
	}
	l.opt(ctx, "@loop_state", state)
	l.opt(ctx, "@loop_note", note)
	if state != "working" {
		l.opt(ctx, "@loop_issue", "")
		l.opt(ctx, "@loop_title", "")
	}
}

func (l *Loop) working(ctx context.Context, n int, title string) {
	l.opt(ctx, "@loop_issue", strconv.Itoa(n))
	l.opt(ctx, "@loop_title", title)
	l.state(ctx, "working", "")
	// A new issue restarts the clock even when the loop was already working.
	l.opt(ctx, "@loop_since", strconv.FormatInt(time.Now().Unix(), 10))
}

// event records one of the three things worth a push: stuck, merged, sent.
// The server watches the option and notifies on each new value.
func (l *Loop) event(ctx context.Context, kind string, n int, title string) {
	l.opt(ctx, "@loop_event", fmt.Sprintf("%d %s %d %s", time.Now().UnixNano(), kind, n,
		strings.ReplaceAll(title, "|~|", "")))
}

// stuck hands an item to Kevin with a question when the loop itself, not the
// agent, is what got stuck.
func (l *Loop) stuck(ctx context.Context, n int, title, why string) error {
	body := QuestionMarker + "\n" + why + "\n\nWhat should the loop do?\n\n1. Try again\n2. Leave it to me"
	if err := l.gh.Comment(ctx, n, body); err != nil {
		return err
	}
	if err := l.gh.AddLabels(ctx, n, "needs-human"); err != nil {
		return err
	}
	l.gh.RemoveLabel(ctx, n, "ready")
	l.gh.RemoveLabel(ctx, n, "needs-review")
	l.event(ctx, "stuck", n, title)
	return nil
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}
