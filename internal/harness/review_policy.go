package harness

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type reviewChoice struct{ Agent, Model, Effort, Class string }
type reviewChange struct {
	Path  string
	Lines int
}

var sensitiveReviewPath = regexp.MustCompile(`(?i)(auth|permission|security|payment|billing|invoice|expense|finance|payroll|ledger|migration|db/changelog|schema|receipt|currency|stripe|schoolfee)`)
var sensitiveReviewCode = regexp.MustCompile(`(?i)(\bDELETE\s+FROM\b|\bDROP\s+(TABLE|COLUMN)\b|@DeleteMapping|@PreAuthorize|@Secured|\bdelete(All|By|Object)|\bremovePermission|\brevoke|\bhasAuthority|\bhasRole)`)

func classifyReview(changes []reviewChange, added string) string {
	lines := 0
	docs := true
	for _, c := range changes {
		lines += c.Lines
		doc := strings.HasSuffix(c.Path, ".md") || strings.HasSuffix(c.Path, ".rst") || strings.HasSuffix(c.Path, ".txt")
		if !doc {
			docs = false
			if sensitiveReviewPath.MatchString(c.Path) || c.Lines < 0 {
				return "deep"
			}
		}
	}
	if !docs && sensitiveReviewCode.MatchString(added) {
		return "deep"
	}
	if docs || len(changes) <= 3 && lines <= 80 {
		return "small"
	}
	if len(changes) > 20 || lines > 1000 {
		return "deep"
	}
	return "normal"
}

func choiceFor(class, last string) reviewChoice {
	agent := "claude"
	if class != "small" && last == "claude" {
		agent = "codex"
	}
	c := reviewChoice{Agent: agent, Effort: "medium", Class: class}
	if agent == "claude" {
		c.Model = "sonnet"
	}
	if class == "deep" {
		c.Effort = "high"
		if agent == "claude" {
			c.Model = "opus"
		}
	}
	return c
}

// Rotation is shared by all review loops/worktrees in the repository. Small
// PRs never consume a normal/deep turn. Failure to read/write state fails closed.
func (l *Loop) nextReviewChoice(ctx context.Context, wt, base string) (reviewChoice, error) {
	if l.fixedReviewer() {
		effort := "medium"
		if l.Task > 0 && l.Effort != "" {
			effort = l.Effort
		}
		class := "fixed"
		if l.Task > 0 && effort == "high" {
			class = "deep"
		}
		return reviewChoice{Agent: l.Agent, Model: l.Model, Effort: effort, Class: class}, nil
	}
	// A failed pre-review merge leaves HEAD behind the base tip. Compare
	// from the common ancestor so unrelated base changes cannot affect routing.
	stat, err := l.git(ctx, wt, "diff", "--numstat", base+"...HEAD")
	if err != nil {
		return reviewChoice{}, err
	}
	var changes []reviewChange
	for _, row := range strings.Split(stat, "\n") {
		fields := strings.SplitN(row, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		a, e1 := strconv.Atoi(fields[0])
		d, e2 := strconv.Atoi(fields[1])
		n := a + d
		if e1 != nil || e2 != nil {
			n = -1
		}
		changes = append(changes, reviewChange{fields[2], n})
	}
	diff, err := l.git(ctx, wt, "diff", "--unified=0", base+"...HEAD")
	if err != nil {
		return reviewChoice{}, err
	}
	var added strings.Builder
	for _, line := range strings.Split(diff, "\n") {
		if (strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-")) && !strings.HasPrefix(line, "+++") && !strings.HasPrefix(line, "---") {
			added.WriteString(line[1:] + "\n")
		}
	}
	class := classifyReview(changes, added.String())
	if class == "small" {
		return choiceFor(class, ""), nil
	}
	unlock, err := l.queueLock(ctx)
	if err != nil {
		return reviewChoice{}, err
	}
	if unlock == nil {
		return reviewChoice{}, fmt.Errorf("review rotation busy; retry next pass")
	}
	defer unlock()
	dir, err := l.git(ctx, l.Repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return reviewChoice{}, err
	}
	path := filepath.Join(dir, "remux-review-last")
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return reviewChoice{}, err
	}
	c := choiceFor(class, strings.TrimSpace(string(b)))
	if err := os.WriteFile(path, []byte(c.Agent+"\n"), 0600); err != nil {
		return reviewChoice{}, err
	}
	return c, nil
}

