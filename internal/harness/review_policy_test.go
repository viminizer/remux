package harness

import (
	"context"
	"fmt"
	"github.com/viminizer/remux/internal/tmux"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewClassification(t *testing.T) {
	for _, tc := range []struct {
		name, path, changed, want string
		lines                     int
	}{
		{"docs mentioning payments", "docs/payments.md", "DELETE FROM invoice", "small", 900},
		{"small handler", "backend/ClassController.java", "return page;", "small", 25},
		{"normal handler", "backend/ClassController.java", "return page;", "normal", 150},
		{"tiny finance change", "backend/BillingService.java", "return amount;", "deep", 1},
		{"removed authorization", "backend/ClassController.java", "@PreAuthorize(hasRole('ADMIN'))", "deep", 1},
		{"delete endpoint", "backend/ClassController.java", "@DeleteMapping", "deep", 1},
		{"migration", "backend/db/changelog/04.xml", "change", "deep", 1},
		{"binary change", "web/asset.bin", "", "deep", -1},
		{"large PR", "backend/ClassService.java", "return page;", "deep", 1200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyReview([]reviewChange{{tc.path, tc.lines}}, tc.changed); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestSharedReviewRotation(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "service.go"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "base")
	wt := filepath.Join(t.TempDir(), "review")
	runGit(t, repo, "worktree", "add", "-b", "review", wt)
	// Each coordinator sees the same rotation through the common Git directory.
	if err := os.WriteFile(filepath.Join(wt, "service.go"), []byte(strings.Repeat("normal change\n", 100)), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, wt, "commit", "-am", "normal")
	first := &Loop{Repo: repo, Agent: "codex"}
	second := &Loop{Repo: wt, Agent: "claude"}
	ctx := context.Background()
	a, err := first.nextReviewChoice(ctx, wt, "main")
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.nextReviewChoice(ctx, wt, "main")
	if err != nil {
		t.Fatal(err)
	}
	if a.Agent != "claude" || a.Model != "sonnet" || b.Agent != "codex" {
		t.Fatalf("rotation: %+v then %+v", a, b)
	}
	// A one-line payment change must receive deeper review, using the next turn.
	base := strings.TrimSpace(runGit(t, wt, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(wt, "BillingService.java"), []byte("amount\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, wt, "add", ".")
	runGit(t, wt, "commit", "-m", "billing")
	c, err := second.nextReviewChoice(ctx, wt, base)
	if err != nil {
		t.Fatal(err)
	}
	if c.Class != "deep" || c.Agent != "claude" || c.Model != "opus" || c.Effort != "high" {
		t.Fatalf("deep route: %+v", c)
	}
	// Fixed policy preserves an explicitly configured reviewer.
	first.set.ReviewPolicy = "fixed"
	first.Agent = "claude"
	first.Model = "opus"
	c, err = first.nextReviewChoice(ctx, wt, base)
	if err != nil {
		t.Fatal(err)
	}
	if c.Agent != "claude" || c.Model != "opus" || c.Class != "fixed" {
		t.Fatalf("fixed route: %+v", c)
	}
}

func TestReviewRoutingExcludesUnmergedBaseChanges(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "config", "user.email", "test@example.com")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("service.go", "base\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "base")
	runGit(t, repo, "checkout", "-b", "review")
	write("service.go", "PR change\n")
	runGit(t, repo, "commit", "-am", "small PR")
	runGit(t, repo, "checkout", "main")
	write("service.go", "main change\n")
	write("BillingService.java", "amount\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "unrelated billing and conflicting service")
	runGit(t, repo, "checkout", "review")
	if _, err := (&Loop{}).git(context.Background(), repo, "merge", "--no-edit", "main"); err == nil {
		t.Fatal("fixture must conflict, as reviewOnce does before routing")
	}
	runGit(t, repo, "merge", "--abort")
	l := &Loop{Repo: repo}
	c, err := l.nextReviewChoice(context.Background(), repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if c.Class != "small" {
		t.Fatalf("small PR routed using unrelated base changes: %+v", c)
	}
}

func TestTimedOutReviewStopsChildBeforeHandoff(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"claude": "#!/bin/sh\ncat >/dev/null\necho started > started.txt\n(sleep 2; echo late-repair > late.txt) &\nwait\n",
		"codex":  "#!/bin/sh\ncat >/dev/null\n[ ! -f late.txt ] || exit 7\necho REVIEW COMPLETE\n",
		"tmux":   "#!/bin/sh\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", root)
	l := Loop{Agent: "claude", Role: "review", Repo: root, Session: "loop-test", Tmux: tmux.New(), runTimeout: 500 * time.Millisecond}
	choice, code, summary := l.inspectReview(context.Background(), root, "Review", choiceFor("normal", ""), 42)
	if choice.Agent != "codex" || code != 0 || !completedReview(summary) {
		t.Fatalf("timed-out child affected fallback: choice=%+v code=%d output=%q", choice, code, summary)
	}
	if _, err := os.Stat(filepath.Join(root, "started.txt")); err != nil {
		t.Fatal("fixture reviewer never started", err)
	}
	// A descendant with redirected output could outlive Wait even without
	// holding its pipes open. It must not edit the branch after handoff either.
	time.Sleep(2100 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "late.txt")); !os.IsNotExist(err) {
		t.Fatal("timed-out child changed the worktree after handoff", err)
	}
}

func TestReviewHandoffPreservesWorkAndEvidence(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			// These fake CLIs touch only this test directory. Claude leaves a repair;
			// Codex must see both that repair and the previous review evidence.
			claude := "#!/bin/sh\ncat >/dev/null\necho repair > preserved.txt\necho 'Evidence: a real caller fails at service.go:4'\necho REVIEW INCOMPLETE\n"
			codex := "#!/bin/sh\ninput=$(cat)\n[ -f preserved.txt ] || exit 9\ncase \"$input\" in *'a real caller fails at service.go:4'*) ;; *) exit 8 ;; esac\n"
			if complete {
				codex += "echo 'Confirmed bugs fixed: 1'\necho REVIEW COMPLETE\n"
			} else {
				codex += "echo REVIEW INCOMPLETE\n"
			}
			for name, body := range map[string]string{"claude": claude, "codex": codex, "tmux": "#!/bin/sh\nexit 0\n"} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("HOME", root)
			l := Loop{Agent: "claude", Model: "sonnet", Role: "review", Repo: root, Session: "loop-test", Tmux: tmux.New()}
			choice, code, summary := l.inspectReview(context.Background(), root, "Inspect this change", choiceFor("normal", ""), 42)
			if code != 0 || choice.Agent != "codex" || completedReview(summary) != complete {
				t.Fatalf("choice=%+v code=%d result=%q", choice, code, summary)
			}
			if _, err := os.Stat(filepath.Join(root, "preserved.txt")); err != nil {
				t.Fatal("repair was lost", err)
			}
		})
	}
}

func TestIncompleteReviewNeverCompletes(t *testing.T) {
	for _, out := range []string{"", "No issues found", "Build passed", "REVIEW INCOMPLETE", "REVIEW COMPLETE\nREVIEW INCOMPLETE", "Example: REVIEW COMPLETE"} {
		if completedReview(out) {
			t.Fatalf("incomplete review accepted: %q", out)
		}
	}
	if !completedReview("No real issues found.\nConfirmed bugs fixed: 0\nREVIEW COMPLETE") {
		t.Fatal("completed review rejected")
	}
	if reportedFixedBugs("No findings") != nil {
		t.Fatal("missing measurement fabricated as zero")
	}
	if n := reportedFixedBugs("Confirmed bugs fixed: 2\nREVIEW COMPLETE"); n == nil || *n != 2 {
		t.Fatal("repair count missing")
	}
}

func TestReviewEffortAndFallback(t *testing.T) {
	c := choiceFor("deep", "codex")
	fallback := alternateReviewer(c)
	if fallback.Agent != "codex" || fallback.Model != "" || fallback.Effort != "high" {
		t.Fatalf("fallback: %+v", fallback)
	}
	for _, choice := range []reviewChoice{c, fallback} {
		l := Loop{Agent: choice.Agent, Model: choice.Model, effort: choice.Effort}
		if !strings.Contains(strings.Join(l.command("/wt", "/last"), " "), "high") {
			t.Fatal("deep effort not passed to CLI")
		}
	}
	if _, err := ParseSettings([]byte(`{"review_policy":"unknown"}`)); err == nil {
		t.Fatal("invalid policy accepted")
	}
}
