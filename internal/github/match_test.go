package github

import (
	"os"
	"path/filepath"
	"testing"
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