func (l *Loop) selectReviewer(ctx context.Context, c reviewChoice) {
	l.Agent, l.Model, l.effort = c.Agent, c.Model, c.Effort
	l.opt(ctx, "@loop_reviewer", c.Agent)
	l.opt(ctx, "@loop_review_model", c.Model)
	l.opt(ctx, "@loop_review_effort", c.Effort)
	l.runTimeout = 20 * time.Minute
	if c.Class == "deep" {
		l.runTimeout = 40 * time.Minute
	}
}

// Accept the status on its own or followed by the reported count. Do not
// interpret an arbitrary mention, example or incomplete verdict as completion.
var reviewComplete = regexp.MustCompile(`(?im)^[ \t]*(?:\*\*)?REVIEW COMPLETE(?:\*\*)?(?:[ \t]*[.!]?[ \t]*$|[ \t]*[-—–:;,][ \t]*Confirmed bugs fixed:[ \t]*[0-9]+(?:[.!]?(?:[ \t]+[^\r\n]*)?)?[ \t]*$)`)
var reviewIncomplete = regexp.MustCompile(`(?im)^[ \t]*(?:\*\*)?REVIEW INCOMPLETE\b`)

func completedReview(out string) bool {
	var plain strings.Builder
	fenced := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") || strings.HasPrefix(strings.TrimSpace(line), "~~~") {
			fenced = !fenced
			continue
		}
		if !fenced {
			plain.WriteString(line + "\n")
		}
	}
	return reviewComplete.MatchString(plain.String()) && !reviewIncomplete.MatchString(plain.String())
}

func alternateReviewer(c reviewChoice) reviewChoice {
	if c.Agent == "claude" {
		c.Agent, c.Model = "codex", ""
	} else {
		c.Agent, c.Model = "claude", "sonnet"
		if c.Effort == "high" {
			c.Model = "opus"
		}
	}
	return c
}

func modelName(model string) string {
	if model == "" {
		return "CLI default"
	}
	return model
}

var confirmedBugs = regexp.MustCompile(`(?im)(?:^[ \t]*|[-—–:;,][ \t]*)Confirmed bugs fixed:[ \t]*([0-9]+)\b`)

func reportedFixedBugs(summary string) *int {
	m := confirmedBugs.FindStringSubmatch(summary)
	if m == nil {
		return nil
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return nil
	}
	return &n
}

func (l *Loop) inspectReview(ctx context.Context, wt, prompt string, choice reviewChoice, n int) (reviewChoice, int, string) {
	code, summary := l.agent(ctx, wt, prompt)
	if code != 0 || l.limited() || !completedReview(summary) {
		if l.Task > 0 || l.fixedReviewer() {
			return choice, code, summary
		}
		if ctx.Err() != nil {
			return choice, -1, summary
		}
		previous := choice
		choice = alternateReviewer(choice)
		l.limit = time.Time{}
		l.selectReviewer(ctx, choice)
		l.recordOutcome("review-handoff", n, n)
		log.Printf("review handoff: %s → %s", previous.Agent, choice.Agent)
		// The existing worktree may contain useful uncommitted repairs. Preserve
		// them, inspect its status, and continue only the unfinished review.
		handoff := fmt.Sprintf("\n# Review handoff\nPrevious reviewer: %s. Exit: %d.\nInspect git status and existing commits first; preserve earlier work. Continue unfinished inspection and repair; do not restart a broad audit. Previous output (may be incomplete):\n%s\n", previous.Agent, code, tail(summary, 100))
		code, summary = l.agent(ctx, wt, prompt+handoff)
	}
	return choice, code, summary
}

// An explicit loop choice overrides project rotation, including failure handoffs.
func (l *Loop) fixedReviewer() bool {
	return l.ReviewPolicy == "fixed" || (l.ReviewPolicy == "" && l.set.ReviewPolicy == "fixed")
}
