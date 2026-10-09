package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TestTimeout caps the test command a pull request has to pass.
var TestTimeout = 30 * time.Minute

// MergeRetry is the wait between merge attempts while GitHub is still working
// out whether a just-pushed PR can merge.
var MergeRetry = 15 * time.Second

func (l *Loop) reviewOnce(ctx context.Context) (bool, error) {
	prs, err := l.gh.PRs(ctx, "needs-review")
	if err != nil {
		return false, err
	}
	// Re-read before claiming, for the same lag the build loop measured.
	var pr *Item
	for _, p := range prs {
		if p.HasPrefix("wip:") || p.HasPrefix("by:") || p.Has("needs-human") {
			continue
		}
		fresh, err := l.gh.PR(ctx, p.Number)
		if err != nil {
			return false, err
		}
		if fresh.State == "OPEN" && fresh.Has("needs-review") && !fresh.HasPrefix("wip:") && !fresh.HasPrefix("by:") && !fresh.Has("needs-human") {
			pr = &fresh
			break
		}
	}
	if pr == nil {
		return false, nil
	}
	n := pr.Number
	// Several review loops can share a repo, so a PR is claimed the same way
	// the build loops claim an issue.
	won, err := l.claim(ctx, "pr", n, "wip:review")
	if err != nil || !won {
		return !won && err == nil, err
	}
	defer func() {
		c := context.WithoutCancel(ctx)
		l.gh.RemoveLabel(c, n, "wip:review")
		l.gh.RemoveLabel(c, n, "by:"+l.Session)
	}()

	l.working(ctx, n, pr.Title)
	log.Printf("── review PR #%d: %s", n, pr.Title)

	if _, err := l.git(ctx, l.Repo, "fetch", "--quiet", "origin", pr.Head); err != nil {
		return true, l.stuck(ctx, n, pr.Title, "Could not fetch the branch: "+err.Error())
	}
	wt, err := l.worktree(ctx, pr.Head, "origin/"+pr.Head)
	if err != nil {
		return true, l.stuck(ctx, n, pr.Title, "Could not create the worktree: "+err.Error())
	}
	// The branch is on GitHub, so a later review starts from a fresh
	// worktree. Keeping this one bought nothing: reviews that ended stuck,
	// with a question, or handed to a supervisor left theirs behind for good.
	// dropWorktree keeps it when it holds uncommitted or unpushed work, and
	// it is left alone while another loop is working on the same branch.
	defer func() {
		c := context.WithoutCancel(ctx)
		if active, err := l.branchActive(c, *pr); err == nil && !active {
			l.dropWorktree(c, wt, pr.Head, nil)
		}
	}()
	// A worktree left from the build run may be behind what was pushed since.
	if _, err := l.git(ctx, wt, "merge", "--ff-only", "--quiet", "origin/"+pr.Head); err != nil {
		return true, l.stuck(ctx, n, pr.Title, "The local branch has moved away from the pushed one: "+err.Error())
	}
	// Bring the branch up to date with the default branch first. The build
	// loops open many PRs from the same base, and once one merges the rest
	// stop merging cleanly - measured on educenter, 6 of the first 8 reviews
	// ended at "the merge commit cannot be cleanly created". A clean merge is
	// done here; a conflict becomes the first part of the review.
	conflict := ""
	def, err := l.defaultBranch(ctx)
	if err != nil {
		return true, err
	}
	if _, err := l.git(ctx, wt, "merge", "--no-edit", "--quiet", def); err != nil {
		l.git(ctx, wt, "merge", "--abort")
		conflict = def
	}
	thread, err := l.gh.Thread(ctx, "pr", n)
	if err != nil {
		return false, err
	}

	// Codex reviews with its own review mode first and its findings go on
	// the PR as review comments. A clean review with no conflict goes
	// straight to tests and merge; only findings or a conflict start a second
	// run, which fixes them on this branch without reviewing again.
	findings, role := "", "review"
	if l.Agent == "codex" {
		role = "fix"
		var ok bool
		findings, ok = l.retry(func() (int, string) { return l.codexReview(ctx, wt, def) })
		if l.limited() {
			return true, nil // still needs-review; tried again after the reset
		}
		if !ok {
			return true, l.stuck(ctx, n, pr.Title, fmt.Sprintf("codex review crashed %d times in a row. See the loop's log.", ReviewTries))
		}
		if hasFindings(findings) {
			if err := l.postFindings(ctx, n, wt, findings); err != nil {
				log.Printf("review comments: %v", err)
			}
		}
	}
	run := func(role, conflict, findings string) (string, bool) {
		return l.retry(func() (int, string) {
			return l.agent(ctx, wt, Prompt(Run{
				Role: role, Slug: l.gh.Slug, Number: n, Branch: pr.Head, Worktree: wt,
				Scope: "review", Mode: l.set.Mode, Settings: l.set, Thread: thread, Conflict: conflict,
				Findings:     findings,
				Instructions: l.get(ctx, "@loop_instr"), InstrMode: l.get(ctx, "@loop_mode"),
			}))
		})
	}
	summary := findings
	if role == "review" || conflict != "" || hasFindings(findings) {
		var ok bool
		if summary, ok = run(role, conflict, findings); !ok {
			if l.limited() {
				return true, nil
			}
			return true, l.stuck(ctx, n, pr.Title, fmt.Sprintf("The %s run crashed %d times in a row. See the loop's log.", role, ReviewTries))
		}
	}
	// The review is posted by the loop, not left to the agent, so every
	// reviewed PR says what the review found - "no real issues" too. It goes
	// in as a GitHub review, so it sits in the PR's review timeline.
	if summary == "" {
		summary = "The review finished, but the agent left no summary. See the loop's log."
	}
	if err := l.gh.Review(ctx, n, summary); err != nil {
		log.Printf("review comment: %v", err)
	}

	after, err := l.gh.PR(ctx, n)
	if err != nil {
		return true, err
	}
	if after.State == "MERGED" {
		return true, l.cleanupMergedPR(ctx, after)
	}
	if after.Has("needs-human") {
		l.gh.RemoveLabel(ctx, n, "needs-review")
		l.event(ctx, "stuck", n, pr.Title)
		return true, nil
	}
	return true, l.finish(ctx, n, pr.Title, pr.Head, wt, func(conflict, problem string) bool {
		_, ok := run("fix", conflict, problem)
		return ok
	})
}

