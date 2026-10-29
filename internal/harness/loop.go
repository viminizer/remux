package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/viminizer/remux/internal/tmux"
)

// Loop is one running loop: one role, one agent, one repo.
type Loop struct {
	Role    string // build or review
	Agent   string // claude or codex
	Model   string // empty keeps the CLI's configured model
	Repo    string // the main checkout of the repo
	Session string // its loop-* tmux session
	Tmux    *tmux.Client

	// The session instructions it starts with. They are written to the
	// session by the loop itself, before anything reads them, and read back
	// before every run so an edit from the phone applies to the next one.
	Scope, Instructions, InstrMode string
	Task                           int // custom loops work exactly one issue/PR, then exit
	BudgetKey                      string
	Reserve                        float64
	ReviewPolicy                   string // optional per-loop provider policy
	Effort                         string
	Dry                            bool // cleanup starts in a dry run

	gh  GH
	set Settings

	labels              map[string]bool // the repo's labels, for mirroring its status:* ones
	lastTriage          time.Time
	lastCleanup         time.Time
	lastRecovery        time.Time
	lastIdleAudit       time.Time
	qualityUsageRetryAt time.Time
	idleAuditSummary    string
	sweepSummary        string
	limit               time.Time // set when the last agent run ended on a usage limit
	item                int       // issue or PR being worked; zero while triaging
	effort              string    // review override; builds keep medium
	runTimeout          time.Duration
}

// limited reports whether the last agent run ran out of usage. Callers skip
// everything they would do with its result: there is none.
func (l *Loop) limited() bool { return !l.limit.IsZero() }

// Idle is the minimum interval between queue checks, including short failed runs.
var Idle = 30 * time.Second

// PRLag is the wait between looks for a PR the agent has just opened.
var PRLag = 5 * time.Second

// RunTimeout caps one agent run, so a hung agent cannot hold an issue forever.
var RunTimeout = 90 * time.Minute

