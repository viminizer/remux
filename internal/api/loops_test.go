package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viminizer/remux/internal/config"
	"github.com/viminizer/remux/internal/harness"
	"github.com/viminizer/remux/internal/tmux"
)

func TestLoopName(t *testing.T) {
	for repo, want := range map[string]string{
		"/Users/mac/code/remux":         "loop-claude-remux",
		"/Users/mac/code/My_Project.v2": "loop-claude-my-project-v2",
	} {
		if got := loopName("claude", repo); got != want || !tmux.ValidLoopName(got) {
			t.Errorf("loopName(%q) = %q, want %q", repo, got, want)
		}
	}
}

func TestSortLoopsProblemsFirst(t *testing.T) {
	loops := []tmux.Loop{{Name: "a", State: "idle"}, {Name: "b", State: "working"}, {Name: "c", State: "blocked"}}
	sortLoops(loops)
	if loops[0].Name != "c" || loops[1].Name != "b" || loops[2].Name != "a" {
		t.Errorf("order = %v", loops)
	}
}

func TestParseQuestion(t *testing.T) {
	c := []struct {
		Body string `json:"body"`
	}{
		{Body: harness.QuestionMarker + "\nOld question\n1. a"},
		{Body: "an ordinary comment"},
		{Body: harness.QuestionMarker + "\nUse Postgres or SQLite for the cache?\n\n1. Postgres, like the rest\n2) SQLite\n3. Skip the cache"},
	}
	q, opts := parseQuestion(c)
	if q != "Use Postgres or SQLite for the cache?" {
		t.Errorf("question = %q", q)
	}
	if len(opts) != 3 || opts[0] != "Postgres, like the rest" || opts[1] != "SQLite" {
		t.Errorf("options = %q", opts)
	}
	if q, _ := parseQuestion(c[1:2]); q != "" {
		t.Errorf("a comment without the marker is not a question: %q", q)
	}
}

func TestLoopPush(t *testing.T) {
	p, ok := loopPush(tmux.Loop{Slug: "o/r", Event: "123 stuck 12 Fix the login"})
	if !ok || p.Title != "An agent is stuck" || p.Route != "#/loops/q/o%2Fr/12" || p.Body != "o/r #12 Fix the login" {
		t.Errorf("stuck push = %+v, %v", p, ok)
	}
	if p, ok := loopPush(tmux.Loop{Slug: "o/r", Event: "124 merged 13 x"}); !ok || p.Title != "PR merged" {
		t.Errorf("merged push = %+v", p)
	}
	if _, ok := loopPush(tmux.Loop{Event: "125 something 1"}); ok {
		t.Error("an unknown event must not notify")
	}
}