// RepairTries is how many times finish hands a conflict, failing tests or a
// refused merge back to a fix run before the PR goes to Kevin. Measured on
// educenter, 71 of 391 PRs went to him for these, and their waiting was 1050
// hours against 665 for the other 320 together.
var RepairTries = 3

// finish is everything after the review that must not depend on the agent
// having done it right: the work is pushed, the tests pass, and only then does
// the pull request move on - merged in personal mode, handed to the supervisor
// in company mode. A conflict with the default branch, failing tests or a
// merge GitHub refuses go back to fix, a fix run, and the checks start over.
func (l *Loop) finish(ctx context.Context, n int, title, branch, wt string, fix func(conflict, problem string) bool) error {
	if l.set.Test == "" {
		return l.stuck(ctx, n, title, "There is no test command in "+SettingsFile+
			", so nothing can merge. Add one, then resume.")
	}
	for try := 1; ; try++ {
		if pr, err := l.gh.PR(ctx, n); err != nil {
			return err
		} else if pr.State == "MERGED" {
			return l.cleanupMergedPR(ctx, pr)
		}
		if dirty, _ := l.git(ctx, wt, "status", "--porcelain"); dirty != "" {
			return l.stuck(ctx, n, title, "The review left uncommitted changes in `"+wt+"`.")
		}
		conflict, problem, err := l.check(ctx, n, title, branch, wt)
		if err != nil {
			return err
		}
		if conflict == "" && problem == "" {
			return nil // merged, or handed to the supervisor
		}
		why := problem
		if conflict != "" {
			why = "The branch conflicts with " + conflict + "."
		}
		if try == RepairTries {
			return l.stuck(ctx, n, title, fmt.Sprintf("Still failing after %d fix runs. %s", RepairTries, why))
		}
		log.Printf("PR #%d: %s Starting a fix run (%d/%d).", n, firstLine(why, nil), try, RepairTries-1)
		if !fix(conflict, problem) {
			if l.limited() {
				// Tried again after the reset. check may have taken the label off.
				return l.gh.AddLabels(ctx, n, "needs-review")
			}
			return l.stuck(ctx, n, title, fmt.Sprintf("The fix run crashed %d times in a row. %s", ReviewTries, why))
		}
	}
}