// Run loops until a stop is queued or ctx ends. A failed iteration is not the
// end: the loop shows it as blocked and tries again, because the usual causes
// (GitHub unreachable, gh logged out) fix themselves or get fixed at the laptop.
func (l *Loop) Run(ctx context.Context) error {
	if l.Scope == "code-quality" && (l.Role != "build" || l.Task != 0 || (l.Agent != "codex" && l.Agent != "claude")) {
		return fmt.Errorf("code-quality requires a dedicated Codex or Claude builder without a queue task")
	}
	for opt, v := range map[string]string{
		"@loop_role": l.Role, "@loop_agent": l.Agent, "@loop_model": l.Model, "@loop_repo": l.Repo,
		"@loop_scope": l.Scope, "@loop_instr": l.Instructions, "@loop_mode": l.InstrMode,
		"@loop_review_policy": l.ReviewPolicy,
	} {
		l.opt(ctx, opt, v)
	}
	if l.Task > 0 {
		kind := "issue"
		if l.Role == "review" {
			kind = "pr"
		}
		l.opt(ctx, "@loop_custom", "1")
		l.opt(ctx, "@loop_budget_key", l.BudgetKey)
		l.opt(ctx, "@loop_reserve", strconv.FormatFloat(l.Reserve, 'f', -1, 64))
		l.opt(ctx, assignmentOption, fmt.Sprintf("%s:%d", kind, l.Task))
		l.effort = l.Effort
	}
	if l.Dry {
		l.opt(ctx, "@loop_dry", "1")
	}
	l.state(ctx, "idle", "")

	// Setup needs GitHub, and GitHub being briefly unreachable is not a
	// reason to give up: measured here, one reset connection during label
	// setup was enough to leave a loop dead until someone restarted it.
	var err error
	for ctx.Err() == nil {
		if l.get(ctx, "@loop_stop") != "" {
			return nil
		}
		if err = l.setup(ctx); err == nil {
			break
		}
		log.Printf("setup: %v", err)
		if l.Task > 0 {
			return err
		}
		l.state(ctx, "blocked", err.Error())
		sleep(ctx, Idle)
	}

	for ctx.Err() == nil {
		if l.get(ctx, "@loop_stop") != "" {
			log.Printf("stop requested, exiting")
			return nil
		}
		started := time.Now()
		l.set, err = l.loadSettings(ctx)
		if err != nil {
			if l.Task > 0 {
				return err
			}
			l.state(ctx, "blocked", err.Error())
			sleep(ctx, Idle)
			continue
		}
		if l.Task > 0 && l.Role == "review" {
			l.set.ReviewPolicy = "fixed"
		}

		var did bool
		// Keep the model stable through this issue's runs. Phone edits apply
		// on the next iteration, including clearing back to CLI settings.
		l.Model = l.get(ctx, "@loop_model")
		l.item = 0
		l.limit = time.Time{}
		switch l.Role {
		case "review":
			if time.Since(l.lastCleanup) >= 15*time.Minute {
				l.lastCleanup = time.Now()
				if err := l.cleanupMerged(ctx); err != nil {
					log.Printf("merged PR cleanup: %v", err)
				}
				l.adoptOrphans(ctx)
				l.reapClaims(ctx)
			}
			did, err = l.reviewOnce(ctx)
		case "supervise":
			did, err = l.superviseOnce(ctx)
		case "cleanup":
			did, err = l.sweepOnce(ctx)
		default:
			if l.scope(ctx) == "code-quality" {
				did, err = l.qualityOnce(ctx)
			} else {
				l.unblock(ctx)
				did, err = l.buildOnce(ctx)
			}
		}
		if l.limited() {
			if l.Task > 0 {
				l.rememberSpawnLimit()
				l.recordOutcome("custom-usage-deferred", l.Task, 0)
				return nil
			}
			msg := "usage limit, back at " + l.limit.Format("15:04")
			if time.Until(l.limit) > 20*time.Hour {
				msg = "usage limit, back " + l.limit.Format("Mon 15:04")
			}
			log.Printf("%s", msg)
			l.state(ctx, "blocked", msg)
			sleep(ctx, time.Until(l.limit)+time.Minute)
			continue
		}
		if l.Task > 0 {
			if err != nil || !did || l.item == l.Task {
				l.recordOutcome("custom-finished", l.Task, 0)
				return err
			}
			// A busy queue lock/lost claim is not a completed task.
			sleep(ctx, Idle)
			continue
		}
		switch {
		case err != nil:
			log.Printf("error: %v", err)
			l.state(ctx, "blocked", err.Error())
			sleep(ctx, Idle)
		case !did:
			note := ""
			switch l.Role {
			case "supervise":
				note = l.idleAuditSummary
			case "cleanup":
				note = l.sweepSummary
			}
			l.state(ctx, "idle", note)
			sleep(ctx, Idle)
		default:
			sleep(ctx, Idle-time.Since(started))
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
	if l.Role == "build" && l.Scope == "code-quality" {
		return nil // independent audit scheduler never touches the issue queue
	}
	if l.Task == 0 {
		if err := l.gh.EnsureLabels(ctx); err != nil {
			return err
		}
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

// ClaimSettle is how long a loop waits after marking an issue before it
// checks whether another loop marked it too.
var ClaimSettle = 3 * time.Second

// claim takes issue n for this loop, or reports that another loop has it.
//
// A label is not a lock, and with several loops of one kind on a repo, two
// of them waking in the same second will pick the same oldest issue. So each
// marks the issue with its own by:<session> label, waits, and reads it back:
// if more than one loop marked it, the one whose name sorts first keeps it
// and the others take theirs off and move on.
//
// kind is issue or pr, and wip the claim label the winner adds.
func (l *Loop) claim(ctx context.Context, kind string, n int, wip string) (bool, error) {
	unlock, err := l.queueLock(ctx)
	if err != nil || unlock == nil {
		return false, err
	}
	defer unlock()
	// The candidate may have changed while cleanup or recovery held the lock.
	if kind == "issue" {
		fresh, err := l.gh.Issue(ctx, n)
		if err != nil || fresh.State != "OPEN" || !free(fresh, strings.TrimPrefix(wip, "wip:")) {
			return false, err
		}
	}
	me := "by:" + l.Session
	if err := l.gh.AddLabels(ctx, n, me); err != nil {
		return false, err
	}
	sleep(ctx, ClaimSettle)
	fresh, err := l.read(ctx, kind, n)
	if err != nil || claimant(fresh) != me || fresh.HasPrefix("wip:") {
		l.gh.RemoveLabel(context.WithoutCancel(ctx), n, me)
		return false, err
	}
	return true, l.gh.AddLabels(ctx, n, wip)
}

// claimant is the loop that keeps an issue several loops marked at once.
func claimant(it Item) string {
	first := ""
	for _, l := range it.Labels {
		if strings.HasPrefix(l.Name, "by:") && (first == "" || l.Name < first) {
			first = l.Name
		}
	}
	return first
}

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
	return isReady(it) && !it.HasPrefix("wip:") && !it.HasPrefix("by:") && !it.Has("done:"+scope) &&
		!it.Has("done:full") && !queueExcluded(it) && !it.Has("needs-human") && !it.Has("escalated")
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
	assigned, err := l.assignedItem(ctx, "issue", scope)
	if err != nil {
		return false, err
	}
	it = assigned
	if it == nil && l.Task > 0 {
		return false, nil
	}
	for _, c := range candidates(issues, scope) {
		if it != nil {
			break
		}
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
		// A partial merge may have happened while no reviewer was running.
		if recovered, err := l.recoverMergedWork(ctx, scope); err != nil {
			return false, err
		} else if recovered {
			return true, nil
		}
		// Nothing is marked ready, but a loop was started here, so the
		// issues are meant to be worked. Look for ones that can start.
		return l.triage(ctx, scope), nil
	}
	n := it.Number
	won, err := l.claim(ctx, "issue", n, "wip:"+scope)
	if err != nil || !won {
		// Lost to another loop: look again straight away, not in a minute.
		return !won && err == nil, err
	}
	l.clearAssignment(ctx, "issue", n)
	defer func() {
		c := context.WithoutCancel(ctx)
		l.gh.RemoveLabel(c, n, "wip:"+scope)
		l.gh.RemoveLabel(c, n, "by:"+l.Session)
	}()
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

	if l.limited() {
		// The run may have opened its PR before the limit hit - measured on
		// educenter, two did at 12:30 and sat unlabelled. A PR still goes to
		// review; the issue goes back as it was.
		if pr, err := l.gh.PRForBranch(ctx, branch); err == nil && pr != nil {
			l.gh.AddLabels(ctx, pr.Number, "needs-review")
		}
		l.mirrorN(ctx, n, "ready")
		return true, nil
	}
	// What the agent left behind decides what happens next. Labels are read
	// fresh: the agent changed them during the run.
	after, err := l.gh.Issue(ctx, n)
	if err != nil {
		return true, err
	}
	// GitHub's lists lag behind changes, so a PR opened seconds ago may not
	// be listed yet. Asked a few times before concluding there is none.
	var pr *Item
	for try := 0; try < 3; try++ {
		if pr, err = l.gh.PRForBranch(ctx, branch); err != nil {
			return true, err
		}
		if pr != nil {
			break
		}
		sleep(ctx, PRLag)
	}
	return true, l.settle(ctx, it.Title, after, pr, scope, wt, branch, code)
}

// settle acts on what a build run left behind: the issue's labels, read fresh,
// and the PR for its branch, if any.
func (l *Loop) settle(ctx context.Context, title string, after Item, pr *Item, scope, wt, branch string, code int) error {
	n := after.Number
	outcome, prNumber := "no-pr", 0
	if pr != nil {
		outcome, prNumber = "pr-opened", pr.Number
	}
	if after.Has("needs-human") || after.Has("blocked") {
		outcome = "blocked"
	}
	l.recordOutcome(outcome, n, prNumber)
	// A PR always goes to review, whatever else happened to the issue. On
	// educenter five partial PRs sat unreviewed for hours: each agent opened
	// a PR for the part it could do, then marked the rest of the issue
	// blocked, and the blocked case came first and skipped the PR.
	if pr != nil {
		if err := l.noteBuild(ctx, n, pr, scope); err != nil {
			log.Printf("build model note: %v", err)
		}
		if err := l.gh.AddLabels(ctx, pr.Number, "needs-review"); err != nil {
			return err
		}
	}
	switch {
	case after.Has("needs-human"):
		l.unready(ctx, n)
		l.mirror(ctx, after, "blocked")
		l.event(ctx, "stuck", n, title)
		l.dropWorktree(ctx, wt, branch, pr)
	case after.Has("blocked"):
		l.unready(ctx, n)
		l.mirror(ctx, after, "blocked")
		l.dropWorktree(ctx, wt, branch, pr)
	case pr != nil:
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
		l.dropWorktree(ctx, wt, branch, pr)
	case after.State == "CLOSED":
	default:
		return l.stuck(ctx, n, title, fmt.Sprintf(
			"The run ended (exit code %d) without a pull request, a done:%s label, a blocker or a question. "+
				"The worktree is kept at `%s`.", code, scope, wt))
	}
	return nil
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
	var numbers []int
	for _, it := range blocked {
		if !it.Has("blocked:external") && !it.Has("postponed") && !it.Has("status:postponed") {
			numbers = append(numbers, it.Number)
		}
	}
	dependencies, err := l.gh.Blockers(ctx, numbers)
	if err != nil {
		log.Printf("dependency check: %v", err)
		return
	}
	for _, it := range blocked {
		if it.Has("blocked:external") || it.Has("postponed") || it.Has("status:postponed") {
			continue // only verified supervision/triage may release these
		}
		blockers := dependencies[it.Number]
		if len(blockers) == 0 {
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

// dropWorktree removes a finished run's worktree when nothing would be lost:
// no PR still needs it, no uncommitted changes, every commit on GitHub. A run
// that ends blocked or with a question used to keep its worktree to resume
// from; measured on educenter, nothing ever removed them, and 9 of 23
// worktrees were these. A resumed issue gets a fresh one from the pushed
// branch, so keeping them bought nothing.
func (l *Loop) dropWorktree(ctx context.Context, wt, branch string, pr *Item) {
	if pr != nil {
		return
	}
	if dirty, err := l.git(ctx, wt, "status", "--porcelain"); err != nil || dirty != "" {
		return
	}
	if ahead, err := l.git(ctx, wt, "log", "--oneline", "HEAD", "--not", "--remotes"); err != nil || ahead != "" {
		return
	}
	if err := l.removeWorktree(ctx, wt, branch); err != nil {
		log.Printf("cleanup: %v", err)
	}
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
//
// Both run at medium effort, whatever the interactive default is. Measured on
// educenter, Claude at high effort spent 75% of a build run thinking and
// writing, about 67k output tokens a run; Codex at medium built faster.
//
// Claude skips Kevin's user settings: his plugins, hooks and MCP servers are
// for his own sessions, and every call of a run sends them again. Measured on
// educenter, a bare start was 39k tokens with them and 22k without, and a run
// makes 100-200 calls. The repo's own settings and CLAUDE.md, and his global
// CLAUDE.md, still load. His deny rules are carried over, except reading .env
// files, which the loops need.
func (l *Loop) command(wt, last string) []string {
	effort := l.effort
	if effort == "" {
		effort = "medium"
	}
	if l.Agent == "codex" {
		return []string{"codex", "exec", "--dangerously-bypass-approvals-and-sandbox", `-cmodel_reasoning_effort="` + effort + `"`, "-C", wt, "-o", last, "-"}
	}
	args := []string{"claude", "-p", "--dangerously-skip-permissions", "--effort", effort, "--setting-sources", "project,local"}
	if deny := userDeny(); deny != "" {
		args = append(args, "--settings", deny)
	}
	if l.Model != "" {
		args = append(args, "--model", l.Model)
	}
	for _, r := range l.set.References {
		args = append(args, "--add-dir", expandHome(r))
	}
	return args
}

// userDeny is the deny rules from ~/.claude/settings.json as a --settings
// value, or empty when there are none.
func userDeny() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return ""
	}
	var user struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if json.Unmarshal(b, &user) != nil {
		return ""
	}
	// Loops may read .env files: Kevin's call, the apps they run need them.
	deny := slices.DeleteFunc(user.Permissions.Deny, func(r string) bool { return strings.Contains(r, ".env") })
	if len(deny) == 0 {
		return ""
	}
	out, _ := json.Marshal(map[string]any{"permissions": map[string]any{"deny": deny}})
	return string(out)
}

// codexMedium sets Codex's reasoning effort, as one -c argument.
const codexMedium = `-cmodel_reasoning_effort="medium"`

// agent runs one agent process in wt and returns its exit code and its final
// message. Its output goes to the loop's own pane, which is where the phone
// reads the log from.
func (l *Loop) agent(ctx context.Context, wt, prompt string) (int, string) {
	return l.spawn(ctx, wt, prompt, func(last string) []string { return l.command(wt, last) })
}

// codexReview runs Codex's built-in review of wt's changes against base. It
// only reports findings; it does not change the branch.
func (l *Loop) codexReview(ctx context.Context, wt, base string) (int, string) {
	return l.spawn(ctx, wt, "", func(last string) []string {
		return []string{"codex", "exec", "review", "--dangerously-bypass-approvals-and-sandbox", codexMedium, "--base", base, "-o", last}
	})
}

// spawn runs argv(last) in wt with stdin on its input. The final message is
// the file at last when the command wrote one (Codex), else its stdout.
func (l *Loop) spawn(ctx context.Context, wt, stdin string, argv func(last string) []string) (int, string) {
	return l.spawnFiles(ctx, wt, stdin, argv, nil)
}

// spawnFiles lets the quality coordinator inherit its repository lock. On an
// abrupt loop-process death, the coordinator still owns the open file lock.
func (l *Loop) spawnFiles(ctx context.Context, wt, stdin string, argv func(last string) []string, files []*os.File) (int, string) {
	timeout := l.runTimeout
	if timeout == 0 {
		timeout = RunTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	f, err := os.CreateTemp("", "remux-last-*.txt")
	if err != nil {
		return -1, ""
	}
	f.Close()
	defer os.Remove(f.Name())

	// Agents used to leave hundreds of drafts and dumps in /tmp. Each run
	// gets its own folder instead, and it goes when the run ends.
	scratch, err := os.MkdirTemp("", "remux-scratch-*")
	if err != nil {
		return -1, ""
	}
	defer os.RemoveAll(scratch)
	if stdin != "" {
		stdin += "\n\nPut scratch files (drafts, JSON dumps, logs) in $REMUX_SCRATCH, not /tmp. It is deleted when this run ends; keep anything that must last where the project's docs say.\n"
	}

	args := argv(f.Name())
	started := time.Now()
	l.recordRun(started, "start", args, 0, 0)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	// Isolate this runner and its tool commands from the live workspace.
	// Stop the whole group on timeout before another reviewer takes over.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if err == syscall.ESRCH {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	cmd.WaitDelay = time.Second
	cmd.Dir = wt
	cmd.ExtraFiles = files
	cmd.Env = append(os.Environ(), "REMUX_SCRATCH="+scratch)
	cmd.Stdin = strings.NewReader(stdin)
	var out, all bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.MultiWriter(os.Stdout, &out, &all), io.MultiWriter(os.Stderr, &all)
	code := 0
	if err := cmd.Run(); err != nil {
		log.Printf("agent: %v", err)
		code = -1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	// Only a failed run is checked, and only its end: an agent writing about
	// rate limits in the code it works on must not read as being out of usage.
	if code != 0 {
		if until, ok := usageLimit(tail(all.String(), 40), time.Now()); ok {
			l.limit = until
		}
	}
	l.recordRun(started, "end", args, code, time.Since(started).Milliseconds())
	if b, _ := os.ReadFile(f.Name()); len(bytes.TrimSpace(b)) > 0 {
		return code, strings.TrimSpace(string(b))
	}
	return code, strings.TrimSpace(out.String())
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
	l.item = n
	l.opt(ctx, "@loop_issue", strconv.Itoa(n))
	l.opt(ctx, "@loop_title", title)
	l.state(ctx, "working", "")
	// A new issue restarts the clock even when the loop was already working.
	l.opt(ctx, "@loop_since", strconv.FormatInt(time.Now().Unix(), 10))
}

// event records one of the three things worth a push: stuck, merged, sent.
// The server watches the option and notifies on each new value.
func (l *Loop) event(ctx context.Context, kind string, n int, title string) {
	l.recordOutcome(kind, n, 0)
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
