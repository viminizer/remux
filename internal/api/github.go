package api

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/viminizer/remux/internal/config"
	gh "github.com/viminizer/remux/internal/github"
)

// The GitHub screen is served from one shared poller plus a few on-demand
// reads. What the poller holds - repo counts and the inbox - is the same for
// everybody, so it is polled once and every request reads the snapshot. What
// depends on which screen is open - a repo's issue list, a PR's checks - is
// fetched when it is asked for, because polling 186 issues nobody is looking
// at would spend the API budget on nothing.

// ghStatus is the HTTP code for one of the package's typed errors.
//
// The important one is 401: the phone cannot fix it, only Kevin at the laptop
// can, and that is a different screen from a generic failure.
func ghStatus(err error) int {
	switch gh.ErrorKind(err) {
	case "auth", "nogh":
		return http.StatusUnauthorized
	case "notfound":
		return http.StatusNotFound
	case "ratelimit":
		return http.StatusTooManyRequests
	case "offline", "timeout":
		return http.StatusServiceUnavailable
	}
	return http.StatusBadGateway
}

func writeGHErr(w http.ResponseWriter, r *http.Request, err error) {
	// Logged as well as returned. Without this a failed screen leaves no
	// trace anywhere on the Mac, so the only evidence of what went wrong is
	// whatever the person holding the phone can describe.
	log.Printf("github %s: %v", r.URL.Path, err)
	writeJSON(w, ghStatus(err), map[string]string{
		"error": err.Error(),
		"kind":  gh.ErrorKind(err),
	})
}

// repoParam rebuilds "owner/name" from the two path segments and validates it
// before it can reach a URL or a GraphQL document.
func repoParam(r *http.Request) (string, bool) {
	full := r.PathValue("owner") + "/" + r.PathValue("name")
	return full, gh.ValidRepo(full)
}

func numberParam(r *http.Request) (int, bool) {
	n, err := strconv.Atoi(r.PathValue("number"))
	return n, err == nil && n > 0
}

// ── the snapshot ──────────────────────────────────────────────────────────

// handleGitHub returns everything the GitHub screen opens with.
//
// It never waits on the network: the poller already has an answer, and an
// empty one on the very first request is the same "loading" state a failed
// poll produces.
func (s *Server) handleGitHub(w http.ResponseWriter, r *http.Request) {
	if s.GH == nil {
		writeErr(w, http.StatusServiceUnavailable, "github is not configured")
		return
	}
	writeJSON(w, http.StatusOK, s.githubSnapshot(r))
}

