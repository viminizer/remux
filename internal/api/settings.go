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

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, notifySettings{
		NotifyWaiting: s.Cfg.NotifyWaiting,
		NotifyDone:    s.Cfg.NotifyDone,
		NotifyCI:      s.Cfg.NotifyCI,
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
	body.Repos = s.Cfg.Repos
	s.mu.Unlock()

	if _, err := config.Update(func(c *config.Config) {
		c.NotifyWaiting = body.NotifyWaiting
		c.NotifyDone = body.NotifyDone
		c.NotifyCI = body.NotifyCI
		c.IgnoreChecks = body.IgnoreChecks
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