// Live: start a loop through the API, see it listed, edit it, stop it. The
// session runs a stand-in for the binary, so no agent and no GitHub are
// involved - this proves the wiring, and that it only ever touches loop-*.
func TestLoopLifecycle(t *testing.T) {
	tm := tmux.New()
	ctx := context.Background()
	if _, err := tm.Version(ctx); err != nil {
		t.Skipf("no tmux: %v", err)
	}
	t.Setenv("HOME", t.TempDir())

	repo := filepath.Join(t.TempDir(), "harness-api-test")
	os.MkdirAll(repo, 0o755)
	fake := filepath.Join(t.TempDir(), "fake-remux")
	os.WriteFile(fake, []byte("#!/bin/sh\nsleep 60\n"), 0o755)
	loopExe = func() (string, error) { return fake, nil }
	defer func() { loopExe = os.Executable }()

	srv := NewServer(config.Default(), tm, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	name := "loop-claude-harness-api-test"
	defer tm.KillLoop(ctx, name)
	defer tm.KillLoop(ctx, "loop-review-harness-api-test")

	code, body := do(t, ts, "POST", "/api/loops", map[string]any{
		"repo": repo, "agents": []string{"claude"}, "review": true, "scope": "backend",
	})
	if code != http.StatusOK || !strings.Contains(string(body), name) {
		t.Fatalf("start: %d %s", code, body)
	}
	if code, body := do(t, ts, "POST", "/api/loops", map[string]any{"repo": repo, "agents": []string{"claude"}, "review": true}); code != http.StatusOK ||
		!strings.Contains(string(body), `"started":[]`) || !strings.Contains(string(body), `"updated":["`+name) {
		t.Errorf("a running loop must be updated, not started twice: %d %s", code, body)
	}

	if code, _ := do(t, ts, "PATCH", "/api/loops/"+name, map[string]any{"instructions": "only tests\nplease"}); code != http.StatusOK {
		t.Fatalf("edit: %d", code)
	}
	if v, _ := tm.LoopOption(ctx, name, "@loop_instr"); v != "only tests\nplease" {
		t.Errorf("instructions = %q", v)
	}
	if code, _ := do(t, ts, "PATCH", "/api/loops/"+name, map[string]any{"scope": "Not A Scope"}); code != http.StatusBadRequest {
		t.Errorf("a bad scope must be refused, got %d", code)
	}

	code, body = do(t, ts, "GET", "/api/loops", nil)
	var got struct {
		Loops []tmux.Loop      `json:"loops"`
		Last  config.LoopStart `json:"last"`
	}
	json.Unmarshal(body, &got)
	if code != http.StatusOK || len(got.Loops) < 2 || got.Last.Repo != repo {
		t.Fatalf("list: %d %s", code, body)
	}

	// Claude twice means a second Claude loop, named so the two differ.
	defer tm.KillLoop(ctx, "loop-claude2-harness-api-test")
	if code, body := do(t, ts, "POST", "/api/loops", map[string]any{"repo": repo, "agents": []string{"claude", "claude"}, "reviews": 1}); code != http.StatusOK ||
		!strings.Contains(string(body), `"started":["loop-claude2-harness-api-test"]`) {
		t.Errorf("a second Claude loop was not started: %d %s", code, body)
	}
	if code, _ := do(t, ts, "POST", "/api/loops", map[string]any{"repo": repo, "agents": []string{"claude", "claude", "claude", "claude", "claude", "claude"}}); code != http.StatusBadRequest {
		t.Errorf("six Claude loops must be refused, got %d", code)
	}

	// Two reviews means a second review loop, beside the one already running.
	defer tm.KillLoop(ctx, "loop-review2-harness-api-test")
	if code, body := do(t, ts, "POST", "/api/loops", map[string]any{"repo": repo, "agents": []string{"claude", "claude"}, "reviews": 2}); code != http.StatusOK ||
		!strings.Contains(string(body), `"started":["loop-review2-harness-api-test"]`) {
		t.Errorf("a second review loop was not started: %d %s", code, body)
	}

	// One review fewer and one Claude more: the extra review loop stops.
	defer tm.KillLoop(ctx, "loop-claude3-harness-api-test")
	if code, body := do(t, ts, "POST", "/api/loops", map[string]any{"repo": repo, "agents": []string{"claude", "claude", "claude"}, "reviews": 1}); code != http.StatusOK ||
		!strings.Contains(string(body), `"started":["loop-claude3-harness-api-test"]`) ||
		!strings.Contains(string(body), `"stopped":["loop-review2-harness-api-test"]`) {
		t.Errorf("lowering the review count did not stop a review loop: %d %s", code, body)
	}
	if l, _ := srv.loopByName(ctx, "loop-review2-harness-api-test"); l != nil {
		t.Error("loop-review2 still runs")
	}

	// A gap in the numbering: with loop-claude stopped by hand, two Claude
	// loops means loop-claude and loop-claude2, not three.
	if code, _ := do(t, ts, "DELETE", "/api/loops/"+name, nil); code != http.StatusOK {
		t.Fatalf("stop: %d", code)
	}
	if code, body := do(t, ts, "POST", "/api/loops", map[string]any{"repo": repo, "agents": []string{"claude", "claude"}, "reviews": 1}); code != http.StatusOK ||
		!strings.Contains(string(body), `"started":["`+name+`"]`) ||
		!strings.Contains(string(body), `"stopped":["loop-claude3-harness-api-test"]`) {
		t.Errorf("a gap left the wrong loops running: %d %s", code, body)
	}

	// A queued "after this issue" is cancelled when the loop is still wanted.
	tm.SetLoopOption(ctx, "loop-claude2-harness-api-test", "@loop_stop", "after")
	do(t, ts, "POST", "/api/loops", map[string]any{"repo": repo, "agents": []string{"claude", "claude"}, "reviews": 1})
	if v, _ := tm.LoopOption(ctx, "loop-claude2-harness-api-test", "@loop_stop"); v != "" {
		t.Errorf("queued stop = %q, want it cancelled", v)
	}

	// Another repo with the same folder name would share these sessions.
	twin := filepath.Join(t.TempDir(), "harness-api-test")
	os.MkdirAll(twin, 0o755)
	if code, body := do(t, ts, "POST", "/api/loops", map[string]any{"repo": twin, "agents": []string{"claude"}}); code != http.StatusConflict {
		t.Errorf("a repo with the same folder name must be refused: %d %s", code, body)
	}

	// Not working, so "after this issue" has nothing to wait for.
	if code, body := do(t, ts, "DELETE", "/api/loops/"+name+"?when=after", nil); code != http.StatusOK || !strings.Contains(string(body), `"now"`) {
		t.Fatalf("stop: %d %s", code, body)
	}
	if l, _ := srv.loopByName(ctx, name); l != nil {
		t.Error("loop still running after stop")
	}
	if code, _ := do(t, ts, "DELETE", "/api/loops/main", nil); code != http.StatusNotFound {
		t.Errorf("stopping a non-loop session must be refused, got %d", code)
	}
}

func TestCodexChat(t *testing.T) {
	got := strings.Join(codexChat("/Users/mac", "brief with ''' inside"), " ")
	for _, want := range []string{
		"check_for_update_on_startup=false",
		`projects={"/Users/mac"={trust_level="trusted"}}`,
		"developer_instructions='''brief with ' ' ' inside'''",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("codex chat command %q is missing %q", got, want)
		}
	}
}

func TestClaimLabel(t *testing.T) {
	for _, tc := range []struct {
		l    tmux.Loop
		want string
	}{
		{tmux.Loop{Role: "review"}, "wip:review"},
		{tmux.Loop{Role: "supervise", Scope: "full"}, "wip:supervise"},
		{tmux.Loop{Role: "build", Scope: "backend"}, "wip:backend"},
		{tmux.Loop{Role: "build"}, "wip:full"},
	} {
		if got := claimLabel(tc.l); got != tc.want {
			t.Errorf("claimLabel(%+v) = %q, want %q", tc.l, got, tc.want)
		}
	}
}
