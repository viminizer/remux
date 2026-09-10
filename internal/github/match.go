package github

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Matcher answers "which GitHub repo is this directory a checkout of".
//
// It is what ties the two halves of the app together. tmux already reports
// pane_current_path for every pane, so a pane can be labelled with its repo
// and a repo can list the panes already open inside it - without Kevin
// configuring the mapping anywhere.
//
// It reads .git directly instead of shelling out to git. The tree walk plus
// one config file is a handful of syscalls; running `git remote get-url` once
// per pane per poll, against ~26 panes, is 26 processes every tick for an
// answer that almost never changes.
type Matcher struct {
	// TTL is how long a resolved path is trusted. Repos do not move often,
	// but a pane that cd's out of one, or a remote that is renamed, has to
	// be picked up eventually.
	TTL time.Duration

	mu    sync.Mutex
	cache map[string]entry
}

type entry struct {
	repo string // owner/name, empty for "not a GitHub checkout"
	at   time.Time
}

func NewMatcher() *Matcher {
	return &Matcher{TTL: 5 * time.Minute, cache: map[string]entry{}}
}

func (m *Matcher) ttl() time.Duration {
	if m.TTL == 0 {
		return 5 * time.Minute
	}
	return m.TTL
}

// Repo returns "owner/name" for the checkout containing dir, or "" if dir is
// not inside a git repository with a GitHub origin.
func (m *Matcher) Repo(dir string) string {
	if dir == "" {
		return ""
	}
	m.mu.Lock()
	if e, ok := m.cache[dir]; ok && time.Since(e.at) < m.ttl() {
		m.mu.Unlock()
		return e.repo
	}
	m.mu.Unlock()

	repo := resolve(dir)

	m.mu.Lock()
	if m.cache == nil {
		m.cache = map[string]entry{}
	}
	m.cache[dir] = entry{repo: repo, at: time.Now()}
	m.mu.Unlock()
	return repo
}

// Panes groups pane ids by repo, given each pane's current path. It is the
// shape the Repos screen wants: repo -> the panes already working on it.
func (m *Matcher) Panes(paths map[string]string) map[string][]string {
	out := map[string][]string{}
	for pane, dir := range paths {
		if repo := m.Repo(dir); repo != "" {
			out[repo] = append(out[repo], pane)
		}
	}
	return out
}

// resolve does the uncached work: find .git, find its config, read origin.
func resolve(dir string) string {
	gitDir := findGitDir(dir)
	if gitDir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(gitDir, "config"))
	if err != nil {
		return ""
	}
	return repoFromConfig(string(b))
}

// findGitDir walks up from dir to the first .git, and returns the directory
// holding the config file.
//
// A linked worktree has .git as a *file* containing "gitdir: <path>", and
// that path is <main>/.git/worktrees/<name> - which has no config of its own.
// The remote lives in the main checkout, so the worktrees suffix is trimmed
// back off. Kevin runs agents in worktrees, so this is the normal case here,
// not an edge one.
func findGitDir(dir string) string {
	for {
		p := filepath.Join(dir, ".git")
		fi, err := os.Stat(p)
		switch {
		case err == nil && fi.IsDir():
			return p
		case err == nil:
			return worktreeGitDir(p, dir)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func worktreeGitDir(file, base string) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	target := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
	if target == "" {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}
	if i := strings.Index(target, string(filepath.Separator)+"worktrees"+string(filepath.Separator)); i >= 0 {
		target = target[:i]
	}
	return filepath.Clean(target)
}

// repoFromConfig pulls the origin URL out of a git config.
//
// This is not a general INI parser and does not need to be: it looks for the
// origin section and takes the first url key inside it. Anything else in the
// file is skipped.
func repoFromConfig(cfg string) string {
	inOrigin := false
	for _, line := range strings.Split(cfg, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inOrigin = strings.HasPrefix(line, `[remote "origin"]`)
			continue
		}
		if !inOrigin {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "url" {
			continue
		}
		return repoFromURL(strings.TrimSpace(val))
	}
	return ""
}

// repoFromURL turns any of git's GitHub remote spellings into owner/name.
//
//	git@github.com:viminizer/remux.git
//	https://github.com/viminizer/remux.git
//	ssh://git@github.com/viminizer/remux
//
// A non-GitHub remote returns empty rather than a guess: there is nothing
// useful this screen could show for one.
func repoFromURL(u string) string {
	host := "github.com"
	i := strings.Index(strings.ToLower(u), host)
	if i < 0 {
		return ""
	}
	rest := u[i+len(host):]
	// What follows the host is ":" for scp-style and "/" for a URL. Both
	// are stripped the same way.
	rest = strings.TrimLeft(rest, ":/")
	rest = strings.TrimSuffix(strings.TrimSuffix(rest, "/"), ".git")
	rest = strings.TrimSuffix(rest, ".git")

	owner, name, ok := strings.Cut(rest, "/")
	if !ok {
		return ""
	}
	// A remote URL can carry a fragment or query after the repo; anything
	// past the second path segment is not part of the name.
	if j := strings.IndexAny(name, "/?#"); j >= 0 {
		name = name[:j]
	}
	full := owner + "/" + name
	if !ValidRepo(full) {
		return ""
	}
	return full
}