func (s *Server) handleGitHubRefresh(w http.ResponseWriter, r *http.Request) {
	if s.GH == nil {
		writeErr(w, http.StatusServiceUnavailable, "github is not configured")
		return
	}
	s.GH.Kick()
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

// ghBase is the poller's snapshot with the muted rows lifted out of it.
//
// The badge on the WebSocket and the GitHub screen both come through here, so
// muting a row changes the number in the drawer at the same moment it clears
// the list - which is the whole point of muting it.
func (s *Server) ghBase() gh.Snapshot {
	return s.ApplyMutes(s.GH.Snapshot())
}

// ApplyMutes lifts the muted rows out of a snapshot handed in from outside.
//
// Exported for the push watcher, which hangs off the poller and so receives
// the raw snapshot rather than asking for one. Without this it notified about
// rows that had been muted - the mute cleared the list and the phone buzzed
// anyway, which is the opposite of what muting is for.
func (s *Server) ApplyMutes(snap gh.Snapshot) gh.Snapshot {
	return applyMutes(snap, s.mutes())
}

// applyMutes moves dismissed rows out of the three inbox lists and into
// Muted, where the screen can show a count and put one back.
//
// A mute is recorded against the item's updatedAt, so it lapses the moment
// the item actually moves. That is what keeps this from being a way to lose
// something: a new commit, a new comment, a review, and the row returns.
func applyMutes(snap gh.Snapshot, muted map[string]time.Time) gh.Snapshot {
	if len(muted) == 0 {
		return snap
	}
	// Keys are folded on the way in, not trusted as stored. A repo name's
	// case is not significant to GitHub, and this file is hand-editable.
	byKey := make(map[string]time.Time, len(muted))
	for k, v := range muted {
		byKey[strings.ToLower(k)] = v
	}

	var out []gh.InboxItem
	keep := func(items []gh.InboxItem) []gh.InboxItem {
		kept := make([]gh.InboxItem, 0, len(items))
		for _, it := range items {
			if at, ok := byKey[muteKey(it.Repo, it.Number)]; ok && !it.Updated.After(at) {
				out = append(out, it)
				continue
			}
			kept = append(kept, it)
		}
		return kept
	}
	snap.Inbox.NeedsYou = keep(snap.Inbox.NeedsYou)
	snap.Inbox.Assigned = keep(snap.Inbox.Assigned)
	snap.Inbox.YourPRs = keep(snap.Inbox.YourPRs)
	snap.Muted = out
	return snap
}

func muteKey(repo string, number int) string {
	return strings.ToLower(repo) + "#" + strconv.Itoa(number)
}

// githubSnapshot is ghBase with the tmux link filled in.
//
// The matching happens here rather than in the poller because it depends on
// where the panes are right now, which changes far faster than GitHub does.
func (s *Server) githubSnapshot(r *http.Request) gh.Snapshot {
	snap := s.ghBase()
	byRepo := s.panesByRepo(r)
	snap.PanesBlocked = s.Match != nil && s.Match.Blocked()
	if len(byRepo) == 0 {
		return snap
	}
	snap.Panes = byRepo

	repos := make([]gh.Repo, len(snap.Repos))
	copy(repos, snap.Repos)
	for i := range repos {
		repos[i].Panes = byRepo[repos[i].Full]
	}
	snap.Repos = repos

	snap.Inbox.NeedsYou = withPanes(snap.Inbox.NeedsYou, byRepo)
	snap.Inbox.Assigned = withPanes(snap.Inbox.Assigned, byRepo)
	snap.Inbox.YourPRs = withPanes(snap.Inbox.YourPRs, byRepo)
	snap.Muted = withPanes(snap.Muted, byRepo)
	return snap
}

func withPanes(items []gh.InboxItem, byRepo map[string][]string) []gh.InboxItem {
	out := make([]gh.InboxItem, len(items))
	copy(out, items)
	for i := range out {
		out[i].Panes = byRepo[out[i].Repo]
	}
	return out
}

// panesByRepo returns the last mapping the background refresher computed.
//
// It is a map read and nothing else. It used to walk the filesystem inline,
// and that is what made every GitHub request hang: reading .git/config under
// ~/Desktop from a LaunchAgent that macOS has not granted access to does not
// fail, it blocks - so the handler never returned, no response was written,
// and the phone sat on a spinner forever. Pane chips are a nice-to-have; they
// must never be able to hold up the screen they decorate.
func (s *Server) panesByRepo(*http.Request) map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paneRepos
}

