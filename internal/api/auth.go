package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/viminizer/remux/internal/config"
	"tailscale.com/client/local"
	"tailscale.com/tailcfg"
)

// Version is stamped at build time by scripts/build.sh.
var Version = "dev"

// Auth turns a tailnet connection into an identity.
//
// There is no token anywhere in remux. The caller's login comes from the
// Tailscale connection itself via WhoIs, so there is nothing to paste, nothing
// to store in localStorage and nothing to leak. Under --local this is nil and
// the middleware is skipped, because the listener is loopback-only.
type Auth struct {
	LC         *local.Client
	AllowLogin string
}

type ctxKey string

const loginKey ctxKey = "remux.login"

// whois resolves the caller's tailnet identity.
func (a *Auth) whois(ctx context.Context, remoteAddr string) (*tailcfg.UserProfile, error) {
	res, err := a.LC.WhoIs(ctx, remoteAddr)
	if err != nil {
		return nil, err
	}
	if res.UserProfile == nil {
		return nil, fmt.Errorf("no user profile for %s", remoteAddr)
	}
	return res.UserProfile, nil
}

// Check reports whether a connection is allowed, and by whom.
func (a *Auth) Check(ctx context.Context, remoteAddr string) (string, bool) {
	who, err := a.whois(ctx, remoteAddr)
	if err != nil {
		return "", false
	}
	login := who.LoginName
	if a.AllowLogin == "" {
		// First allowed caller pins the identity, so the common
		// single-user case needs no configuration.
		a.AllowLogin = login
		log.Printf("auth: pinned allowed login to %s", login)
		return login, true
	}
	return login, strings.EqualFold(login, a.AllowLogin)
}

// withAuth gates every route, including the WebSocket upgrade.
//
// Gating the upgrade matters as much as gating REST: a socket authorised only
// at connect time and then never re-checked would otherwise be the one way to
// keep talking to tmux after access was removed.
func (s *Server) withAuth(next http.Handler) http.Handler {
	if s.Auth == nil {
		return next // --local: loopback only, nothing to authenticate
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The UI itself must load so it can render the "Not authorized"
		// screen; only the API and the socket are gated.
		if !strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/ws" {
			next.ServeHTTP(w, r)
			return
		}
		login, ok := s.Auth.Check(r.Context(), r.RemoteAddr)
		if !ok {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error":   "forbidden",
				"login":   login,
				"allowed": s.Auth.AllowLogin,
				"detail":  "Nothing was sent to tmux. The request was rejected before it reached the server.",
			})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), loginKey, login)))
	})
}

func (s *Server) callerLogin(r *http.Request) string {
	if v, ok := r.Context().Value(loginKey).(string); ok {
		return v
	}
	if s.Auth == nil {
		return "local"
	}
	return ""
}

// ── audit log ─────────────────────────────────────────────────────────────

// AuditLog records every mutating call: who, what tmux target, and what was
// sent. It is a plain append-only text file - the point is that Kevin can read
// it, not that it is machine-parseable.
type AuditLog struct {
	mu sync.Mutex
	f  *os.File
}

func OpenAuditLog() (*AuditLog, error) {
	dir, err := config.EnsureDir()
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "audit.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &AuditLog{f: f}, nil
}

func (a *AuditLog) Write(login, action, target, detail string) {
	if a == nil || a.f == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	fmt.Fprintf(a.f, "%s\t%s\t%s\t%s\t%s\n",
		time.Now().Format(time.RFC3339), login, action, target,
		strconv.Quote(detail))
}

func (a *AuditLog) Close() error {
	if a == nil || a.f == nil {
		return nil
	}
	return a.f.Close()
}

func (s *Server) audit(r *http.Request, action, target, detail string) {
	login := s.callerLogin(r)
	if login == "" {
		login = "unknown"
	}
	s.Audit.Write(login, action, target, detail)
}

// ── small helpers ─────────────────────────────────────────────────────────

func hostname() (string, error) { return os.Hostname() }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func atoiClamp(s string, lo, hi int) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// expandHome resolves a leading ~ so the create sheet can accept the paths a
// human actually types.
func expandHome(p string) string {
	if p == "" || !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}
