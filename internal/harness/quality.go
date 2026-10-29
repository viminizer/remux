package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/viminizer/remux/internal/tmux"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var qualityCategories = []string{"UI screens", "backend read flows", "backend write workflows", "shared components"}

// Quality evidence lives in the common Git directory, shared by every worktree
// and retained when a task worktree or loop session is removed.
func (l *Loop) qualityDir(ctx context.Context) (string, error) {
	dir, err := l.git(ctx, l.Repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "remux-code-quality")
	return dir, os.MkdirAll(dir, 0o700)
}

func qualityLockFile(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, "cycle.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, nil
		}
		return nil, err
	}
	return f, nil
}

func releaseQualityLock(f *os.File) { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }

func qualityLock(dir string) (func(), error) {
	f, err := qualityLockFile(dir)
	if err != nil || f == nil {
		return nil, err
	}
	return func() { releaseQualityLock(f) }, nil
}

// A cycle advances only after a successful runner exit and a durable report.
// This is a scheduling receipt, never a claim that authorizes a merge.
func qualityCycle(dir string) (int, string, error) {
	for n := 0; ; n++ {
		cycle := filepath.Join(dir, fmt.Sprintf("cycle-%06d", n+1))
		b, err := os.ReadFile(filepath.Join(cycle, "complete.json"))
		if os.IsNotExist(err) {
			return n, cycle, os.MkdirAll(cycle, 0o700)
		}
		if err != nil {
			return 0, "", err
		}
		if !qualityEvidenceValid(cycle, b) {
			return n, cycle, nil // interrupted evidence resumes the recorded cycle
		}
		if _, err := os.Stat(filepath.Join(cycle, "runner-ok")); os.IsNotExist(err) {
			return n, cycle, nil // interrupted runner must resume even if it wrote a receipt
		} else if err != nil {
			return 0, "", err
		}
	}
}

func qualityEvidenceValid(cycle string, b []byte) bool {
	var receipt struct {
		Outcome string `json:"outcome"`
	}
	if json.Unmarshal(b, &receipt) != nil || (receipt.Outcome != "clean" && receipt.Outcome != "merged") {
		return false
	}
	report, err := os.ReadFile(filepath.Join(cycle, "report.md"))
	return err == nil && strings.TrimSpace(string(report)) != ""
}

