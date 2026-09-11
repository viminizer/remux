package github

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRepoFromURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"git@github.com:viminizer/remux.git", "viminizer/remux"},
		{"https://github.com/viminizer/remux.git", "viminizer/remux"},
		{"https://github.com/viminizer/remux", "viminizer/remux"},
		{"https://github.com/viminizer/remux/", "viminizer/remux"},
		{"ssh://git@github.com/apache/shardingsphere.git", "apache/shardingsphere"},
		{"git://github.com/apache/shardingsphere.git", "apache/shardingsphere"},
		{"https://viminizer@github.com/viminizer/remux.git", "viminizer/remux"},
		{"GIT@GITHUB.COM:viminizer/remux.git", "viminizer/remux"},

		// Not GitHub, or not a repo: better to show nothing than a guess.
		{"git@gitlab.com:viminizer/remux.git", ""},
		{"https://github.com/viminizer", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := repoFromURL(c.in); got != c.want {
			t.Errorf("repoFromURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRepoFromConfig(t *testing.T) {
	cfg := `[core]
	repositoryformatversion = 0
	bare = false
[remote "upstream"]
	url = git@github.com:apache/shardingsphere.git
	fetch = +refs/heads/*:refs/remotes/upstream/*
[remote "origin"]
	url = git@github.com:viminizer/remux.git
	fetch = +refs/heads/*:refs/remotes/origin/*
[branch "main"]
	remote = origin
`
	// origin wins even though upstream is listed first.
	if got := repoFromConfig(cfg); got != "viminizer/remux" {
		t.Errorf("repoFromConfig = %q, want viminizer/remux", got)
	}
	if got := repoFromConfig("[core]\n\tbare = false\n"); got != "" {
		t.Errorf("config with no origin = %q, want empty", got)
	}
}

// writeRepo lays down a checkout with the given origin and returns its root.
func writeRepo(t *testing.T, root, origin string) string {
	t.Helper()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[remote \"origin\"]\n\turl = " + origin + "\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestMatcherRepo(t *testing.T) {
	tmp := t.TempDir()
	root := writeRepo(t, filepath.Join(tmp, "remux"), "git@github.com:viminizer/remux.git")
	deep := filepath.Join(root, "internal", "github")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(tmp, "not-a-repo")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewMatcher()
	if got := m.Repo(root); got != "viminizer/remux" {
		t.Errorf("root = %q", got)
	}
	// A pane sitting deep inside the tree is the normal case.
	if got := m.Repo(deep); got != "viminizer/remux" {
		t.Errorf("subdirectory = %q", got)
	}
	if got := m.Repo(outside); got != "" {
		t.Errorf("non-repo = %q, want empty", got)
	}
	if got := m.Repo(""); got != "" {
		t.Errorf("empty path = %q, want empty", got)
	}
}

// Kevin runs agents in linked worktrees, where .git is a file pointing at
// <main>/.git/worktrees/<name> - a directory with no config of its own.
func TestMatcherWorktree(t *testing.T) {
	tmp := t.TempDir()
	main := writeRepo(t, filepath.Join(tmp, "remux"), "https://github.com/viminizer/remux.git")

	wtMeta := filepath.Join(main, ".git", "worktrees", "feature")
	if err := os.MkdirAll(wtMeta, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(tmp, "remux-feature")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+wtMeta+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewMatcher()
	if got := m.Repo(wt); got != "viminizer/remux" {
		t.Errorf("worktree = %q, want viminizer/remux", got)
	}
}

func TestMatcherPanes(t *testing.T) {
	tmp := t.TempDir()
	a := writeRepo(t, filepath.Join(tmp, "remux"), "git@github.com:viminizer/remux.git")
	b := writeRepo(t, filepath.Join(tmp, "shortlist"), "git@github.com:viminizer/shortlist.git")
	none := filepath.Join(tmp, "home")
	if err := os.MkdirAll(none, 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewMatcher()
	got := m.Panes(map[string]string{
		"%1": a,
		"%2": a,
		"%3": b,
		"%4": none,
	})
	if len(got) != 2 {
		t.Fatalf("got %d repos, want 2: %v", len(got), got)
	}
	if len(got["viminizer/remux"]) != 2 {
		t.Errorf("remux panes = %v, want 2", got["viminizer/remux"])
	}
	if len(got["viminizer/shortlist"]) != 1 {
		t.Errorf("shortlist panes = %v, want 1", got["viminizer/shortlist"])
	}
}

// A pane's directory is read on every tree poll, ~26 of them every few
// seconds. The cache is what keeps that off the filesystem.
func TestMatcherCaches(t *testing.T) {
	tmp := t.TempDir()
	root := writeRepo(t, filepath.Join(tmp, "remux"), "git@github.com:viminizer/remux.git")

	m := NewMatcher()
	if got := m.Repo(root); got != "viminizer/remux" {
		t.Fatalf("first read = %q", got)
	}
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	if got := m.Repo(root); got != "viminizer/remux" {
		t.Errorf("second read = %q, want the cached answer", got)
	}

	// And an expired entry is re-read rather than trusted forever.
	m.TTL = -1
	if got := m.Repo(root); got != "" {
		t.Errorf("after TTL = %q, want a fresh empty answer", got)
	}
}

// A directory that never answers must not take the caller with it.
//
// This is not hypothetical: reading .git/config under ~/Desktop from a
// LaunchAgent that macOS has not granted access to blocks rather than failing,
// and having that on the request path left every GitHub request hanging with
// no response at all.
func TestResolveWithinGivesUp(t *testing.T) {
	start := time.Now()
	repo, ok := resolveWithin(blockingDir(t), 80*time.Millisecond)
	if ok {
		t.Error("a directory that never answers reported success")
	}
	if repo != "" {
		t.Errorf("got %q, want empty", repo)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("waited %v - it should have given up after 80ms", d)
	}
}

// blockingDir is a FIFO standing in for a file whose open never returns.
// Opening one for reading blocks until a writer arrives, which is the same
// shape as the privacy-control stall.
func blockingDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(gitDir, "config")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot make a fifo here: %v", err)
	}
	return root
}

// The matcher records that it was refused, so the UI can say why the pane
// chips are missing rather than silently dropping them.
func TestMatcherRecordsBlocked(t *testing.T) {
	m := NewMatcher()
	m.Probe = 60 * time.Millisecond
	if m.Blocked() {
		t.Fatal("blocked before anything was probed")
	}
	if got := m.Repo(blockingDir(t)); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if !m.Blocked() {
		t.Error("a probe that never answered was not recorded")
	}
}

// A plain missing repo is not a refusal - reporting it as one would put a
// "grant Full Disk Access" banner on a perfectly healthy screen.
func TestMatcherNotBlockedForOrdinaryMiss(t *testing.T) {
	m := NewMatcher()
	if got := m.Repo(t.TempDir()); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if m.Blocked() {
		t.Error("a directory that is simply not a repo was reported as refused")
	}
}

// A refusal is recorded against the directory, not against the matcher, so it
// expires with the cache entry.
//
// It used to latch: one slow probe - a cold cache, a busy disk, a directory
// that answered late - put "grant Full Disk Access" on the GitHub screen for
// the life of the process, telling Kevin to grant a permission he had already
// granted. Only a restart cleared it.
func TestMatcherBlockedExpires(t *testing.T) {
	m := NewMatcher()
	m.Probe = 60 * time.Millisecond
	m.TTL = 150 * time.Millisecond

	if got := m.Repo(blockingDir(t)); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if !m.Blocked() {
		t.Fatal("a probe that never answered was not recorded")
	}

	time.Sleep(m.TTL + 50*time.Millisecond)
	if m.Blocked() {
		t.Error("the refusal outlived its cache entry")
	}
}

// One unreadable directory must not hide a healthy one, and a healthy one must
// not hide a refusal: Blocked is about whether anything is still refused.
func TestMatcherBlockedIsPerDirectory(t *testing.T) {
	m := NewMatcher()
	m.Probe = 60 * time.Millisecond

	m.Repo(t.TempDir())
	if m.Blocked() {
		t.Fatal("an ordinary directory was reported as refused")
	}
	m.Repo(blockingDir(t))
	if !m.Blocked() {
		t.Error("a later refusal was lost")
	}
	m.Repo(t.TempDir())
	if !m.Blocked() {
		t.Error("a good probe cleared a refusal that is still live")
	}
}
