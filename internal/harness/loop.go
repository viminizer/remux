package harness

import (
	"bytes"
	"context"
	"fmt"
	"io"
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

	// The session instructions it starts with. They are written to the
	// session by the loop itself, before anything reads them, and read back
	// before every run so an edit from the phone applies to the next one.
	Scope, Instructions, InstrMode string

	gh  GH
	set Settings

	labels     map[string]bool // the repo's labels, for mirroring its status:* ones
	lastTriage time.Time
}

// Idle is how long a loop waits before looking again when there is no work.
var Idle = time.Minute

// RunTimeout caps one agent run, so a hung agent cannot hold an issue forever.
var RunTimeout = 90 * time.Minute

// Run loops until a stop is queued or ctx ends. A failed iteration is not the
// end: the loop shows it as blocked and tries again, because the usual causes
// (GitHub unreachable, gh logged out) fix themselves or get fixed at the laptop.
func (l *Loop) Run(ctx context.Context) error {
	for opt, v := range map[string]string{
		"@loop_role": l.Role, "@loop_agent": l.Agent, "@loop_repo": l.Repo,
		"@loop_scope": l.Scope, "@loop_instr": l.Instructions, "@loop_mode": l.InstrMode,
	} {
		l.opt(ctx, opt, v)
	}
	l.state(ctx, "idle", "")

	// Setup needs GitHub, and GitHub being briefly unreachable is not a
	// reason to give up: measured here, one reset connection during label
	// setup was enough to leave a loop dead until someone restarted it.
	var err error
	for ctx.Err() == nil {
		if err = l.setup(ctx); err == nil {
			break
		}
		log.Printf("setup: %v", err)
		l.state(ctx, "blocked", err.Error())
		sleep(ctx, Idle)
	}

	for ctx.Err() == nil {
		if l.get(ctx, "@loop_stop") != "" {
			log.Printf("stop requested, exiting")
			return nil
		}
		l.set, err = l.loadSettings(ctx)
		if err != nil {
			l.state(ctx, "blocked", err.Error())
			sleep(ctx, Idle)
			continue
		}

		var did bool
		switch l.Role {
		case "review":
			did, err = l.reviewOnce(ctx)
		case "supervise":
			did, err = l.superviseOnce(ctx)
		default:
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

func (l *Loop) setup(ctx context.Context) error {
	slug, err := Slug(ctx, l.Repo)
	if err != nil {
		return fmt.Errorf("cannot find the GitHub repo: %w", err)
	}
	l.gh = GH{Slug: slug, Dir: l.Repo}
	l.opt(ctx, "@loop_slug", slug)
	if err := l.gh.EnsureLabels(ctx); err != nil {
		return err
	}
	l.loadLabels(ctx)
	return nil
}

// loadSettings reads the settings from the default branch on GitHub, not from
// the main checkout's files. The checkout is where Kevin works: measured on
// educenter, it was behind origin with uncommitted changes, so a settings file
// pushed to main would not have reached the loop until someone pulled there.
// The checkout's own file is the fallback, for a repo that has not pushed one.
func (l *Loop) loadSettings(ctx context.Context) (Settings, error) {
	if def, err := l.defaultBranch(ctx); err == nil {
		if b, err := l.git(ctx, l.Repo, "show", def+":"+SettingsFile); err == nil {
			return ParseSettings([]byte(b))
		}
	}
	return LoadSettings(l.Repo)
}

func (l *Loop) scope(ctx context.Context) string {
	if s := l.get(ctx, "@loop_scope"); s != "" {
		return s
	}
	return "full"
}

// ── build ─────────────────────────────────────────────────────────────────

// candidates are the issues this scope may take, blockers first, then oldest.
func candidates(issues []Item, scope string) []Item {
	var out []Item
	for _, it := range issues {
		if free(it, scope) {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		bi, bj := out[i].Has("blocker"), out[j].Has("blocker")
		if bi != bj {
			return bi
		}
		return out[i].Number < out[j].Number
	})
	return out
}

func free(it Item, scope string) bool {
	return isReady(it) && !it.HasPrefix("wip:") && !it.Has("done:"+scope) &&
		!it.Has("blocked") && !it.Has("needs-human")
}

func (l *Loop) buildOnce(ctx context.Context) (bool, error) {
	scope := l.scope(ctx)
	issues, err := l.readyIssues(ctx)
	if err != nil {
		return false, err
	}
	// The label-filtered list lags behind label changes: measured here, an
	// issue whose ready label had just been removed was listed as ready again
	// a second later and worked twice. Reading the issue itself is current.
	var it *Item
	for _, c := range candidates(issues, scope) {
		fresh, err := l.gh.Issue(ctx, c.Number)
		if err != nil {
			return false, err
		}
		if fresh.State == "OPEN" && free(fresh, scope) {
			it = &fresh
			break
		}
	}
	if it == nil {
		// Nothing is marked ready, but a loop was started here, so the
		// issues are meant to be worked. Look for ones that can start.
		return l.triage(ctx, scope), nil
	}
	n := it.Number
	// Claiming by label is not atomic: two loops can grab the same issue. The
	// design accepts that until it actually happens.
	if err := l.gh.AddLabels(ctx, n, "wip:"+scope); err != nil {
		return false, err
	}
	defer l.gh.RemoveLabel(context.WithoutCancel(ctx), n, "wip:"+scope)
	l.mirror(ctx, *it, "in-progress")

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

	code, _ := l.agent(ctx, wt, Prompt(Run{
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
		l.unready(ctx, n)
		l.mirror(ctx, after, "blocked")
		l.event(ctx, "stuck", n, it.Title)
	case after.Has("blocked"):
		l.unready(ctx, n)
		l.mirror(ctx, after, "blocked")
	case pr != nil:
		if err := l.gh.AddLabels(ctx, pr.Number, "needs-review"); err != nil {
			return true, err
		}
		// A partial PR leaves the issue ready for the loops with other scopes.
		if !after.Has("done:" + scope) {
			l.unready(ctx, n)
			l.mirror(ctx, after, "review")
		} else {
			l.mirror(ctx, after, "ready")
		}
	case after.Has("done:" + scope):
		// Done for this scope; still queued for the others.
		l.mirror(ctx, after, "ready")
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
			l.mirror(ctx, it, "ready")
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
		return out.String(), fmt.Errorf("git %s: %s", args[0], gitReason(errb.String(), err))
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

// command builds the agent's command line. The prompt goes in on stdin.
//
// Both agents run with every permission granted, on the host. That was
// Kevin's call: a fixed tool allowlist kept stopping the agents on ordinary
// commands, and a loop that cannot run what it needs gets stuck instead.
//
// last is a file Codex writes its final message to; Claude prints only its
// final message, so its stdout is that already.
func (l *Loop) command(wt, last string) []string {
	if l.Agent == "codex" {
		return []string{"codex", "exec", "--dangerously-bypass-approvals-and-sandbox", "-C", wt, "-o", last, "-"}
	}
	args := []string{"claude", "-p", "--dangerously-skip-permissions"}
	for _, r := range l.set.References {
		args = append(args, "--add-dir", expandHome(r))
	}
	return args
}

// agent runs one agent process in wt and returns its exit code and its final
// message. Its output goes to the loop's own pane, which is where the phone
// reads the log from.
func (l *Loop) agent(ctx context.Context, wt, prompt string) (int, string) {
	ctx, cancel := context.WithTimeout(ctx, RunTimeout)
	defer cancel()
	f, err := os.CreateTemp("", "remux-last-*.txt")
	if err != nil {
		return -1, ""
	}
	f.Close()
	defer os.Remove(f.Name())

	argv := l.command(wt, f.Name())
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = wt
	cmd.Stdin = strings.NewReader(prompt)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.MultiWriter(os.Stdout, &out), os.Stderr
	code := 0
	if err := cmd.Run(); err != nil {
		log.Printf("agent: %v", err)
		code = -1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	last := out.String()
	if l.Agent == "codex" {
		b, _ := os.ReadFile(f.Name())
		last = string(b)
	}
	return code, strings.TrimSpace(last)
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
	if kind == "stuck" && l.Role != "supervise" && l.supervised(ctx) {
		return
	}
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
	l.unready(ctx, n)
	l.gh.RemoveLabel(ctx, n, "needs-review")
	l.mirrorN(ctx, n, "blocked")
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

// gitReason picks the lines of git's stderr that say what went wrong. The
// first line alone is often just "To https://github.com/..." - measured on
// educenter, three stuck PRs said only that.
func gitReason(stderr string, fallback error) string {
	var keep []string
	for _, line := range strings.Split(stderr, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "!") || strings.HasPrefix(t, "error:") ||
			strings.HasPrefix(t, "fatal:") || strings.HasPrefix(t, "CONFLICT") {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		return firstLine(stderr, fallback)
	}
	return strings.Join(keep, "; ")
}
