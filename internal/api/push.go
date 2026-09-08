package api

import (
	"net/http"

	"github.com/viminizer/remux/internal/push"
)

func (s *Server) handlePushKey(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		writeErr(w, http.StatusServiceUnavailable, "push not configured")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"publicKey":     s.Push.PublicKey(),
		"subscriptions": s.Push.Count(),
	})
}

func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		writeErr(w, http.StatusServiceUnavailable, "push not configured")
		return
	}
	var sub push.Subscription
	if err := readJSON(r, &sub); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if sub.Endpoint == "" {
		writeErr(w, http.StatusBadRequest, "missing endpoint")
		return
	}
	if err := s.Push.Add(sub); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "push-subscribe", "", sub.Endpoint)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "subscriptions": s.Push.Count()})
}

func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		writeErr(w, http.StatusServiceUnavailable, "push not configured")
		return
	}
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Push.Remove(body.Endpoint); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
