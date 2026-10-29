package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/viminizer/remux/internal/config"
)

type usageMetric struct {
	Label     string   `json:"label"`
	Remaining *float64 `json:"remaining_percent"`
	Resets    string   `json:"resets_at"`
}
type usageAccount struct {
	Provider  string        `json:"provider"`
	Email     string        `json:"email"`
	Plan      string        `json:"plan,omitempty"`
	Metrics   []usageMetric `json:"metrics,omitempty"`
	CheckedAt *time.Time    `json:"checked_at,omitempty"`
	Error     string        `json:"error,omitempty"`
	Stale     bool          `json:"stale,omitempty"`
}
type usageResponse struct {
	Accounts []usageAccount `json:"accounts"`
	Error    string         `json:"error,omitempty"`
}

// Credential binding mirrors this reader's selection rules; update them together.
const usagePackage = "tokscale@4.20.1"

type usageService struct {
	// Optional native credential reader for isolated verification.
	credential func(context.Context) claudeCredential
	mu         sync.Mutex
	busy       chan struct{}
	cached     usageResponse
	checked    time.Time
	binding    string
}

func usageConfig(ctx context.Context) (map[string]string, string, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, "", err
	}
	b, err := readUsageFile(ctx, filepath.Join(dir, "model-usage.json"))
	if err != nil {
		return nil, "", err
	}
	var c map[string]string
	if json.Unmarshal(b, &c) != nil || strings.TrimSpace(c["claude"]) == "" || strings.TrimSpace(c["codex"]) == "" {
		return nil, "", errors.New("invalid account configuration")
	}
	for k, v := range c {
		c[k] = strings.TrimSpace(v)
	}
	return c, strings.ToLower(c["claude"] + "\n" + c["codex"]), nil
}
func runUsage(ctx context.Context) ([]byte, error) {
	bin, err := exec.LookPath("bunx")
	if err != nil {
		home, e := os.UserHomeDir()
		if e != nil {
			return nil, e
		}
		bin = filepath.Join(home, ".bun", "bin", "bunx")
	}
	cmd := exec.CommandContext(ctx, bin, usagePackage, "usage", "--json")
	// A descendant retaining stdout must not leave this request waiting forever.
	cmd.WaitDelay = time.Second
	return cmd.Output()
}
func parseUsage(data []byte, configured map[string]string, before, after string, now time.Time) usageResponse {
	var rows []struct {
		Provider string        `json:"provider"`
		Email    string        `json:"email"`
		Plan     string        `json:"plan"`
		Metrics  []usageMetric `json:"metrics"`
		Account  struct {
			Active bool `json:"is_active"`
		} `json:"account"`
	}
	result := usageResponse{Accounts: make([]usageAccount, 0, 2)}
	parseErr := json.Unmarshal(data, &rows)
	for _, provider := range []string{"Claude", "Codex"} {
		email := configured[strings.ToLower(provider)]
		a := usageAccount{Provider: provider, Email: email, Error: "Usage unavailable. Check this account's CLI sign-in on the Mac, then refresh."}
		selected := -1
		if provider == "Codex" {
			matches, active := 0, 0
			for i, row := range rows {
				if strings.EqualFold(row.Provider, provider) && strings.EqualFold(row.Email, email) {
					matches++
					selected = i
					if row.Account.Active {
						active++
					}
				}
			}
			if active == 1 {
				for i, row := range rows {
					if strings.EqualFold(row.Provider, provider) && strings.EqualFold(row.Email, email) && row.Account.Active {
						selected = i
					}
				}
			} else if active > 1 || matches != 1 {
				selected = -1
			}
		}
		for index, row := range rows {
			if provider == "Codex" && index != selected {
				continue
			}
			if parseErr != nil || !strings.EqualFold(provider, row.Provider) {
				continue
			}
			identity := row.Email
			if provider == "Claude" && identity == "" {
				if !strings.EqualFold(before, after) {
					continue
				}
				identity = before
			}
			if !strings.EqualFold(identity, email) || identity == "" {
				continue
			}
			if provider == "Claude" && (!strings.EqualFold(before, email) || !strings.EqualFold(after, email)) {
				continue
			}
			valid := len(row.Metrics) > 0
			for _, m := range row.Metrics {
				if m.Label == "" || m.Remaining == nil || math.IsNaN(*m.Remaining) || *m.Remaining < 0 || *m.Remaining > 100 {
					valid = false
				}
				if m.Resets != "" {
					if _, e := time.Parse(time.RFC3339Nano, m.Resets); e != nil {
						valid = false
					}
				}
			}
			if !valid {
				continue
			}
			a.Plan = row.Plan
			a.Metrics = row.Metrics
			a.CheckedAt = &now
			a.Error = ""
			break
		}
		result.Accounts = append(result.Accounts, a)
	}
	return result
}
func (u *usageService) get(ctx context.Context, refresh bool) usageResponse {
	for {
		c, key, err := usageConfig(ctx)
		if err != nil {
			return usageResponse{Accounts: []usageAccount{}, Error: "Model usage accounts are not configured on the Mac."}
		}
		// Cached readings remain explicitly timestamped; Refresh verifies identity again.
		u.mu.Lock()
		if u.busy != nil {
			done := u.busy
			u.mu.Unlock()
			select {
			case <-done:
				refresh = false
				continue
			case <-ctx.Done():
				return usageResponse{Accounts: []usageAccount{}, Error: "Usage request canceled."}
			}
		}
		if key == u.binding && !refresh && time.Since(u.checked) < time.Minute {
			r := u.cached
			u.mu.Unlock()
			return r
		}
		previous := u.cached
		if key != u.binding {
			previous = usageResponse{}
		}
		done := make(chan struct{})
		u.busy = done
		u.mu.Unlock()
		collectCtx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		readCredential := u.credential
		if readCredential == nil {
			readCredential = readClaudeCredential
		}
		credential := readCredential(collectCtx)
		// The same bearer credential must identify the configured account and
		// remain selected across native collection. Never trust .claude.json.
		profileClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		before := claudeCredentialEmail(collectCtx, credential.token, profileClient)
		data, runErr := runUsage(collectCtx)
		afterCredential := readCredential(collectCtx)
		after := before
		if credential.token == "" || sha256.Sum256([]byte(credential.token)) != sha256.Sum256([]byte(afterCredential.token)) {
			after = ""
		}
		cancel()
		if runErr != nil {
			data = nil
		}
		result := parseUsage(data, c, before, after, time.Now())
		for i, a := range result.Accounts {
			if a.Error != "" {
				for _, old := range previous.Accounts {
					if old.Provider == a.Provider && strings.EqualFold(old.Email, a.Email) && old.CheckedAt != nil {
						old.Error = a.Error
						old.Stale = true
						result.Accounts[i] = old
					}
				}
			}
		}
		u.mu.Lock()
		u.cached = result
		u.binding = key
		u.checked = time.Now()
		u.busy = nil
		close(done)
		u.mu.Unlock()
		return result
	}
}
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.usage.get(r.Context(), r.URL.Query().Get("refresh") == "1"))
}
