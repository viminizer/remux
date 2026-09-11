package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/viminizer/remux/internal/config"
	gh "github.com/viminizer/remux/internal/github"
	"github.com/viminizer/remux/internal/tmux"
)

// githubServer builds a server with the GitHub routes wired up and a private
// HOME, so the watchlist round trip writes to a temporary config file and
// never touches the real one.
//
// The gh client points at a binary that does not exist. That is deliberate:
// every test here is about the routes, and a read that reaches GitHub would
// make them slow and dependent on the network. The one call that must work -
// the watchlist - never shells out at all.
func githubServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := config.Default()
	cfg.TreeMS = 60000
	srv := NewServer(cfg, tmux.New(), nil)
	srv.GHClient = &gh.Client{Bin: filepath.Join(home, "no-such-gh"), Timeout: time.Second}
	srv.Match = gh.NewMatcher()
	srv.GH = &gh.Poller{Client: srv.GHClient, Watchlist: srv.Watchlist}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv
}

// The snapshot route reads the poller's cache, so it answers instantly and
// never waits on GitHub - including before the first poll has finished.
func TestGitHubSnapshotIsCached(t *testing.T) {
	ts, _ := githubServer(t)

	start := time.Now()
	code, body := do(t, ts, "GET", "/api/github", nil)
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v - the handler waited on something", time.Since(start))
	}

	var snap gh.Snapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Repos == nil {
		t.Error("repos should be an empty list, not null")
	}
}

func TestGitHubWatchlistRoundTrip(t *testing.T) {
	ts, srv := githubServer(t)

	repos := func(body []byte) []string {
		t.Helper()
		var v struct {
			Repos []string `json:"repos"`
		}
		if err := json.Unmarshal(body, &v); err != nil {
			t.Fatalf("json: %v (%s)", err, body)
		}
		return v.Repos
	}

	code, body := do(t, ts, "POST", "/api/github/watch", map[string]string{"repo": "viminizer/remux"})
	if code != http.StatusOK {
		t.Fatalf("watch: %d %s", code, body)
	}
	if got := repos(body); len(got) != 1 || got[0] != "viminizer/remux" {
		t.Fatalf("after watch: %v", got)
	}

	// Adding the same repo twice must not add it twice.
	_, body = do(t, ts, "POST", "/api/github/watch", map[string]string{"repo": "viminizer/remux"})
	if got := repos(body); len(got) != 1 {
		t.Errorf("duplicate add: %v", got)
	}

	// It reached the config file, which is what survives a restart.
	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Repos) != 1 || saved.Repos[0] != "viminizer/remux" {
		t.Errorf("config on disk: %v", saved.Repos)
	}
	if got := srv.Watchlist(); len(got) != 1 {
		t.Errorf("in-memory watchlist: %v", got)
	}

	code, body = do(t, ts, "DELETE", "/api/github/watch/viminizer/remux", nil)
	if code != http.StatusOK {
		t.Fatalf("unwatch: %d %s", code, body)
	}
	if got := repos(body); len(got) != 0 {
		t.Errorf("after unwatch: %v", got)
	}
}

// A settings save must not carry a command line flag into the config file.
//
// --port, --lines, --poll and --hostname all overwrite the in-memory Config,
// so writing that copy meant a development server run once on another port
// moved the installed service to it. Every save goes through config.Update,
// which reads the file first.
func TestSettingsSaveDoesNotPersistFlags(t *testing.T) {
	ts, srv := githubServer(t)

	// Whatever was on disk before, plus a flag override in memory only.
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	srv.Cfg.Port = 7401
	srv.mu.Unlock()

	code, body := do(t, ts, "PUT", "/api/settings",
		notifySettings{NotifyWaiting: true, NotifyDone: true, NotifyCI: true})
	if code != http.StatusOK {
		t.Fatalf("put: %d %s", code, body)
	}

	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Port != config.Default().Port {
		t.Errorf("port on disk is %d, want the file's own %d", saved.Port, config.Default().Port)
	}
	if !saved.NotifyDone {
		t.Error("the setting that was actually changed did not persist")
	}
}

func TestGitHubRejectsBadRepo(t *testing.T) {
	ts, _ := githubServer(t)

	// A name starting with "-" would be read by gh as a flag rather than a
	// value, which is the whole reason repo names are validated.
	for _, path := range []string{
		"/api/github/repos/-evil/name/issues",
		"/api/github/repos/owner/-evil/prs",
		"/api/github/repos/owner/name/issues/0",
		"/api/github/repos/owner/name/prs/abc",
	} {
		if code, body := do(t, ts, "GET", path, nil); code != http.StatusBadRequest {
			t.Errorf("GET %s: status %d (want 400), body %s", path, code, body)
		}
	}

	for _, repo := range []string{"", "noslash", "-evil/name", "owner/name/extra"} {
		code, _ := do(t, ts, "POST", "/api/github/watch", map[string]string{"repo": repo})
		if code != http.StatusBadRequest {
			t.Errorf("watch %q: status %d, want 400", repo, code)
		}
	}
}

// gh being missing is a state the screen draws, not a 500. It has to arrive
// as 401 with a kind the UI can switch on, because only Kevin at the laptop
// can fix it.
func TestGitHubReportsMissingGh(t *testing.T) {
	ts, _ := githubServer(t)

	code, body := do(t, ts, "GET", "/api/github/repos/viminizer/remux/issues", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("status %d (want 401), body %s", code, body)
	}
	var v struct{ Kind string }
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	if v.Kind != "nogh" {
		t.Errorf("kind = %q, want nogh", v.Kind)
	}
}

