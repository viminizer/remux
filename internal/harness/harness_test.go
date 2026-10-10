package harness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func item(n int, labels ...string) Item {
	it := Item{Number: n}
	for _, l := range labels {
		it.Labels = append(it.Labels, struct {
			Name string `json:"name"`
		}{l})
	}
	return it
}

func TestCandidates(t *testing.T) {
	issues := []Item{
		item(3, "ready"),
		item(1, "ready", "wip:backend"),      // another loop has it
		item(2, "ready", "done:full"),        // this scope already did its part
		item(7, "ready", "blocker"),          // blockers jump the queue
		item(5, "ready", "blocked"),          // waiting on a blocker
		item(4, "ready", "needs-human"),      // waiting on Kevin
		item(6, "ready", "blocker", "wip:x"), // a claimed blocker is still claimed
	}
	if got := candidates(issues, "full"); len(got) != 2 || got[0].Number != 7 || got[1].Number != 3 {
		t.Fatalf("candidates = %+v, want the free blocker #7 then #3", got)
	}
	if got := candidates([]Item{item(9, "ready", "done:backend"), item(8, "ready")}, "backend"); len(got) != 1 || got[0].Number != 8 {
		t.Fatalf("candidates skipped wrong: %+v", got)
	}
	if got := candidates([]Item{item(1, "ready", "wip:full"), item(2)}, "full"); len(got) != 0 {
		t.Fatalf("candidates = %+v, want nothing", got)
	}
}

func TestPRFor(t *testing.T) {
	prs := []Item{
		{Number: 20, Body: "Refs #12, part one"},
		{Number: 21, Body: "Closes #1. Fixes the thing.\nSee #100"},
	}
	if p := prFor(prs, 1); p == nil || p.Number != 21 {
		t.Errorf("prFor(1) = %+v, want #21", p)
	}
	if p := prFor(prs, 12); p == nil || p.Number != 20 {
		t.Errorf("prFor(12) = %+v, want #20", p)
	}
	// #10 is a prefix of #100, and must not match it.
	if p := prFor(prs, 10); p != nil {
		t.Errorf("prFor(10) = %+v, want nothing", p)
	}
}

func TestRecentReviewer(t *testing.T) {
	var a, b mergedPR
	a.MergedBy.Login = "kevin"
	b.MergedBy.Login = "boss"
	b.Reviews = append(b.Reviews, struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
	}{})
	b.Reviews[0].Author.Login = "lead"
	if who := recentReviewer([]mergedPR{a, b}, "kevin"); who != "lead" {
		t.Errorf("recentReviewer = %q, want lead (a review beats a merge, and never Kevin)", who)
	}
	if who := recentReviewer([]mergedPR{a}, "kevin"); who != "" {
		t.Errorf("recentReviewer = %q, want empty", who)
	}
}

