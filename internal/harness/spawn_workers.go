package harness

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/viminizer/remux/internal/tmux"
)

const customWorkersPerRole = 5

var workerScope = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)
var mentionedCode = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9_]*\.(?:java|tsx|ts|sql)\b`)

func customCount(loops []tmux.Loop, slug, role string) int {
	n := 0
	for _, o := range loops {
		if o.Slug == slug && o.Role == role && o.Custom {
			n++
		}
	}
	return n
}

func codeOverlap(a, b string) bool {
	files := map[string]bool{}
	for _, f := range mentionedCode.FindAllString(a, -1) {
		files[f] = true
	}
	for _, f := range mentionedCode.FindAllString(b, -1) {
		if files[f] {
			return true
		}
	}
	return false
}

func (l *Loop) spawnWorkers(ctx context.Context, summary string) error {
	var usage []providerUsage
	loaded := false
	var checkedAt time.Time
	for _, line := range controlLines(summary) {
		f := strings.Fields(line)
		if len(f) != 5 || f[0] != "SPAWN" {
			continue
		}
		role, scope, class := f[1], f[2], f[4]
		n, err := strconv.Atoi(f[3])
		if n <= 0 || err != nil || (role != "build" && role != "review") || !workerScope.MatchString(scope) || (class != "normal" && class != "deep") {
			continue
		}
		unlock, err := l.queueLock(ctx)
		if err != nil {
			return err
		}
		if unlock == nil {
			l.spawnNote("Spawn deferred: queue is changing")
			continue
		}
		err = func() error {
			defer unlock()
			loops, err := l.Tmux.Loops(ctx)
			if err != nil {
				return err
			}
			roleExists, allStopped := false, true
			for _, o := range loops {
				if o.Slug == l.gh.Slug && o.Role == role {
					roleExists = true
					allStopped = allStopped && o.Stop != ""
				}
			}
			if roleExists && allStopped {
				l.spawnNote("Spawn deferred: all " + role + " loops have stop requests")
				return nil
			}
			if customCount(loops, l.gh.Slug, role) >= customWorkersPerRole {
				l.spawnNote("Spawn deferred: five custom " + role + " loops already running")
				return nil
			}
			kind := "issue"
			if role == "review" {
				kind = "pr"
			}
			it, err := l.readSpawnItem(ctx, kind, n)
			if err != nil {
				return err
			}
			if (role == "build" && (it.State != "OPEN" || !free(it, scope))) || (role == "review" && !reviewEligible(it)) {
				l.spawnNote(fmt.Sprintf("Spawn skipped: #%d is no longer eligible", n))
				return nil
			}
			for _, o := range loops {
				if o.Slug != l.gh.Slug || o.Role != role {
					continue
				}
				pending, err := l.Tmux.LoopOption(ctx, o.Name, assignmentOption)
				if err != nil {
					return err
				}
				if o.Issue == n || pending == fmt.Sprintf("%s:%d", kind, n) {
					return nil
				}
				activeID := o.Issue
				if activeID == 0 && strings.HasPrefix(pending, "issue:") {
					activeID, _ = strconv.Atoi(strings.TrimPrefix(pending, "issue:"))
				}
				if role == "build" && activeID > 0 && (o.Scope == scope || o.Scope == "full" || scope == "full") {
					active, err := l.readSpawnItem(ctx, "issue", activeID)
					if err != nil {
						return err
					}
					if codeOverlap(it.Body, active.Body) {
						l.spawnNote(fmt.Sprintf("Spawn deferred: #%d overlaps active issue #%d", n, active.Number))
						return nil
					}
				}
			}
			if !loaded || time.Since(checkedAt) > 2*time.Minute {
				usage, err = readSpawnUsage(ctx)
				if err != nil && role != "build" {
					l.spawnNote(err.Error())
					return nil
				}
				loaded = err == nil
				checkedAt = time.Now()
			}
			// Missing quota data must not block an installed Claude builder.
			if role == "build" && !hasClaudeUsage(usage) {
				usage = append(usage, providerUsage{Provider: "Claude"})
			}
			var available []providerUsage
			for _, p := range usage {
				if _, err := exec.LookPath(strings.ToLower(p.Provider)); err == nil {
					available = append(available, p)
				}
			}
			choice, ok := chooseSpawn(available, loops, role, class, spawnHolds())
			if !ok {
				l.spawnNote("Spawn deferred: no supported model has verified usage headroom")
				return nil
			}
			if _, err := exec.LookPath(choice.Agent); err != nil {
				l.spawnNote("Spawn deferred: " + choice.Agent + " CLI unavailable")
				return nil
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			name := customName(l.gh.Slug, role, n)
			instructions, _ := Instructions(l.Repo, scope, role)
			instructions += "\nKevin's 2026-10-14 usage policy supersedes older project guards: create new Codex workers only while actual weekly remaining usage is above 30%, with no predicted task reservations. Claude builders may be created regardless of remaining usage or missing quota data. Native provider limits still apply, and existing work is preserved.\n"
			if role == "build" && scope == "backend" {
				instructions += "\nLatest user authorization: newly filed ready backend prerequisites may be implemented by this budget-selected builder. Older notes saying the backend queue was finished or requiring a different coding provider do not override this assigned scope. Existing active ownership and genuine external blocks remain binding.\n"
			}
			instructions += fmt.Sprintf("\nThis is a temporary %s loop assigned ONLY %s #%d in scope %s. Kevin authorizes routine capacity/model decisions without another approval. Read project rules and the complete task/prerequisites. Work only this task, preserve existing ownership and worktrees, and report actual verification/limits. Builders open drafts with needs-review and never merge. Reviewers post findings and verify GitHub accepted them. Do not broaden scope, weaken permissions, start redundant runtimes or delete unfinished work. The harness exits this session after the assigned run.\n", role, kind, n, scope)
			argv := []string{exe, "loop", "--role", role, "--agent", choice.Agent, "--repo", l.Repo, "--session", name, "--scope", scope, "--mode", "add", "--instructions", instructions, "--task", strconv.Itoa(n), "--budget-key", choice.Key, "--reserve", fmt.Sprint(choice.Reserve), "--effort", choice.Effort}
			if choice.Model != "" {
				argv = append(argv, "--model", choice.Model)
			}
			if err := l.Tmux.NewLoopSession(ctx, name, l.Repo, argv); err != nil {
				return err
			}
			// Publish reservation metadata before another spawn sees this session.
			for key, value := range map[string]string{"@loop_custom": "1", "@loop_budget_key": choice.Key, "@loop_reserve": fmt.Sprint(choice.Reserve), "@loop_slug": l.gh.Slug, "@loop_role": role, "@loop_assignment": fmt.Sprintf("%s:%d", kind, n)} {
				if err := l.Tmux.SetLoopOption(ctx, name, key, value); err != nil {
					return err
				}
			}
			l.spawnNote(fmt.Sprintf("Started %s for #%d: %s %s, %.0f%% headroom", name, n, choice.Agent, modelName(choice.Model), choice.Remaining))
			l.recordOutcome("custom-started", n, 0)
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

func (l *Loop) readSpawnItem(ctx context.Context, kind string, n int) (Item, error) {
	var it Item
	fields := itemFields + ",body"
	if kind == "pr" {
		fields += ",isDraft,headRefName,headRefOid"
	}
	err := l.gh.JSON(ctx, &it, kind, "view", strconv.Itoa(n), "--json", fields)
	return it, err
}

func customName(slug, role string, n int) string {
	name := regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(strings.ToLower(strings.ReplaceAll(slug, "/", "-")), "-")
	if len(name) > 30 {
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(slug)))
		name = name[:23] + "-" + digest[:6]
	}
	return fmt.Sprintf("loop-auto-%s-%s-%d", name, role, n)
}

func (l *Loop) spawnNote(note string) {
	log.Printf("supervisor: %s", note)
	l.idleAuditSummary = note + "\n" + l.idleAuditSummary
}

func hasClaudeUsage(usage []providerUsage) bool {
	for _, p := range usage {
		if strings.EqualFold(p.Provider, "claude") {
			return true
		}
	}
	return false
}
