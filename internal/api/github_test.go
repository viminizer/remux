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