// The order is the design: rules, project defaults, session instructions, issue.
func TestPromptOrder(t *testing.T) {
	r := Run{
		Role: "build", Slug: "o/r", Number: 12, Branch: "issue-12-backend", Scope: "backend",
		Mode: "personal", Settings: Settings{Instructions: "Never touch payments."},
		Instructions: "Only the backend.", InstrMode: "add", Thread: "title: Fix login",
	}
	p := Prompt(r)
	order := []string{"# Agent rules", "Never touch payments.", "Only the backend.", "Fix login"}
	last := -1
	for _, s := range order {
		i := strings.Index(p, s)
		if i <= last {
			t.Fatalf("%q is out of order in:\n%s", s, p)
		}
		last = i
	}
	for _, want := range []string{"done:backend", "Closes #12", "issues/12/dependencies/blocked_by", "repos/o/r/"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	if strings.Contains(p, "SCOPE") || strings.Contains(p, "SLUG") || strings.Contains(p, "#N ") {
		t.Errorf("prompt still has placeholders:\n%s", p)
	}

	r.InstrMode = "replace"
	if strings.Contains(Prompt(r), "Never touch payments.") {
		t.Error("replace must drop the project defaults")
	}
	r.Role = "review"
	if !strings.Contains(Prompt(r), "review pull request #12 once") {
		t.Error("review prompt has the wrong rules")
	}
}

func TestCommand(t *testing.T) {
	l := &Loop{Agent: "claude", Repo: "/r", set: Settings{References: []string{"/ref"}}}
	if c := strings.Join(l.command("/r/wt", "/tmp/last"), " "); c != "claude -p --dangerously-skip-permissions --effort medium --add-dir /ref" {
		t.Errorf("claude command = %q", c)
	}
	l.Agent = "codex"
	if c := strings.Join(l.command("/r/wt", "/tmp/last"), " "); c != `codex exec --dangerously-bypass-approvals-and-sandbox -cmodel_reasoning_effort="medium" -C /r/wt -o /tmp/last -` {
		t.Errorf("codex command = %q", c)
	}
}

func TestSettings(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadSettings(dir)
	if err != nil || s.Mode != "personal" {
		t.Fatalf("no file: %+v, %v", s, err)
	}
	wrote, err := WriteStarterFiles(dir)
	if err != nil || len(wrote) != 2 {
		t.Fatalf("WriteStarterFiles = %v, %v", wrote, err)
	}
	if again, _ := WriteStarterFiles(dir); len(again) != 0 {
		t.Errorf("second run overwrote %v", again)
	}
	if _, err := LoadSettings(dir); err != nil {
		t.Fatalf("starter file does not load: %v", err)
	}
	os.WriteFile(filepath.Join(dir, SettingsFile), []byte(`{"mode":"yolo"}`), 0o644)
	if _, err := LoadSettings(dir); err == nil {
		t.Error("an unknown mode must be refused")
	}
}

func TestStatusReadyCountsAsReady(t *testing.T) {
	got := candidates([]Item{item(4, "status:ready"), item(5, "status:blocked"), item(6, "ready")}, "full")
	if len(got) != 2 || got[0].Number != 4 || got[1].Number != 6 {
		t.Fatalf("candidates = %+v, want #4 and #6", got)
	}
}

// fakeGH stands in for the gh binary and records each call, one per line.
func fakeGH(t *testing.T) func() []string {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "gh")
	os.WriteFile(bin, []byte("#!/bin/sh\necho \"$*\" >> "+log+"\necho '[]'\n"), 0o755)
	old := ghBin
	ghBin = bin
	t.Cleanup(func() { ghBin = old })
	return func() []string {
		b, _ := os.ReadFile(log)
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

func TestMirror(t *testing.T) {
	calls := fakeGH(t)
	l := &Loop{gh: GH{Slug: "o/r"}, labels: map[string]bool{"status:in-progress": true, "status:blocked": true}}
	l.mirror(context.Background(), item(7, "type:work", "status:blocked"), "in-progress")
	got := strings.Join(calls(), "\n")
	for _, want := range []string{"DELETE repos/o/r/issues/7/labels/status:blocked", "POST repos/o/r/issues/7/labels -f labels[]=status:in-progress"} {
		if !strings.Contains(got, want) {
			t.Errorf("calls are missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "type:work") {
		t.Errorf("only status labels may be touched:\n%s", got)
	}

	// A repo without that status label is left alone.
	calls2 := fakeGH(t)
	l.labels = map[string]bool{}
	l.mirror(context.Background(), item(8, "status:blocked"), "review")
	if c := calls2(); len(c) != 1 || c[0] != "" {
		t.Errorf("a repo without status:review got calls: %q", c)
	}
}

// The loop must see settings pushed to the default branch even when its
// checkout is behind and has never pulled them.
func TestSettingsComeFromOrigin(t *testing.T) {
	dir := t.TempDir()
	git := func(in string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = in
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	origin, a, b := filepath.Join(dir, "origin.git"), filepath.Join(dir, "a"), filepath.Join(dir, "b")
	git(dir, "init", "-q", "--bare", "-b", "main", origin)
	git(dir, "clone", "-q", origin, a)
	git(a, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "first")
	git(a, "push", "-q", "origin", "main")
	git(dir, "clone", "-q", origin, b) // the stale checkout

	os.MkdirAll(filepath.Join(a, ".remux"), 0o755)
	os.WriteFile(filepath.Join(a, SettingsFile), []byte(`{"mode":"company","test":"make check"}`), 0o644)
	git(a, "add", ".")
	git(a, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "settings")
	git(a, "push", "-q", "origin", "main")

	l := &Loop{Repo: b}
	s, err := l.loadSettings(context.Background())
	if err != nil || s.Mode != "company" || s.Test != "make check" {
		t.Fatalf("loadSettings = %+v, %v; want the pushed settings", s, err)
	}
}

func TestGitReason(t *testing.T) {
	push := "To https://github.com/o/r.git\n ! [rejected]        issue-1 -> issue-1 (fetch first)\nerror: failed to push some refs to 'https://github.com/o/r.git'\nhint: Updates were rejected"
	if got := gitReason(push, nil); !strings.Contains(got, "[rejected]") || !strings.Contains(got, "failed to push") || strings.Contains(got, "hint") {
		t.Errorf("gitReason = %q", got)
	}
	if got := gitReason("fatal: not a git repository\n", nil); got != "fatal: not a git repository" {
		t.Errorf("gitReason = %q", got)
	}
}

func TestPromptConflict(t *testing.T) {
	p := Prompt(Run{Role: "review", Number: 3, Conflict: "origin/main"})
	if !strings.Contains(p, "conflicts with origin/main") || !strings.Contains(p, "git merge origin/main") {
		t.Errorf("review prompt does not hand over the conflict:\n%s", p)
	}
	if strings.Contains(Prompt(Run{Role: "review", Number: 3}), "conflicts with") {
		t.Error("a clean branch must not mention a conflict")
	}
}

func TestPromptFindings(t *testing.T) {
	p := Prompt(Run{Role: "fix", Number: 3, Findings: "- [P2] Remove the extra offset"})
	if !strings.Contains(p, "# To fix") || !strings.Contains(p, "[P2] Remove the extra offset") ||
		!strings.Contains(p, "Do not review the diff again") {
		t.Errorf("review prompt does not hand over the findings:\n%s", p)
	}
	if strings.Contains(Prompt(Run{Role: "review", Number: 3}), "# To fix") {
		t.Error("a run without findings must not have a findings section")
	}
}

// The two outputs are what codex exec review printed for a real bug and for
// the fixed branch.
func TestHasFindings(t *testing.T) {
	bug := "The new average function produces incorrect results.\n\nReview comment:\n\n" +
		"- [P2] Remove the extra offset from the average — /tmp/rv/m.py:5-5\n  For nonempty inputs..."
	clean := "The new avg function correctly computes arithmetic means. No actionable regressions were identified."
	if !hasFindings(bug) {
		t.Error("a review with a finding read as clean")
	}
	if hasFindings(clean) {
		t.Error("a clean review read as having findings")
	}
}

func TestParseFindings(t *testing.T) {
	review := "The new average function is wrong.\n\nReview comment:\n\n" +
		"- [P2] Remove the extra offset — /r/wt/m.py:5-5\n  For nonempty inputs, `+ 1` is wrong.\n  Return the quotient.\n" +
		"- [P1] Guard empty input — /r/wt/pkg/m.py:4-6\n  len(xs) can be 0.\n"
	overall, cs, ok := parseFindings(review, "/r/wt")
	if !ok || overall != "The new average function is wrong." || len(cs) != 2 {
		t.Fatalf("parseFindings = %q, %+v, %v", overall, cs, ok)
	}
	want := ReviewComment{Path: "m.py", Line: 5, Side: "RIGHT",
		Body: "**[P2] Remove the extra offset**\n\nFor nonempty inputs, `+ 1` is wrong.\nReturn the quotient."}
	if cs[0] != want {
		t.Errorf("first comment = %+v", cs[0])
	}
	if c := cs[1]; c.Path != "pkg/m.py" || c.StartLine != 4 || c.Line != 6 {
		t.Errorf("range comment = %+v", c)
	}
	if _, _, ok := parseFindings("- [P2] Outside — /elsewhere/m.py:5-5\n", "/r/wt"); ok {
		t.Error("a finding outside the worktree must not become an inline comment")
	}
}

func TestRetry(t *testing.T) {
	l := &Loop{}
	runs := 0
	out, ok := l.retry(func() (int, string) {
		runs++
		if runs < 3 {
			return 1, ""
		}
		return 0, "done"
	})
	if !ok || out != "done" || runs != 3 {
		t.Errorf("retry = %q, %v after %d runs", out, ok, runs)
	}
	runs = 0
	if _, ok := l.retry(func() (int, string) { runs++; return 0, "" }); ok || runs != ReviewTries {
		t.Errorf("a run with no output must count as a crash: ok=%v runs=%d", ok, runs)
	}
}

func TestVerdict(t *testing.T) {
	for in, want := range map[string]string{
		"RESOLVED\nRebased and pushed.": "RESOLVED",
		"**ESCALATED**: needs Kevin":    "ESCALATED",
		"resolved. fixed the push":      "RESOLVED",
		"":                              "",
		"I think this is resolved":      "I",
	} {
		if got := verdict(in); got != want {
			t.Errorf("verdict(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBriefing(t *testing.T) {
	b := Briefing([]string{"viminizer/educenter  /Users/mac/dev/envoy/educenter"})
	for _, want := range []string{"viminizer/educenter", "Never run tmux attach", "loop-*", "short sentences"} {
		if !strings.Contains(b, want) {
			t.Errorf("briefing is missing %q", want)
		}
	}
}

// A PR goes to review even when the agent also marked the rest of its issue
// blocked - the case that left five educenter PRs unreviewed.
func TestSettleReviewsPRWhenIssueBlocked(t *testing.T) {
	calls := fakeGH(t)
	l := &Loop{gh: GH{Slug: "o/r"}}
	after := item(5, "blocked", "done:full")
	if err := l.settle(context.Background(), "t", after, &Item{Number: 9}, "full", "/wt", "b", 0); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(calls(), "\n")
	if !strings.Contains(got, "repos/o/r/issues/9/labels -f labels[]=needs-review") {
		t.Errorf("the PR was not sent to review:\n%s", got)
	}
	if !strings.Contains(got, "DELETE repos/o/r/issues/5/labels/ready") {
		t.Errorf("the blocked issue kept its ready label:\n%s", got)
	}
}

func TestUsageLimit(t *testing.T) {
	seoul := time.FixedZone("KST", 9*3600)
	now := time.Date(2026, 10, 2, 12, 56, 0, 0, seoul)
	cases := []struct {
		out  string
		want time.Time
	}{
		{"You've hit your session limit · resets 1:50pm (Asia/Seoul)", time.Date(2026, 10, 2, 13, 50, 0, 0, seoul)},
		{"You've hit your session limit · resets 3am", time.Date(2026, 10, 3, 3, 0, 0, 0, seoul)},
		{"You've hit your usage limit. Upgrade to Pro or try again at 3:53 AM.", time.Date(2026, 10, 3, 3, 53, 0, 0, seoul)},
		{"You've hit your usage limit. Upgrade to Pro or try again in 1 day 2 hours 5 minutes.", now.Add(26*time.Hour + 5*time.Minute)},
		{"Usage limit reached", now.Add(LimitWait)},
	}
	for _, c := range cases {
		got, ok := usageLimit(c.out, now)
		if !ok || !got.Equal(c.want) {
			t.Errorf("usageLimit(%q) = %v, %v; want %v", c.out, got, ok, c.want)
		}
	}
	if _, ok := usageLimit("go test ./... FAIL: TestRateLimiter", now); ok {
		t.Error("an ordinary test failure is not a usage limit")
	}
}

func TestClaimant(t *testing.T) {
	it := item(3, "ready", "by:loop-claude2-x", "by:loop-claude-x", "by:loop-codex-x")
	if got := claimant(it); got != "by:loop-claude-x" {
		t.Errorf("claimant = %q", got)
	}
	if got := claimant(item(3, "ready")); got != "" {
		t.Errorf("claimant of an unmarked issue = %q", got)
	}
	if free(item(4, "ready", "by:loop-claude-x"), "full") {
		t.Error("an issue another loop is claiming is not free")
	}
}

func TestOrphan(t *testing.T) {
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	pr := func(draft bool, head string, labels ...string) Item {
		it := item(1, labels...)
		it.IsDraft, it.Head = draft, head
		return it
	}
	if !orphan(pr(true, "issue-879-backend"), old, now) {
		t.Error("an old unlabelled loop draft must be adopted")
	}
	for name, c := range map[string]struct {
		p       Item
		created time.Time
	}{
		"fresh":          {pr(true, "issue-1"), now.Add(-time.Minute)},
		"not a draft":    {pr(false, "issue-1"), old},
		"not a loop PR":  {pr(true, "feature-x"), old},
		"already queued": {pr(true, "issue-1", "needs-review"), old},
		"stuck":          {pr(true, "issue-1", "needs-human"), old},
		"being reviewed": {pr(true, "issue-1", "wip:review"), old},
	} {
		if orphan(c.p, c.created, now) {
			t.Errorf("%s: must not be adopted", name)
		}
	}
}

func TestDropWorktree(t *testing.T) {
	dir := t.TempDir()
	run := func(in string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		cmd.Dir = in
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	origin, repo := filepath.Join(dir, "origin.git"), filepath.Join(dir, "repo")
	run(dir, "init", "-q", "--bare", "-b", "main", origin)
	run(dir, "clone", "-q", origin, repo)
	run(repo, "commit", "-q", "--allow-empty", "-m", "first")
	run(repo, "push", "-q", "origin", "main")
	wt := func(name string) string {
		p := filepath.Join(repo, ".claude", "worktrees", name)
		run(repo, "worktree", "add", "-q", "-b", name, p, "main")
		return p
	}
	l := &Loop{Repo: repo}
	ctx := context.Background()
	gone := func(p string) bool { _, err := os.Stat(p); return os.IsNotExist(err) }

	clean := wt("issue-1")
	l.dropWorktree(ctx, clean, "issue-1", nil)
	if !gone(clean) {
		t.Error("a clean, pushed worktree of a blocked run was kept")
	}
	local := wt("issue-2")
	run(local, "commit", "-q", "--allow-empty", "-m", "only here")
	l.dropWorktree(ctx, local, "issue-2", nil)
	if gone(local) {
		t.Error("a worktree with an unpushed commit was removed")
	}
	withPR := wt("issue-3")
	l.dropWorktree(ctx, withPR, "issue-3", &Item{Number: 9})
	if gone(withPR) {
		t.Error("a worktree whose PR is open was removed")
	}
}

func TestStaleClaims(t *testing.T) {
	on := map[string]int{"loop-claude-x": 5, "loop-claude2-x": 9}
	items := []Item{
		item(5, "wip:backend", "by:loop-claude-x"),    // its loop is on it
		item(172, "wip:backend", "by:loop-codex2-x"),  // loop gone
		item(196, "wip:backend", "by:loop-claude2-x"), // loop moved on to #9
		item(7, "by:loop-gone-x"),                     // claim still being made
	}
	got := staleIn(items, on)
	if len(got) != 2 {
		t.Fatalf("stale = %v, want #172 and #196", got)
	}
	for _, k := range []string{"172 by:loop-codex2-x", "196 by:loop-claude2-x"} {
		if _, ok := got[k]; !ok {
			t.Errorf("%s should be stale", k)
		}
	}
	if n := liveClaims(item(5, "wip:backend", "by:loop-claude-x", "by:loop-codex2-x"), map[string]Item{"5 by:loop-codex2-x": {}}); n != 1 {
		t.Errorf("liveClaims = %d, want 1", n)
	}
}
