package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/viminizer/remux/internal/config"
)

// The notification toggles have to live on the server, not just on the phone.
//
// The watcher runs in this process and reads them, so a switch that only wrote
// to localStorage would appear to work and change nothing - the phone would
// keep getting notifications it had just turned off.
type notifySettings struct {
	NotifyWaiting bool `json:"notifyWaiting"`
	NotifyDone    bool `json:"notifyDone"`
	// NotifyCI is the same argument for the GitHub watcher: it reads the
	// flag in this process, so the switch has to reach the server.
	NotifyCI bool `json:"notifyCi"`
	// NamePanes is the same argument again, and the one switch here that is
	// about money rather than noise: the naming pass reads it in this
	// process, on every tick.
	NamePanes bool `json:"namePanes"`
	// Repos is read-only here. It is shown on the Settings screen so the
	// watchlist is visible in one place, but it is edited through the
	// GitHub screen's Add and Remove, which also kick the poller.
	Repos []string `json:"repos"`
	// IgnoreChecks names checks that do not count as a failure. Editable
	// here, because it is a preference and not a per-item decision - and
	// because the gh client in this process is what applies it.
	IgnoreChecks []string `json:"ignoreChecks"`
}

// normalizeChecks trims, drops blanks, folds duplicates that differ only in
// case, and sorts. A list of check names is a set, and letting the same name
// in twice would only make the Settings screen look broken.
func normalizeChecks(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, x := range in {
		x = strings.TrimSpace(x)
		k := strings.ToLower(x)
		if x == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

// The four switches are read from three goroutines - this handler, the push
// watcher's ticker, and the naming pass on WatchPanes - and written by
// handlePutSettings. Every read goes through one of these so the write below
// is the only place that touches the fields, all of them under s.mu.
//
// They are methods on Server rather than closures over the config, because a
// closure over a plain bool is exactly what the race was: `func() bool {
// return cfg.NamePanes }` looks like a read of a value and is a read of shared
// memory on a loop that runs every two seconds.

func (s *Server) NotifyWaiting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Cfg.NotifyWaiting
}

func (s *Server) NotifyDone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Cfg.NotifyDone
}

func (s *Server) NotifyCI() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Cfg.NotifyCI
}

func (s *Server) NamePanes() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Cfg.NamePanes
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, notifySettings{
		NotifyWaiting: s.NotifyWaiting(),
		NotifyDone:    s.NotifyDone(),
		NotifyCI:      s.NotifyCI(),
		NamePanes:     s.NamePanes(),
		Repos:         s.Watchlist(),
		IgnoreChecks:  s.IgnoredChecks(),
	})
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var body notifySettings
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	body.IgnoreChecks = normalizeChecks(body.IgnoreChecks)

	s.mu.Lock()
	s.Cfg.NotifyWaiting = body.NotifyWaiting
	s.Cfg.NotifyDone = body.NotifyDone
	s.Cfg.NotifyCI = body.NotifyCI
	s.Cfg.IgnoreChecks = body.IgnoreChecks
	s.Cfg.NamePanes = body.NamePanes
	body.Repos = s.Cfg.Repos
	s.mu.Unlock()

	if _, err := config.Update(func(c *config.Config) {
		c.NotifyWaiting = body.NotifyWaiting
		c.NotifyDone = body.NotifyDone
		c.NotifyCI = body.NotifyCI
		c.IgnoreChecks = body.IgnoreChecks
		c.NamePanes = body.NamePanes
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The ignore list changes what the poller calls red, so the next snapshot
	// has to be recomputed rather than waiting out the interval.
	if s.GH != nil {
		s.GH.Kick()
	}
	s.audit(r, "settings", "", "notifications updated")
	writeJSON(w, http.StatusOK, body)
}
