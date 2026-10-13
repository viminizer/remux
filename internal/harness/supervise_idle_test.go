package harness

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/viminizer/remux/internal/tmux"
)

func TestIdleWorkerExclusions(t *testing.T) {
	now := time.Now()
	base := tmux.Loop{Name: "loop-builder", Slug: "o/r", Role: "build", State: "idle", Since: now.Add(-3 * time.Minute).Unix()}
	if !idleWorker(base, "o/r", now) {
		t.Fatal("sustained idle worker ignored")
	}
	for _, mutate := range []func(*tmux.Loop){
		func(o *tmux.Loop) { o.Stop = "after" },
		func(o *tmux.Loop) { o.Slug = "other/repo" },
		func(o *tmux.Loop) { o.State = "working" },
		func(o *tmux.Loop) { o.State = "triaging" },
		func(o *tmux.Loop) { o.Role = "supervise" },
		func(o *tmux.Loop) { o.Since = 0 },
		func(o *tmux.Loop) { o.Since = now.Unix() },
		func(o *tmux.Loop) { o.State = "blocked"; o.Note = "usage limit, back at 15:04" },
	} {
		o := base
		mutate(&o)
		if idleWorker(o, "o/r", now) {
			t.Fatalf("unsafe worker eligible: %+v", o)
		}
	}
}

// Stand-in commands exercise the dispatch boundary without touching live tmux,
// GitHub, agents or the user's run history.
func idleHarness(t *testing.T) (*Loop, string, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("REMUX_IDLE_ROOT", root)
	for name, script := range map[string]string{
		"gh":   "#!/bin/sh\ncase \"$1 $2\" in\n'issue list'|'pr list') cat \"$REMUX_IDLE_ROOT/items\" ;;\n'issue view'|'pr view') cat \"$REMUX_IDLE_ROOT/item\" ;;\n*) echo '[]' ;;\nesac\n",
		"tmux": "#!/bin/sh\ncase \"$1\" in\nlist-sessions) cat \"$REMUX_IDLE_ROOT/loops\" ;;\ndisplay-message) case \"$*\" in *loop_assignment*) cat \"$REMUX_IDLE_ROOT/assignment\" ;; esac ;;\nset-option) case \"$*\" in *loop_assignment*) if [ \"$2\" = '-u' ]; then : > \"$REMUX_IDLE_ROOT/assignment\"; else for value do :; done; printf '%s' \"$value\" > \"$REMUX_IDLE_ROOT/assignment\"; fi ;; esac ;;\nesac\n",
	} {
		script = strings.ReplaceAll(script, "$REMUX_IDLE_ROOT", root)
		if err := os.WriteFile(filepath.Join(root, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := ghBin
	ghBin = filepath.Join(root, "gh")
	t.Cleanup(func() { ghBin = old })
	for name, text := range map[string]string{"items": "[]", "item": "{}", "loops": "", "assignment": ""} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	l := &Loop{Role: "supervise", Session: "loop-supervise", Repo: root, gh: GH{Slug: "o/r", Dir: root}, Tmux: &tmux.Client{Bin: filepath.Join(root, "tmux")}}
	return l, root, filepath.Join(root, "assignment")
}

func writeIdleRoster(t *testing.T, root, state, note, stop string) {
	t.Helper()
	fields := []string{"loop-builder", "%1", "build", "claude", root, "o/r", state, strconv.FormatInt(time.Now().Add(-3*time.Minute).Unix(), 10), "0", "", "backend", "", "add", stop, base64.StdEncoding.EncodeToString([]byte(note)), "", "sonnet"}
	if err := os.WriteFile(filepath.Join(root, "loops"), []byte(strings.Join(fields, "|~|")), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDelegationRevalidatesAndConsumesPreference(t *testing.T) {
	l, root, assignment := idleHarness(t)
	writeIdleRoster(t, root, "idle", "", "")
	it := openItem(12, "ready")
	writeItem := func(it Item) {
		t.Helper()
		b, _ := json.Marshal(it)
		if err := os.WriteFile(filepath.Join(root, "item"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeItem(it)
	ctx := context.Background()
	if err := l.delegate(ctx, "DELEGATE loop-builder issue 12\nDELEGATE loop-builder issue 13"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(assignment)
	if string(b) != "issue:12" {
		t.Fatalf("assignment = %q", b)
	}
	worker := *l
	worker.Role = "build"
	worker.Session = "loop-builder"
	got, err := worker.assignedItem(ctx, "issue", "backend")
	if err != nil || got == nil || got.Number != 12 {
		t.Fatalf("preferred task not consumed: %v %v", got, err)
	}
	// Fresh ownership/completion overrides a previously valid assignment.
	for _, labels := range [][]string{{"ready", "wip:backend"}, {"ready", "done:backend"}, {"ready", "blocked:external"}, {"ready", "queue:handoff-held"}, {"ready", "postponed"}} {
		os.WriteFile(assignment, []byte("issue:12"), 0o600)
		writeItem(openItem(12, labels...))
		got, err := worker.assignedItem(ctx, "issue", "backend")
		if err != nil || got != nil {
			t.Fatalf("ineligible assignment accepted: %v %v", labels, err)
		}
		b, _ := os.ReadFile(assignment)
		if len(b) != 0 {
			t.Fatal("stale preference retained")
		}
	}
	writeItem(it)
	for _, state := range []string{"working", "triaging"} {
		writeIdleRoster(t, root, state, "", "")
		if err := l.delegate(ctx, "DELEGATE loop-builder issue 12"); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(assignment)
		if len(b) != 0 {
			t.Fatal("active worker received assignment")
		}
	}
	writeIdleRoster(t, root, "blocked", "usage limit, back at 15:04", "")
	if err := l.delegate(ctx, "DELEGATE loop-builder issue 12"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(assignment)
	if len(b) != 0 {
		t.Fatal("limited worker received assignment")
	}
	if reviewEligible(openItem(12, "needs-review", "blocked")) || reviewEligible(openItem(12, "needs-review", "escalated")) {
		t.Fatal("blocked/escalated PR eligible")
	}
}

func TestHealthyIdleAndCooldownDoNotLaunchAgent(t *testing.T) {
	l, root, _ := idleHarness(t)
	writeIdleRoster(t, root, "idle", "", "")
	// No agent binary configured: either case must return before spawning one.
	did, err := l.superviseIdleOnce(context.Background())
	if err != nil || did {
		t.Fatalf("healthy idle triggered investigation: %v %v", did, err)
	}
	b, _ := json.Marshal([]Item{openItem(12, "blocked")})
	os.WriteFile(filepath.Join(root, "items"), b, 0o600)
	did, err = l.superviseIdleOnce(context.Background())
	if err != nil || did {
		t.Fatalf("cooldown ignored: %v %v", did, err)
	}
}