// With no poller the routes are not registered at all, so nothing can call
// into a nil pointer.
func TestGitHubRoutesAbsentWithoutPoller(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := NewServer(config.Default(), tmux.New(), nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	if code, _ := do(t, ts, "GET", "/api/github", nil); code != http.StatusNotFound {
		t.Errorf("status %d, want 404", code)
	}
}

// A mute keeps a row out of the inbox until the row itself moves.
func TestApplyMutesLapsesWhenTheItemChanges(t *testing.T) {
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	snap := gh.Snapshot{Inbox: gh.Inbox{
		NeedsYou: []gh.InboxItem{
			{Repo: "viminizer/remux", Number: 41, Updated: at},
			{Repo: "viminizer/remux", Number: 42, Updated: at},
		},
		Assigned: []gh.InboxItem{{Repo: "yegadev/lugar-flow", Number: 71, Updated: at}},
	}}

	// Case is not significant in a repo name, so it must not be in the key.
	muted := map[string]time.Time{
		"viminizer/remux#41":    at,
		"YegaDev/lugar-flow#71": at,
	}
	got := applyMutes(snap, muted)
	if len(got.Inbox.NeedsYou) != 1 || got.Inbox.NeedsYou[0].Number != 42 {
		t.Errorf("needs you: %+v", got.Inbox.NeedsYou)
	}
	if len(got.Inbox.Assigned) != 0 {
		t.Errorf("assigned: %+v", got.Inbox.Assigned)
	}
	if len(got.Muted) != 2 {
		t.Errorf("muted: %+v", got.Muted)
	}

	// One new comment on #41 and it is waiting again.
	snap.Inbox.NeedsYou[0].Updated = at.Add(time.Minute)
	got = applyMutes(snap, muted)
	if len(got.Inbox.NeedsYou) != 2 {
		t.Errorf("a changed item stayed muted: %+v", got.Inbox.NeedsYou)
	}

	// No mutes at all must not touch the snapshot.
	if plain := applyMutes(snap, nil); len(plain.Muted) != 0 || len(plain.Inbox.Assigned) != 1 {
		t.Errorf("empty mute set changed the snapshot: %+v", plain)
	}
}

// The push watcher hangs off the poller, so it is handed the raw snapshot
// rather than asking for one. It has to go through the same mutes as every
// other reader: muting a row used to clear it from the screen and leave the
// phone buzzing about it, which is the opposite of what muting is for.
func TestApplyMutesIsAvailableToTheWatcher(t *testing.T) {
	_, srv := githubServer(t)
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	srv.mu.Lock()
	srv.Cfg.Muted = map[string]time.Time{"viminizer/remux#42": at}
	srv.mu.Unlock()

	snap := gh.Snapshot{At: at, Inbox: gh.Inbox{NeedsYou: []gh.InboxItem{
		{Repo: "viminizer/remux", Number: 42, Updated: at, Checks: gh.ChecksFail},
		{Repo: "viminizer/remux", Number: 43, Updated: at, Checks: gh.ChecksFail},
	}}}

	got := srv.ApplyMutes(snap)
	if len(got.Inbox.NeedsYou) != 1 || got.Inbox.NeedsYou[0].Number != 43 {
		t.Errorf("a muted row reached the watcher: %+v", got.Inbox.NeedsYou)
	}
	if len(got.Muted) != 1 || got.Muted[0].Number != 42 {
		t.Errorf("the muted row was dropped instead of lifted: %+v", got.Muted)
	}
}

func TestGitHubMuteRoundTrip(t *testing.T) {
	ts, srv := githubServer(t)

	code, body := do(t, ts, "POST", "/api/github/mute",
		map[string]any{"repo": "viminizer/remux", "number": 41})
	if code != http.StatusOK {
		t.Fatalf("mute: %d %s", code, body)
	}
	if got := srv.mutes(); len(got) != 1 {
		t.Fatalf("in-memory mutes: %v", got)
	}

	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Muted["viminizer/remux#41"]; !ok {
		t.Errorf("config on disk: %v", saved.Muted)
	}

	code, body = do(t, ts, "DELETE", "/api/github/mute/viminizer/remux/41", nil)
	if code != http.StatusOK {
		t.Fatalf("unmute: %d %s", code, body)
	}
	if got := srv.mutes(); len(got) != 0 {
		t.Errorf("after unmute: %v", got)
	}

	// A malformed item must not reach the config file.
	if code, _ := do(t, ts, "POST", "/api/github/mute",
		map[string]any{"repo": "nope", "number": 1}); code != http.StatusBadRequest {
		t.Errorf("bad repo: %d", code)
	}
	if code, _ := do(t, ts, "POST", "/api/github/mute",
		map[string]any{"repo": "viminizer/remux", "number": 0}); code != http.StatusBadRequest {
		t.Errorf("bad number: %d", code)
	}
}

func TestSettingsNormalisesIgnoreChecks(t *testing.T) {
	ts, srv := githubServer(t)

	code, body := do(t, ts, "PUT", "/api/settings", map[string]any{
		"notifyWaiting": true,
		"ignoreChecks":  []string{" Vercel ", "vercel", "", "netlify"},
	})
	if code != http.StatusOK {
		t.Fatalf("put: %d %s", code, body)
	}
	got := srv.IgnoredChecks()
	if len(got) != 2 || got[0] != "netlify" || got[1] != "Vercel" {
		t.Errorf("normalised to %v", got)
	}
	if !got.Match("Vercel - Preview") {
		t.Error("the stored list does not match what it was set from")
	}
}
