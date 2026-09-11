package github

import (
	"errors"
	"io/fs"
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
	// Probe is how long one directory gets to answer.
	//
	// This exists because of macOS privacy control. ~/Desktop, ~/Documents
	// and ~/Downloads are protected, and a LaunchAgent that has not been
	// granted access does not get a quick refusal when it opens a file
	// there - the open blocks, seemingly forever, while the system waits on
	// a consent decision that a background service can never produce. Kevin
	// keeps every repo under ~/Desktop, so that is the normal case here and
	// not a corner. A read that cannot be cancelled must at least not be
	// waited on.
	Probe time.Duration

	mu    sync.Mutex
	cache map[string]entry
}

type entry struct {
	repo string // owner/name, empty for "not a GitHub checkout"
	at   time.Time
	// blocked records that this directory's probe was refused or never came
	// back, so the UI can say "grant Full Disk Access" instead of quietly
	// showing no pane chips at all.
	//
	// Per directory rather than one flag for the matcher, because a single
	// flag only ever latched: one slow probe pinned the banner for the life
	// of the process, telling Kevin to grant a permission he had granted.
	// Held here, it is re-decided whenever the entry expires.
	blocked bool
}

func NewMatcher() *Matcher {
	return &Matcher{TTL: 5 * time.Minute, Probe: 2 * time.Second, cache: map[string]entry{}}
}

func (m *Matcher) ttl() time.Duration {
	if m.TTL == 0 {
		return 5 * time.Minute
	}
	return m.TTL
}

func (m *Matcher) probe() time.Duration {
	if m.Probe <= 0 {
		return 2 * time.Second
	}
	return m.Probe
}

// Blocked reports whether the filesystem refused a probe or never answered
// one. On macOS that means this process has not been granted access to the
// folders the repos live in.
//
// Only entries still inside the TTL count. An expired one says nothing about
// now - and a pane that has since been closed is never probed again, so
// counting it would leave the banner up for good.
func (m *Matcher) Blocked() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.cache {
		if e.blocked && time.Since(e.at) < m.ttl() {
			return true
		}
	}
	return false
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

	repo, ok := resolveWithin(dir, m.probe())

	m.mu.Lock()
	if m.cache == nil {
		m.cache = map[string]entry{}
	}
	m.cache[dir] = entry{repo: repo, at: time.Now(), blocked: !ok}
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

// resolveWithin runs resolve on its own goroutine and gives up after d.
//
// A blocked syscall cannot be cancelled, so the goroutine is left behind. That
// is deliberate and bounded: the caller records the failure in the cache, so
// each directory is abandoned at most once per TTL rather than once per
// request. The alternative - waiting - is what left every GitHub request
// hanging with no response at all.
//
// ok is false when the probe did not finish, or finished by being refused.
func resolveWithin(dir string, d time.Duration) (string, bool) {
	type result struct {
		repo   string
		denied bool
	}
	ch := make(chan result, 1) // buffered, so the goroutine can always finish
	go func() {
		repo, denied := resolve(dir)
		ch <- result{repo, denied}
	}()

	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case r := <-ch:
		return r.repo, !r.denied
	case <-t.C:
		return "", false
	}
}

// resolve does the uncached work: find .git, find its config, read origin.
//
// denied separates "this is not a repo" from "this machine would not let me
// look", which are the same empty answer to a caller but very different
// things to tell a person.
func resolve(dir string) (repo string, denied bool) {
	gitDir := findGitDir(dir)
	if gitDir == "" {
		return "", false
	}
	b, err := os.ReadFile(filepath.Join(gitDir, "config"))
	if err != nil {
		return "", errors.Is(err, fs.ErrPermission)
	}
	return repoFromConfig(string(b)), false
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