// check brings the branch up to date, pushes it, tests it and moves it on. It
// returns the default branch when that conflicts, or what failed when a fix
// run can repair it; both empty means the pull request moved on.
func (l *Loop) check(ctx context.Context, n int, title, branch, wt string) (conflict, problem string, err error) {
	// main may have moved again during the review.
	def, err := l.defaultBranch(ctx)
	if err != nil {
		return "", "", err
	}
	if _, err := l.git(ctx, wt, "merge-base", "--is-ancestor", def, "HEAD"); err != nil {
		if _, err := l.git(ctx, wt, "merge", "--no-edit", "--quiet", def); err != nil {
			l.git(ctx, wt, "merge", "--abort")
			return def, "", nil
		}
	}
	// With lease: the branch is the loop's own, and an agent may have
	// rebased it, but a push from anywhere else since the fetch still wins.
	if _, err := l.git(ctx, wt, "push", "--quiet", "--force-with-lease", "origin", "HEAD:"+branch); err != nil {
		return "", "", l.stuck(ctx, n, title, "Could not push the branch: "+err.Error())
	}
	if out, err := l.test(ctx, wt); err != nil {
		return "", "The tests fail. Make them pass:\n\n```\n" + tail(out, 60) + "\n```", nil
	}
	sha, err := l.git(ctx, wt, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}

	if _, err := l.gh.run(ctx, "pr", "ready", fmt.Sprint(n)); err != nil && !strings.Contains(err.Error(), "already") {
		return "", "", l.stuck(ctx, n, title, "Could not mark the pull request ready: "+err.Error())
	}
	l.gh.RemoveLabel(ctx, n, "needs-review")

	if l.set.Mode == "company" {
		if who := l.supervisor(ctx); who != "" {
			if _, err := l.gh.run(ctx, "pr", "edit", fmt.Sprint(n), "--add-reviewer", who); err != nil {
				log.Printf("could not request a review from %s: %v", who, err)
			}
		}
		l.event(ctx, "sent", n, title)
		return "", "", nil
	}

	// --match-head-commit: merge exactly what was tested, not whatever was
	// pushed in between. Retried, because GitHub works out whether a PR can
	// merge after each push, and asking straight away can find it unsure.
	var err2 error
	for try := 0; try < 4; try++ {
		if _, err2 = l.gh.run(ctx, "pr", "merge", fmt.Sprint(n), "--squash", "--match-head-commit", sha); err2 == nil ||
			!strings.Contains(err2.Error(), "not mergeable") {
			break
		}
		sleep(ctx, MergeRetry)
	}
	if err2 != nil {
		if pr, err := l.gh.PR(ctx, n); err == nil && pr.State == "MERGED" {
			return "", "", l.cleanupMergedPR(ctx, pr)
		}
		// Most often the default branch moved and now conflicts; the next
		// round finds that and hands it to a fix run.
		if strings.Contains(err2.Error(), "not mergeable") {
			return "", "GitHub refuses to merge: " + err2.Error(), nil
		}
		return "", "", l.stuck(ctx, n, title, "Could not merge: "+err2.Error())
	}
	// The merge is complete. Cleanup is retried by the review loop if it fails.
	if pr, err := l.gh.PR(ctx, n); err == nil {
		if err := l.cleanupMergedPR(ctx, pr); err != nil {
			log.Printf("cleanup: %v", err)
		}
	} else {
		log.Printf("cleanup: could not reload merged PR #%d: %v", n, err)
	}
	l.event(ctx, "merged", n, title)
	return "", "", nil
}

func (l *Loop) test(ctx context.Context, wt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, TestTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", l.set.Test)
	cmd.Dir = wt
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// supervisor names who reviews in company mode. An empty answer with no error
// means CODEOWNERS covers it: GitHub asks the owners itself when a draft is
// marked ready.
func (l *Loop) supervisor(ctx context.Context) string {
	for _, p := range []string{"CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"} {
		if _, err := os.Stat(filepath.Join(l.Repo, p)); err == nil {
			return ""
		}
	}
	me, _ := l.gh.run(ctx, "api", "user", "-q", ".login")
	if who := recentReviewer(l.history(ctx), strings.TrimSpace(string(me))); who != "" {
		return who
	}
	return l.set.Supervisor
}

type mergedPR struct {
	MergedBy struct {
		Login string `json:"login"`
	} `json:"mergedBy"`
	Reviews []struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
	} `json:"reviews"`
}

func (l *Loop) history(ctx context.Context) []mergedPR {
	out, err := l.gh.run(ctx, "pr", "list", "--state", "merged", "--limit", "20", "--json", "mergedBy,reviews")
	if err != nil {
		return nil
	}
	var prs []mergedPR
	json.Unmarshal(out, &prs)
	return prs
}

