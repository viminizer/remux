package harness

import (
	"context"
	"github.com/viminizer/remux/internal/tmux"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestQualityLockSerializesAndReleases(t *testing.T) {
	dir := t.TempDir()
	first, err := qualityLock(dir)
	if err != nil || first == nil {
		t.Fatal(err)
	}
	second, err := qualityLock(dir)
	if err != nil || second != nil {
		t.Fatal("overlapping quality cycle", err)
	}
	first()
	third, err := qualityLock(dir)
	if err != nil || third == nil {
		t.Fatal("lock not released", err)
	}
	third()
}

func TestQualityResumeAndRotation(t *testing.T) {
	dir := t.TempDir()
	n, cycle, err := qualityCycle(dir)
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
	os.WriteFile(filepath.Join(cycle, "handoff.md"), []byte("pending fix worktree"), 0600)
	n2, resumed, err := qualityCycle(dir)
	if err != nil || resumed != cycle || n2 != n {
		t.Fatal("pending cycle abandoned", err)
	}
	os.WriteFile(filepath.Join(cycle, "report.md"), []byte("clean audited flow"), 0600)
	os.WriteFile(filepath.Join(cycle, "complete.json"), []byte(`{"outcome":"clean"}`), 0600)
	_, resumed, err = qualityCycle(dir)
	if err != nil || resumed != cycle {
		t.Fatal("interrupted runner advanced", err)
	}
	os.WriteFile(filepath.Join(cycle, "runner-ok"), nil, 0600)
	next, nextCycle, err := qualityCycle(dir)
	if err != nil || next != 1 || nextCycle == cycle || qualityCategories[next] != "backend read flows" {
		t.Fatal(next, err)
	}
	if b, _ := os.ReadFile(filepath.Join(cycle, "handoff.md")); string(b) != "pending fix worktree" {
		t.Fatal("evidence removed")
	}
}

func TestQualityRejectsIncompleteEvidence(t *testing.T) {
	for _, receipt := range []string{`{"outcome":"merged"}`, `{"outcome":"blocked"}`, `bad json`} {
		dir := t.TempDir()
		_, cycle, _ := qualityCycle(dir)
		os.WriteFile(filepath.Join(cycle, "complete.json"), []byte(receipt), 0600)
		os.WriteFile(filepath.Join(cycle, "runner-ok"), nil, 0600)
		if n, resumed, err := qualityCycle(dir); err != nil || n != 0 || resumed != cycle {
			t.Fatal("invalid receipt did not resume", receipt, err)
		}
	}
}

func TestQualityPromptPreservesWorkflow(t *testing.T) {
	p := qualityPrompt("/repo", "/private/evidence", "/private/evidence/cycle-1", "/private/brief", "UI screens", Settings{Test: "required-check", Instructions: "keep finance"})
	for _, want := range []string{"/private/evidence", "UI screens", "handoff.md BEFORE", "NEW worker", "Codex CLI review", "consistency-audit", "SAME", "source-head checks", "verify GitHub accepted", "freshly fetched origin/main", "DO NOT write", "never permission", "read-only", "required-check", "keep finance"} {
		if !strings.Contains(p, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestQualityRejectsQueueRolesBeforeTmux(t *testing.T) {
	for _, l := range []*Loop{{Scope: "code-quality", Role: "review", Agent: "codex"}, {Scope: "code-quality", Role: "build", Agent: "claude"}, {Scope: "code-quality", Role: "build", Agent: "codex", Task: 1}} {
		if err := l.Run(context.Background()); err == nil {
			t.Fatal("unsupported quality loop ran")
		}
	}
}

// Every executable is a stand-in: this verifies scheduling without a paid agent,
// a network request, a real Git worktree or any write to the tmux server.
func TestQualitySchedulerStandInsResumeWithoutQueue(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("QUALITY_TEST_ROOT", root)
	scripts := map[string]string{
		"git": `#!/bin/sh
printf '%s\n' "$*" >> "$QUALITY_TEST_ROOT/git-calls"
case "$1" in
 rev-parse) printf '%s\n' "$QUALITY_TEST_ROOT/common" ;;
 fetch|checkout) exit 0 ;;
 status) test ! -f "$QUALITY_TEST_ROOT/dirty" || echo ' M source.go' ;;
 worktree) mkdir -p "$QUALITY_TEST_ROOT/common/remux-code-quality/coordinator" ;;
 *) exit 1 ;;
esac
`,
		"tmux": `#!/bin/sh
case "$*" in
 *'#{@loop_instr}'*) test -f "$QUALITY_TEST_ROOT/no-instructions" || printf 'c2F2ZWQgcXVhbGl0eSBicmllZg==' ;;
 *'#{@loop_mode}'*) echo replace ;;
 *'#{@loop_stop}'*) test ! -f "$QUALITY_TEST_ROOT/stop" || echo after ;;
 *'@loop_stop after'*) touch "$QUALITY_TEST_ROOT/stop" ;;
esac
exit 0
`,
		"gh": "#!/bin/sh\necho 'queue must not be touched' >&2\nexit 99\n",
		"bunx": `#!/bin/sh
echo '[{"provider":"codex","account":{"id":"test","is_active":true},"metrics":[{"label":"weekly","remaining_percent":90}]}]'
`,
		"codex": `#!/bin/sh
cat >> "$QUALITY_TEST_ROOT/prompts"
echo run >> "$QUALITY_TEST_ROOT/runs"
printf '%s\n' "$*" >> "$QUALITY_TEST_ROOT/argv"
cycle="$QUALITY_TEST_ROOT/common/remux-code-quality/cycle-000001"
echo pending-fix > "$cycle/handoff.md"
if test ! -f "$QUALITY_TEST_ROOT/resume"; then exit 1; fi
if test -f "$QUALITY_TEST_ROOT/missing"; then exit 0; fi
if test -f "$QUALITY_TEST_ROOT/invalid"; then echo '{"outcome":' > "$cycle/complete.json"; exit 0; fi
if test -f "$QUALITY_TEST_ROOT/empty-report"; then echo '{"outcome":"clean"}' > "$cycle/complete.json"; echo ' ' > "$cycle/report.md"; exit 0; fi
echo 'clean audited flow' > "$cycle/report.md"
echo '{"outcome":"clean"}' > "$cycle/complete.json"
if test -f "$QUALITY_TEST_ROOT/fail-valid"; then exit 1; fi
`,
	}
	for name, body := range scripts {
		body = strings.ReplaceAll(body, "$QUALITY_TEST_ROOT", root)
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	l := &Loop{Repo: root, Role: "build", Agent: "codex", Session: "loop-quality-test", Tmux: tmux.New(), set: Settings{Instructions: "historical conflicting defaults"}}
	did, err := l.qualityOnce(context.Background())
	if !did || err == nil {
		t.Fatal("failed runner should preserve pending cycle", did, err)
	}
	dir := filepath.Join(root, "common/remux-code-quality")
	n, cycle, err := qualityCycle(dir)
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if b, _ := os.ReadFile(filepath.Join(cycle, "handoff.md")); string(b) != "pending-fix\n" {
		t.Fatal("lost handoff")
	}
	os.WriteFile(filepath.Join(root, "resume"), nil, 0600)
	// A stale success marker must never combine with a failed retry's receipt.
	os.WriteFile(filepath.Join(cycle, "runner-ok"), nil, 0600)
	for _, failure := range []string{"missing", "invalid", "empty-report", "fail-valid"} {
		os.WriteFile(filepath.Join(root, failure), nil, 0600)
		if did, err := l.qualityOnce(context.Background()); !did || err == nil {
			t.Fatalf("%s accepted: %v %v", failure, did, err)
		}
		if n, same, err := qualityCycle(dir); err != nil || n != 0 || same != cycle {
			t.Fatalf("restart abandoned category after %s: %d %s %v", failure, n, same, err)
		}
		if _, err := os.Stat(filepath.Join(cycle, "runner-ok")); !os.IsNotExist(err) {
			t.Fatalf("%s left success marker", failure)
		}
		os.Remove(filepath.Join(root, failure))
	}
	if archived, _ := filepath.Glob(filepath.Join(cycle, "previous-complete-*.json")); len(archived) < 2 {
		t.Fatal("interrupted receipts lost")
	}
	did, err = l.qualityOnce(context.Background())
	if !did || err != nil {
		t.Fatal("resume failed", did, err)
	}
	if n, _, err := qualityCycle(dir); err != nil || n != 1 {
		t.Fatal("did not advance completed cycle", n, err)
	}
	os.WriteFile(filepath.Join(root, "stop"), nil, 0600)
	if did, err := l.qualityOnce(context.Background()); did || err != nil {
		t.Fatal("stop ignored", did, err)
	}
	os.Remove(filepath.Join(root, "stop"))
	os.WriteFile(filepath.Join(root, "no-instructions"), nil, 0600)
	if did, err := l.qualityOnce(context.Background()); did || err == nil {
		t.Fatal("missing instructions not closed", did, err)
	}
	os.Remove(filepath.Join(root, "no-instructions"))
	os.WriteFile(filepath.Join(root, "dirty"), nil, 0600)
	if did, err := l.qualityOnce(context.Background()); did || err == nil {
		t.Fatal("dirty coordinator not preserved", did, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "prompts")); strings.Contains(string(b), "historical conflicting defaults") {
		t.Fatal("replace mode inherited conflicting instructions")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "argv")); !strings.Contains(string(b), "--model gpt-6.1-sol") {
		t.Fatal("coordinator model not pinned")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "runs")); strings.Count(string(b), "run\n") != 6 {
		t.Fatalf("unexpected launches %q", b)
	}
	os.Remove(filepath.Join(root, "dirty"))
	os.WriteFile(filepath.Join(root, "bunx"), []byte("#!/bin/sh\necho '[{\"provider\":\"codex\",\"account\":{\"id\":\"test\",\"is_active\":true},\"metrics\":[{\"label\":\"weekly\",\"remaining_percent\":20}]}]'\n"), 0700)
	if did, err := l.qualityOnce(context.Background()); did || err == nil || !strings.Contains(err.Error(), "20% reserve") {
		t.Fatal("quality reserve did not block before launch", did, err)
	}
	if l.get(context.Background(), "@loop_stop") != "after" {
		t.Fatal("budget-blocked quality loop would retry")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "runs")); strings.Count(string(b), "run\n") != 6 {
		t.Fatal("budget-blocked coordinator launched")
	}
}

func TestQualityInheritedLockSurvivesRunnerDeath(t *testing.T) {
	if dir := os.Getenv("REMUX_QUALITY_ORPHAN_HELPER"); dir != "" {
		lock, err := qualityLockFile(dir)
		if err != nil || lock == nil {
			os.Exit(2)
		}
		l := &Loop{}
		l.spawnFiles(context.Background(), dir, "", func(string) []string {
			return []string{"sh", "-c", `echo $$ > coordinator.pid; sleep 30`}
		}, []*os.File{lock})
		os.Exit(0)
	}
	dir := t.TempDir()
	helper := exec.Command(os.Args[0], "-test.run=^TestQualityInheritedLockSurvivesRunnerDeath$")
	helper.Env = append(os.Environ(), "REMUX_QUALITY_ORPHAN_HELPER="+dir)
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	defer helper.Process.Kill()
	deadline := time.Now().Add(5 * time.Second)
	var pid int
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(filepath.Join(dir, "coordinator.pid")); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("stand-in coordinator did not start")
	}
	defer syscall.Kill(-pid, syscall.SIGKILL)
	if err := helper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	helper.Wait()
	if unlock, err := qualityLock(dir); err != nil || unlock != nil {
		if unlock != nil {
			unlock()
		}
		t.Fatal("orphan coordinator lost repository lock", err)
	}
	syscall.Kill(-pid, syscall.SIGKILL)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if unlock, err := qualityLock(dir); err != nil {
			t.Fatal(err)
		} else if unlock != nil {
			unlock()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("coordinator lock did not release after process group ended")
}