// WatchPanes reads the workspace on a timer until ctx is cancelled, and hands
// the tree to everything that wants the whole workspace regularly.
//
// This is remux's only unconditional pass over every pane. The per-connection
// poller stops when the phone disconnects and the push watcher only ticks when
// a subscription exists, so anything that has to keep working with nobody
// looking belongs here - and belongs here rather than in a loop of its own,
// because the tree read is already happening and #32 was exactly the cost of
// sweeping the same panes twice.
//
// It recomputes the pane/repo mapping off the request path on purpose - see
// panesByRepo. If a probe blocks, this goroutine is the only thing that waits,
// and the matcher abandons that directory rather than retrying it every tick.
func (s *Server) WatchPanes(ctx context.Context, every time.Duration) {
	if s.Match == nil && s.OnTree == nil {
		return
	}
	if every <= 0 {
		every = 5 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		s.watchPanes(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) watchPanes(ctx context.Context) {
	tree, err := s.Tmux.Tree(ctx)
	if err != nil {
		return
	}

	if s.Match != nil {
		paths := make(map[string]string, len(tree.Panes()))
		for _, p := range tree.Panes() {
			paths[p.ID] = p.Path
		}
		byRepo := s.Match.Panes(paths)

		// Stamp each pane with its repo on the way past. The matcher has just
		// resolved every one of these directories and its answers are cached,
		// so this is a map lookup - and it saves the naming pass either
		// reaching into the api package or walking the filesystem itself,
		// which is the thing that must not happen on a shared loop.
		for repo, ids := range byRepo {
			for _, id := range ids {
				if pane := tree.Pane(id); pane != nil {
					pane.Repo = repo
				}
			}
		}

		s.mu.Lock()
		s.paneRepos = byRepo
		s.mu.Unlock()
	}

	if s.OnTree != nil {
		s.OnTree(ctx, tree)
	}
}

// ── one repo ──────────────────────────────────────────────────────────────

func (s *Server) handleGitHubIssues(w http.ResponseWriter, r *http.Request) {
	full, ok := repoParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid repo")
		return
	}
	filter := gh.IssuesMine
	if r.URL.Query().Get("filter") == "all" {
		filter = gh.IssuesAll
	}
	page, err := s.GHClient.Issues(r.Context(), full, filter,
		s.GH.Snapshot().Viewer, r.URL.Query().Get("after"))
	if err != nil {
		writeGHErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleGitHubPRs(w http.ResponseWriter, r *http.Request) {
	full, ok := repoParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid repo")
		return
	}
	prs, err := s.GHClient.PRs(r.Context(), full, 100)
	if err != nil {
		writeGHErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prs": prs})
}

func (s *Server) handleGitHubIssue(w http.ResponseWriter, r *http.Request) {
	full, ok := repoParam(r)
	num, numOK := numberParam(r)
	if !ok || !numOK {
		writeErr(w, http.StatusBadRequest, "invalid repo or number")
		return
	}
	issue, err := s.GHClient.Issue(r.Context(), full, num)
	if err != nil {
		writeGHErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, issue)
}

func (s *Server) handleGitHubPR(w http.ResponseWriter, r *http.Request) {
	full, ok := repoParam(r)
	num, numOK := numberParam(r)
	if !ok || !numOK {
		writeErr(w, http.StatusBadRequest, "invalid repo or number")
		return
	}
	pr, err := s.GHClient.PR(r.Context(), full, num)
	if err != nil {
		writeGHErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pr)
}

// ── the watchlist ─────────────────────────────────────────────────────────

// handleGitHubPicker backs the Add repo sheet.
//
// With no query it lists what Kevin can already see, most recently pushed
// first, which is the right answer without him typing anything. With a query
// it searches all of GitHub, because the repos he watches without belonging to
// - apache/shardingsphere - can only be found that way.
func (s *Server) handleGitHubPicker(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	var (
		repos []gh.Repo
		err   error
	)
	if q == "" {
		repos, err = s.GHClient.Mine(r.Context(), 30)
	} else {
		repos, err = s.GHClient.SearchRepos(r.Context(), q, 20)
	}
	if err != nil {
		writeGHErr(w, r, err)
		return
	}

	// Logged on the way out whatever happens. A picker call only exists
	// because somebody tapped Add, so there is no volume to worry about and
	// every one of them is worth a line - a failure-only log left silence
	// meaning both "it worked" and "the request never arrived".
	log.Printf("github picker: q=%q returned %d repos", q, len(repos))

	watched := map[string]bool{}
	for _, full := range s.Watchlist() {
		watched[strings.ToLower(full)] = true
	}
	// The picker gets pane chips too, and they matter most here: a repo
	// already open on the laptop is nearly always the one being added, and
	// showing that is what makes the list right before any typing.
	byRepo := s.panesByRepo(r)

	type pick struct {
		gh.Repo
		Watched bool `json:"watched"`
	}
	out := make([]pick, len(repos))
	for i, rp := range repos {
		rp.Panes = byRepo[rp.Full]
		out[i] = pick{Repo: rp, Watched: watched[strings.ToLower(rp.Full)]}
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": out})
}

func (s *Server) handleGitHubWatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Repo string `json:"repo"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !gh.ValidRepo(body.Repo) {
		writeErr(w, http.StatusBadRequest, "invalid repo")
		return
	}
	list, err := s.setWatchlist(append(s.Watchlist(), body.Repo))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "github", body.Repo, "watched")
	s.GH.Kick()
	writeJSON(w, http.StatusOK, map[string]any{"repos": list})
}

func (s *Server) handleGitHubUnwatch(w http.ResponseWriter, r *http.Request) {
	full, ok := repoParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid repo")
		return
	}
	var kept []string
	for _, x := range s.Watchlist() {
		if !strings.EqualFold(x, full) {
			kept = append(kept, x)
		}
	}
	list, err := s.setWatchlist(kept)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "github", full, "unwatched")
	s.GH.Kick()
	writeJSON(w, http.StatusOK, map[string]any{"repos": list})
}

// ── muting ────────────────────────────────────────────────────────────────

// mutes is a copy of the dismissed set.
func (s *Server) mutes() map[string]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]time.Time, len(s.Cfg.Muted))
	for k, v := range s.Cfg.Muted {
		out[k] = v
	}
	return out
}

// IgnoredChecks is the check names that do not count as a failure. Exported
// because the gh client reads it fresh on every call, so an edit from the
// phone takes effect without a restart.
func (s *Server) IgnoredChecks() gh.Ignored {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(gh.Ignored, len(s.Cfg.IgnoreChecks))
	copy(out, s.Cfg.IgnoreChecks)
	return out
}

// handleGitHubMute dismisses one inbox row.
//
// The timestamp is taken from the server's own snapshot rather than from the
// request. The phone would be sending back a value it read from this same
// snapshot, and trusting it would let a stale screen mute an item as of a
// moment that has already passed - which is exactly the case where the row
// should have come back instead.
func (s *Server) handleGitHubMute(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Repo   string `json:"repo"`
		Number int    `json:"number"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !gh.ValidRepo(body.Repo) || body.Number <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid item")
		return
	}

	at := time.Now()
	snap := s.GH.Snapshot()
	for _, list := range [][]gh.InboxItem{snap.Inbox.NeedsYou, snap.Inbox.Assigned, snap.Inbox.YourPRs} {
		for _, it := range list {
			if strings.EqualFold(it.Repo, body.Repo) && it.Number == body.Number {
				at = it.Updated
			}
		}
	}

	if err := s.setMute(muteKey(body.Repo, body.Number), at, true); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "github", body.Repo+"#"+strconv.Itoa(body.Number), "muted")
	writeJSON(w, http.StatusOK, s.githubSnapshot(r))
}

func (s *Server) handleGitHubUnmute(w http.ResponseWriter, r *http.Request) {
	full, ok := repoParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid repo")
		return
	}
	n, ok := numberParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid number")
		return
	}
	if err := s.setMute(muteKey(full, n), time.Time{}, false); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "github", full+"#"+strconv.Itoa(n), "unmuted")
	writeJSON(w, http.StatusOK, s.githubSnapshot(r))
}