// recentReviewer is whoever reviewed or merged the most recent pull requests,
// newest first, never Kevin himself.
func recentReviewer(prs []mergedPR, me string) string {
	for _, p := range prs {
		for i := len(p.Reviews) - 1; i >= 0; i-- {
			if who := p.Reviews[i].Author.Login; who != "" && who != me {
				return who
			}
		}
		if who := p.MergedBy.Login; who != "" && who != me {
			return who
		}
	}
	return ""
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// OrphanAge is how old an unlabelled loop PR must be before the review loop
// adopts it, so a build run still settling its own PR is never raced.
var OrphanAge = time.Hour

// adoptOrphans sends a loop's draft PR to review when nothing ever labelled
// it: a run that died after opening its PR leaves one behind, and no loop
// looks at a PR without a label. Only drafts on issue-* branches: a PR out of
// draft with no label is a company PR with its supervisor.
func (l *Loop) adoptOrphans(ctx context.Context) {
	var prs []struct {
		Item
		CreatedAt time.Time `json:"createdAt"`
	}
	if err := l.gh.JSON(ctx, &prs, "pr", "list", "--state", "open", "--limit", "200",
		"--json", itemFields+",isDraft,headRefName,createdAt"); err != nil {
		return
	}
	for _, p := range prs {
		if !orphan(p.Item, p.CreatedAt, time.Now()) {
			continue
		}
		if err := l.gh.AddLabels(ctx, p.Number, "needs-review"); err == nil {
			log.Printf("adopted unlabelled PR #%d", p.Number)
		}
	}
}

func orphan(p Item, created, now time.Time) bool {
	if !p.IsDraft || !strings.HasPrefix(p.Head, "issue-") || now.Sub(created) < OrphanAge {
		return false
	}
	return !p.Has("needs-review") && !p.Has("needs-human") && !p.HasPrefix("wip:") && !p.HasPrefix("by:")
}

// ReviewTries is how many times a review or fix run is started in all
// before a crash hands the PR to Kevin.
var ReviewTries = 3

// retry starts run again while it crashes: a non-zero exit or no final
// message. It stops at a usage limit, which the caller waits out.
func (l *Loop) retry(run func() (int, string)) (string, bool) {
	for try := 1; ; try++ {
		code, out := run()
		if l.limited() {
			return out, false
		}
		if code == 0 && out != "" {
			return out, true
		}
		if try == ReviewTries {
			return out, false
		}
		log.Printf("run crashed (exit %d, %d chars out), starting it again (%d/%d)", code, len(out), try+1, ReviewTries)
	}
}

// findingRE matches a finding line in Codex review output:
// "- [P1] Title — /abs/path/file.go:12-14".
// ponytail: tied to Codex's output format; if it changes, every review looks
// clean and skips the fix run. Switch to codex exec review --json then.
var findingRE = regexp.MustCompile(`(?m)^\s*- \[P\d\]`)

var findingHead = regexp.MustCompile(`^\s*- (\[P\d\] .+?) — (.+?):(\d+)(?:-(\d+))?\s*$`)

func hasFindings(review string) bool { return findingRE.MatchString(review) }

// parseFindings turns Codex review output into the overall verdict and one
// inline comment per finding. ok is false when a finding cannot be placed on
// a file in wt, so the caller posts the text as it is instead.
func parseFindings(review, wt string) (overall string, comments []ReviewComment, ok bool) {
	lines := strings.Split(review, "\n")
	first := len(lines)
	for i, line := range lines {
		if findingRE.MatchString(line) {
			first = i
			break
		}
	}
	head := strings.TrimSpace(strings.Join(lines[:first], "\n"))
	if i := strings.LastIndex(head, "\n"); i >= 0 && strings.HasSuffix(head, ":") {
		head = strings.TrimSpace(head[:i]) // drop the "Review comment:" header
	}
	for _, line := range lines[first:] {
		if m := findingHead.FindStringSubmatch(line); m != nil {
			path, ok := strings.CutPrefix(m[2], strings.TrimSuffix(wt, "/")+"/")
			if !ok {
				return head, nil, false
			}
			c := ReviewComment{Path: path, Side: "RIGHT", Body: "**" + m[1] + "**\n"}
			start, _ := strconv.Atoi(m[3])
			c.Line = start
			if m[4] != "" {
				c.Line, _ = strconv.Atoi(m[4])
			}
			if start < c.Line {
				c.StartLine = start
			}
			comments = append(comments, c)
		} else if findingRE.MatchString(line) {
			return head, nil, false
		} else if len(comments) > 0 {
			c := &comments[len(comments)-1]
			c.Body += "\n" + strings.TrimSpace(line)
		}
	}
	for i := range comments {
		comments[i].Body = strings.TrimSpace(comments[i].Body)
	}
	return head, comments, len(comments) > 0
}

// postFindings puts each finding on its line of the PR. GitHub refuses the
// whole review when one line is outside the diff; then the findings go in
// as one review body.
func (l *Loop) postFindings(ctx context.Context, n int, wt, review string) error {
	if overall, comments, ok := parseFindings(review, wt); ok {
		if err := l.gh.ReviewComments(ctx, n, overall, comments); err == nil {
			return nil
		} else {
			log.Printf("inline review comments: %v", err)
		}
	}
	return l.gh.Review(ctx, n, strings.ReplaceAll(review, strings.TrimSuffix(wt, "/")+"/", ""))
}
