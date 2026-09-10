package api

import (
	"net/http"

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
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, notifySettings{
		NotifyWaiting: s.Cfg.NotifyWaiting,
		NotifyDone:    s.Cfg.NotifyDone,
		NotifyCI:      s.Cfg.NotifyCI,
		Repos:         s.Watchlist(),
	})
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var body notifySettings
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	s.mu.Lock()
	s.Cfg.NotifyWaiting = body.NotifyWaiting
	s.Cfg.NotifyDone = body.NotifyDone
	s.Cfg.NotifyCI = body.NotifyCI
	body.Repos = s.Cfg.Repos
	s.mu.Unlock()

	if _, err := config.Update(func(c *config.Config) {
		c.NotifyWaiting = body.NotifyWaiting
		c.NotifyDone = body.NotifyDone
		c.NotifyCI = body.NotifyCI
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "settings", "", "notifications updated")
	writeJSON(w, http.StatusOK, body)
}