// setMute adds or removes one entry, in memory and on disk.
//
// Entries older than 90 days are dropped on the way past. A muted pull request
// that was merged long ago is never coming back through the inbox to clear
// itself, and without this the file would only ever grow.
func (s *Server) setMute(key string, at time.Time, on bool) error {
	cutoff := time.Now().AddDate(0, 0, -90)

	edit := func(m map[string]time.Time) map[string]time.Time {
		out := make(map[string]time.Time, len(m)+1)
		for k, v := range m {
			if k != key && v.After(cutoff) {
				out[k] = v
			}
		}
		if on {
			out[key] = at
		}
		return out
	}

	s.mu.Lock()
	s.Cfg.Muted = edit(s.Cfg.Muted)
	s.mu.Unlock()

	_, err := config.Update(func(c *config.Config) { c.Muted = edit(c.Muted) })
	return err
}

// Watchlist is the repos the GitHub poller should read. It is exported
// because the poller reads it fresh on every tick, so adding a repo takes
// effect without a restart.
func (s *Server) Watchlist() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.Cfg.Repos))
	copy(out, s.Cfg.Repos)
	return out
}

// setWatchlist normalises, stores and persists. The normalise step is what
// keeps a duplicate or a malformed name out of the config file rather than
// having every reader defend against one.
func (s *Server) setWatchlist(list []string) ([]string, error) {
	list = gh.NormalizeWatchlist(list)

	s.mu.Lock()
	s.Cfg.Repos = list
	s.mu.Unlock()

	if _, err := config.Update(func(c *config.Config) { c.Repos = list }); err != nil {
		return nil, err
	}
	// A removal is answerable without the network, so answer it now. Waiting
	// for the kicked poll would leave the repo on screen for seconds after the
	// tap that removed it.
	if s.GH != nil {
		s.GH.Retain(list)
	}
	return list, nil
}
