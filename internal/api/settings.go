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
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, notifySettings{
		NotifyWaiting: s.Cfg.NotifyWaiting,
		NotifyDone:    s.Cfg.NotifyDone,
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
	cfg := *s.Cfg
	s.mu.Unlock()

	if err := config.Save(&cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "settings", "", "notifications updated")
	writeJSON(w, http.StatusOK, body)
}
