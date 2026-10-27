package api

import (
	"encoding/json"
	"github.com/viminizer/remux/internal/config"
	"github.com/viminizer/remux/internal/tmux"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProjectPathAliasesPreserveClones(t *testing.T) {
	for path, want := range map[string]string{
		"/one/repo/.claude/worktrees/task":       "/one/repo",
		"/Users/user/.codex/worktrees/task/repo": "/Users/user/.codex/worktrees/task/repo",
		"/one/repo/.worktrees/task":              "/one/repo",
		"/other/repo":                            "/other/repo",
		"/one/real-worktrees/clone":              "/one/real-worktrees/clone",
	} {
		if got := projectPath(path); got != want {
			t.Errorf("%s: got %s want %s", path, got, want)
		}
	}
}

func TestLoopProjectResolutionAndAvailability(t *testing.T) {
	repo := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(".remux/harness.json", `{"mode":"personal","instructions":"project defaults"}`)
	write(".remux/instructions/build.md", "generic builder")
	write(".remux/instructions/frontend-build.md", "frontend builder")
	write(".remux/instructions/review.md", "shared review")
	got, err := readLoopProject(repo, "frontend")
	if err != nil {
		t.Fatal(err)
	}
	if got.Quality || got.Defaults != "project defaults" || got.Roles["build"].Text != "frontend builder" || got.Roles["review"].Source != ".remux/instructions/review.md" {
		t.Fatalf("wrong resolution: %+v", got)
	}
	quality, err := readLoopProject(repo, "code-quality")
	if err != nil {
		t.Fatal(err)
	}
	if quality.Quality || quality.Roles["build"].Text != "" {
		t.Fatalf("generic builder is not a quality workflow: %+v", quality)
	}
	write(".remux/instructions/code-quality-build.md", "Read the short committed workflow pointer.")
	quality, err = readLoopProject(repo, "code-quality")
	if err != nil || !quality.Quality || quality.Roles["build"].Source != ".remux/instructions/code-quality-build.md" {
		t.Fatalf("quality resolution: %+v %v", quality, err)
	}
}

func TestLoopProjectErrorsAreNotMissingWorkflow(t *testing.T) {
	repo := t.TempDir()
	if _, err := readLoopProject(filepath.Join(repo, "missing"), "full"); err == nil {
		t.Fatal("nonexistent repo must be an error")
	}
	if err := os.MkdirAll(filepath.Join(repo, ".remux/instructions/frontend-build.md"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := readLoopProject(repo, "frontend"); err == nil {
		t.Fatal("unreadable role path must not be silently absent")
	}
}

func TestLoopProjectEndpointValidationAndEmptyProject(t *testing.T) {
	srv := &Server{}
	for _, query := range []string{"repo=relative&scope=full", "repo=/tmp&scope=../escape"} {
		w := httptest.NewRecorder()
		srv.handleLoopProject(w, httptest.NewRequest("GET", "/api/loops/project?"+query, nil))
		if w.Code != 400 {
			t.Fatalf("invalid query: %d", w.Code)
		}
	}
	w := httptest.NewRecorder()
	srv.handleLoopProject(w, httptest.NewRequest("GET", "/api/loops/project?repo="+url.QueryEscape(t.TempDir())+"&scope=full", nil))
	if w.Code != 200 {
		t.Fatalf("empty accessible project: %d %s", w.Code, w.Body)
	}
}

func TestLoopProjectReaderLimitIsBounded(t *testing.T) {
	// No filesystem work begins once both potentially stalled reader slots are full.
	projectReaders <- struct{}{}
	projectReaders <- struct{}{}
	defer func() { <-projectReaders; <-projectReaders }()
	w := httptest.NewRecorder()
	(&Server{}).handleLoopProject(w, httptest.NewRequest("GET", "/api/loops/project?repo=/tmp&scope=full", nil))
	if w.Code != 503 {
		t.Fatalf("reader limit: %d %s", w.Code, w.Body)
	}
}

func TestLoopProjectChoicesOmitOnlyMissingPaths(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "clone-one")
	second := filepath.Join(root, "clone-two")
	for _, path := range []string{first, second} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.HarnessRepos = []config.HarnessRepo{
		{Path: first, Slug: "owner/repo"},
		{Path: first + "/.claude/worktrees/task", Slug: "owner/repo"},
		{Path: second, Slug: "owner/repo"},
		{Path: filepath.Join(root, "missing"), Slug: "owner/repo"},
	}
	srv := NewServer(cfg, &tmux.Client{Bin: "remux-no-such-tmux"}, nil)
	srv.ws.tree, srv.ws.at = &tmux.Tree{}, time.Now()
	w := httptest.NewRecorder()
	srv.handleLoopProjects(w, httptest.NewRequest("GET", "/api/loops/projects", nil))
	if w.Code != 200 {
		t.Fatalf("choices: %d %s", w.Code, w.Body)
	}
	var repos []config.HarnessRepo
	if err := json.Unmarshal(w.Body.Bytes(), &repos); err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 || repos[0].Path != first || repos[1].Path != second {
		t.Fatalf("clones lost or aliases retained: %+v", repos)
	}
	if len(cfg.HarnessRepos) != 4 {
		t.Fatal("choice validation must not delete config history")
	}
}