// Each invocation needs its own successful exit and receipt. Retain interrupted
// receipts, including partial JSON, for the next coordinator to inspect.
func prepareQualityRun(cycle string) error {
	if err := os.Remove(filepath.Join(cycle, "runner-ok")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Stat(filepath.Join(cycle, "complete.json")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	archive, err := os.CreateTemp(cycle, "previous-complete-*.json")
	if err != nil {
		return err
	}
	if err := archive.Close(); err != nil {
		return err
	}
	return os.Rename(filepath.Join(cycle, "complete.json"), archive.Name())
}

func (l *Loop) qualityOnce(ctx context.Context) (bool, error) {
	instructions := strings.TrimSpace(l.get(ctx, "@loop_instr"))
	if instructions == "" {
		return false, fmt.Errorf("code-quality requires saved scope instructions")
	}
	if l.Task != 0 {
		return false, fmt.Errorf("code-quality cannot be assigned a queue task")
	}
	dir, err := l.qualityDir(ctx)
	if err != nil {
		return false, err
	}
	lock, err := qualityLockFile(dir)
	if err != nil || lock == nil {
		return false, err
	}
	defer releaseQualityLock(lock)
	if ctx.Err() != nil || l.get(ctx, "@loop_stop") != "" {
		return false, ctx.Err()
	}
	n, cycle, err := qualityCycle(dir)
	if err != nil {
		return false, err
	}
	usage, err := readSpawnUsage(ctx)
	if err != nil {
		return false, err
	}
	loops, err := l.Tmux.Loops(ctx)
	if err != nil {
		return false, err
	}
	// The caller's previous usage-limit note must not block it after reset.
	var peers []tmux.Loop
	for _, loop := range loops {
		if loop.Name != l.Session {
			peers = append(peers, loop)
		}
	}
	var provider []providerUsage
	for _, u := range usage {
		if strings.EqualFold(u.Provider, l.Agent) {
			provider = append(provider, u)
		}
	}
	// Kevin's quality-loop reserve is 20%; ordinary worker policy remains separate.
	if _, ok := chooseSpawnWithCodexFloor(provider, peers, "build", "normal", spawnHolds(), 20); !ok {
		l.opt(ctx, "@loop_stop", "after")
		return false, fmt.Errorf("code-quality provider budget gate closed (20%% reserve); stopped without retrying")
	}
	// Save before any paid process so a killed coordinator leaves a discoverable
	// assignment. Workers must update handoff before source changes or spawning.
	brief := filepath.Join(cycle, "instructions.md")
	if _, err := os.Stat(brief); os.IsNotExist(err) {
		if err := os.WriteFile(brief, []byte(instructions), 0o600); err != nil {
			return false, err
		}
	} else if err != nil {
		return false, err
	}
	if _, err := l.git(ctx, l.Repo, "fetch", "--quiet", "origin"); err != nil {
		return false, err
	}
	// A separate checkout lets the coordinator read repository instructions
	// without running an unrestricted agent in the user's main source checkout.
	workspace := filepath.Join(dir, "coordinator")
	if _, err := os.Stat(workspace); os.IsNotExist(err) {
		if _, err := l.git(ctx, l.Repo, "worktree", "add", "--detach", workspace, "origin/main"); err != nil {
			return false, err
		}
	} else if err != nil {
		return false, err
	}
	if dirty, err := l.git(ctx, workspace, "status", "--porcelain"); err != nil || dirty != "" {
		return false, fmt.Errorf("quality coordinator checkout must be clean; preserve %s: %v", workspace, err)
	}
	if _, err := l.git(ctx, workspace, "checkout", "--detach", "origin/main"); err != nil {
		return false, err
	}
	settings := l.set
	if l.get(ctx, "@loop_mode") == "replace" {
		settings.Instructions = ""
	}
	l.state(ctx, "working", "code-quality: "+qualityCategories[n%len(qualityCategories)])
	if ctx.Err() != nil || l.get(ctx, "@loop_stop") != "" {
		return false, ctx.Err()
	}
	// Remove an interrupted runner's receipt; this invocation must write its own.
	if err := prepareQualityRun(cycle); err != nil {
		return false, err
	}
	l.Model = "gpt-6.1-sol"
	code, output := l.spawnFiles(ctx, workspace, qualityPrompt(l.Repo, dir, cycle, brief, qualityCategories[n%len(qualityCategories)], settings), func(last string) []string {
		return append(l.command(workspace, last), "--model", "gpt-6.1-sol")
	}, []*os.File{lock})
	if err := os.WriteFile(filepath.Join(cycle, "last-run.md"), []byte(output), 0o600); err != nil {
		return true, err
	}
	if code != 0 || l.limited() {
		return true, fmt.Errorf("quality coordinator interrupted; resume %s", cycle)
	}
	if receipt, err := os.ReadFile(filepath.Join(cycle, "complete.json")); err != nil || !qualityEvidenceValid(cycle, receipt) {
		return true, fmt.Errorf("quality cycle unfinished; resume handoff at %s", cycle)
	}
	if err := os.WriteFile(filepath.Join(cycle, "runner-ok"), nil, 0o600); err != nil {
		return true, err
	}
	_, _, err = qualityCycle(dir)
	return true, err
}

func qualityPrompt(repo, evidence, cycle, brief, category string, settings Settings) string {
	return fmt.Sprintf(`You are the fresh coordinator for ONE bounded code-quality cycle.
Repository main checkout (read-only): %s
Persistent private evidence root: %s
Current cycle: %s
Saved scope instructions (mandatory; read them now): %s
Assigned category: %s
Repository settings: %s
Required checks: %s

Read AGENTS.md, RULEBOOK.md, contracts and saved instructions afresh. Your current
directory is a dedicated coordinator checkout: do not edit application source in
it or the main checkout. Read current origin/main when selecting scope; this
checkout may be older. Never scan, claim, relabel or implement ordinary queued
issues. Respect ownership, postponed scope, existing runtime owners and stop flags.
Do not launch unrelated loops or alter live panes. Before each worker spawn check
native usage and honor existing provider budget gates and stop requests. Use Sol workers; never use Astra.

First read all previous cycle reports and the current handoff.md. If any work is
pending, resume exactly that fix, worktree, branch and PR before auditing anything
else. Record phase, ownership, base/candidate SHA, worktree, branch, PR and artifact
paths in handoff.md BEFORE launching each worker or changing source. Maintain
ledger.md in the evidence root. Keep all reports here, never in disposable scratch.
If a prior worker stopped unexpectedly, inspect its recorded worktree/PR and
preserve all changes. Missing/ambiguous evidence is a blocker, never permission
to abandon work. Stop after this single cycle.

Delegate each audit, implementation pass and review to a NEW worker without
inherited history. Supply the brief, exact SHAs, paths, constraints and findings.
The audit worker is read-only except its report. Select one production flow,
normally 3–8 files, compare 2–3 approved peers and report verified consequences.
A clean audit is valid. For actionable findings delegate one bounded fix in its
own isolated worktree/branch from freshly fetched origin/main; never edit first
and isolate later. Follow the target repository's checks and testing policy.

After implementation spawn TWO separate fresh read-only reviewers on the SAME
candidate SHA: one invokes Codex CLI review against the actual base; the other
uses consistency-audit against RULEBOOK, contracts and peers. Record reports and
actual verification. Fix findings with a NEW implementation worker then spawn
TWO NEW reviewers. Unverified findings/disagreements block merge. Post both
reviews, exact reviewed SHA and verification visibly on the PR, even clean
results, and verify GitHub accepted them. Create PRs as drafts.

Merge only where the saved user instructions explicitly authorize it. Before
merge fetch/rebase current origin/main; any base or candidate change invalidates
both reviews and checks, which must be rerun. Require both fresh reviews clean,
accepted PR comments, required checks passed and source-head checks proving the
PR head equals the reviewed SHA. Merge that exact reviewed head without bypasses,
confirm GitHub reports merged, record merge SHA, then clean ONLY the merged task's
clean worktree/branches and task-owned runtimes. Preserve all unfinished changes
on errors/limits and write the handoff. No final one-line claim grants merge.

Write report.md with prompt version, scope, peer references, findings, exact SHAs,
PR, both reviewer artifact paths, checks, accepted comments, merge and cleanup
evidence (as applicable). Update the persistent ledger. Only after a clean audit
or verified merged-and-cleaned fix write complete.json with outcome "clean" or
"merged" respectively. For blocked, unmerged or unfinished work DO NOT write a
completion receipt; record the specific blocker in handoff.md and stop.
`, repo, evidence, cycle, brief, category, settings.Instructions, settings.Test)
}
