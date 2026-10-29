package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUsageFiltersAccountsAndValidatesClaudeIdentity(t *testing.T) {
	configured := map[string]string{"claude": "reader@example.com", "codex": "coder@example.com"}
	data := []byte(`[
 {"provider":"Claude","plan":"Max","metrics":[{"label":"Session","remaining_percent":82,"resets_at":"2026-11-01T00:00:00Z"}]},
 {"provider":"Codex","email":"other@example.com","plan":"Wrong","account":{"id":"private-account-id"},"metrics":[{"label":"Weekly","remaining_percent":1}]},
 {"provider":"Codex","email":"CODER@example.com","plan":"Pro","metrics":[{"label":"Weekly","remaining_percent":23}]},
 {"provider":"Copilot","email":"reader@example.com","metrics":[{"label":"Premium","remaining_percent":100}]}
 ]`)
	now := time.Now()
	result := parseUsage(data, configured, "reader@example.com", "reader@example.com", now)
	if len(result.Accounts) != 2 || result.Accounts[0].Error != "" || result.Accounts[1].Plan != "Pro" {
		t.Fatalf("wrong filtering: %+v", result)
	}
	encoded, _ := json.Marshal(result)
	for _, hidden := range []string{"other@example.com", "private-account-id", "Copilot"} {
		if strings.Contains(string(encoded), hidden) {
			t.Fatalf("response leaked %s", hidden)
		}
	}
	result = parseUsage(data, configured, "reader@example.com", "switched@example.com", now)
	if result.Accounts[0].Error == "" || len(result.Accounts[0].Metrics) != 0 || result.Accounts[1].Error != "" {
		t.Fatalf("account switch must fail Claude independently: %+v", result)
	}
	result = parseUsage(data, configured, "", "", now)
	if result.Accounts[0].Error == "" {
		t.Fatal("unknown Claude identity accepted")
	}
}

func TestUsageRejectsMissingOrInvalidMetrics(t *testing.T) {
	configured := map[string]string{"claude": "reader@example.com", "codex": "coder@example.com"}
	for _, metrics := range []string{`[]`, `[{"label":"Weekly"}]`, `[{"label":"Weekly","remaining_percent":101}]`, `[{"label":"Weekly","remaining_percent":-1}]`, `[{"label":"Weekly","remaining_percent":20,"resets_at":"bad"}]`} {
		result := parseUsage([]byte(`[{"provider":"Codex","email":"coder@example.com","metrics":`+metrics+`}]`), configured, "", "", time.Now())
		if result.Accounts[1].Error == "" {
			t.Fatalf("accepted invalid metrics: %s", metrics)
		}
	}
	result := parseUsage([]byte(`not json`), configured, "", "", time.Now())
	if len(result.Accounts) != 2 || result.Accounts[0].Error == "" || result.Accounts[1].Error == "" {
		t.Fatal("malformed response must fail both providers")
	}
}

func TestUsageCacheSingleflightAndStaleSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", home)
	t.Setenv("USAGE_TEST_COUNT", filepath.Join(home, "calls"))
	t.Setenv("USAGE_TEST_DATA", filepath.Join(home, "data"))
	if err := os.MkdirAll(filepath.Join(home, ".config", "remux"), 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		".config/remux/model-usage.json": `{"claude":"reader@example.com","codex":"coder@example.com"}`,
		".claude.json":                   `{"oauthAccount":{"emailAddress":"reader@example.com"}}`,
		"data":                           `[{"provider":"Codex","email":"coder@example.com","metrics":[{"label":"Weekly","remaining_percent":23}]}]`,
		"bunx":                           "#!/bin/sh\necho call >> \"$USAGE_TEST_COUNT\"\n/bin/sleep 0.05\n/bin/cat \"$USAGE_TEST_DATA\"\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	service := usageService{credential: func(context.Context) claudeCredential { return claudeCredential{} }}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			r := service.get(context.Background(), false)
			if len(r.Accounts) != 2 || r.Accounts[1].Error != "" {
				t.Errorf("bad shared result: %+v", r)
			}
		}()
	}
	group.Wait()
	calls, err := os.ReadFile(filepath.Join(home, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(calls), "call") != 1 {
		t.Fatalf("expected one collection: %s", calls)
	}
	if err := os.WriteFile(filepath.Join(home, "data"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	result := service.get(context.Background(), true)
	if !result.Accounts[1].Stale || result.Accounts[1].Error == "" || len(result.Accounts[1].Metrics) != 1 {
		t.Fatalf("failed refresh lost snapshot: %+v", result)
	}
	if err := os.WriteFile(filepath.Join(home, ".config/remux/model-usage.json"), []byte(`{"claude":"reader@example.com","codex":"changed@example.com"}`), 0600); err != nil {
		t.Fatal(err)
	}
	result = service.get(context.Background(), false)
	if result.Accounts[1].Email != "changed@example.com" || len(result.Accounts[1].Metrics) != 0 || result.Accounts[1].Stale {
		t.Fatalf("account change reused snapshot: %+v", result)
	}
}

func TestUsageClaudeCredentialSelection(t *testing.T) {
	for _, tc := range []struct {
		file, keychain string
		want           string
	}{
		{`{"claudeAiOauth":{"accessToken":"file","expiresAt":2000}}`, `{"claudeAiOauth":{"accessToken":"keychain","expiresAt":1800}}`, "keychain"},
		{`{"claudeAiOauth":{"accessToken":"file","expiresAt":2000}}`, `{"claudeAiOauth":{"accessToken":"keychain","expiresAt":900}}`, "file"},
		{`{"claudeAiOauth":{"accessToken":"file","expiresAt":800}}`, `{"claudeAiOauth":{"accessToken":"keychain","expiresAt":900}}`, "keychain"},
		{`{"claudeAiOauth":{"accessToken":"file","expiresAt":2000}}`, `{"claudeAiOauth":{"accessToken":"keychain","expiresAt":"unknown"}}`, "keychain"},
		{`{"claudeAiOauth":{"accessToken":"file"}}`, `{"claudeAiOauth":{"accessToken":" "}}`, "file"},
		{`invalid`, `{"claudeAiOauth":{"accessToken":"keychain"}}`, "keychain"},
	} {
		got := selectClaudeCredential(parseClaudeCredential([]byte(tc.file)), parseClaudeCredential([]byte(tc.keychain)), 1000)
		if got.token != tc.want {
			t.Fatalf("wrong credential selection: wanted %s", tc.want)
		}
	}
}

type usageProfileTransport func(*http.Request) (*http.Response, error)

func (f usageProfileTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestUsageClaudeProfileVerifiesCredential(t *testing.T) {
	client := &http.Client{Transport: usageProfileTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.anthropic.com/api/oauth/profile" || r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
			t.Fatal("incorrect profile request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"account":{"email":"verified@example.com"}}`)), Header: make(http.Header)}, nil
	})}
	if got := claudeCredentialEmail(context.Background(), "test-token", client); got != "verified@example.com" {
		t.Fatalf("wrong identity: %s", got)
	}
	client.Transport = usageProfileTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(`{"account":{"email":"untrusted@example.com"}}`))}, nil
	})
	if got := claudeCredentialEmail(context.Background(), "test-token", client); got != "" {
		t.Fatal("accepted non-success profile")
	}
}

func TestUsageCodexDuplicateEmailSelection(t *testing.T) {
	configured := map[string]string{"claude": "reader@example.com", "codex": "coder@example.com"}
	row := `{"provider":"Codex","email":"coder@example.com","metrics":[{"label":"Weekly","remaining_percent":23}]}`
	active := `{"provider":"Codex","email":"coder@example.com","account":{"is_active":true},"plan":"Active","metrics":[{"label":"Weekly","remaining_percent":42}]}`
	for _, tc := range []struct {
		data  string
		valid bool
	}{
		{"[" + row + "," + row + "]", false},
		{"[" + row + "," + active + "]", true},
		{"[" + active + "," + active + "]", false},
	} {
		result := parseUsage([]byte(tc.data), configured, "", "", time.Now())
		if (result.Accounts[1].Error == "") != tc.valid {
			t.Fatalf("wrong duplicate selection: %+v", result)
		}
		if tc.valid && result.Accounts[1].Plan != "Active" {
			t.Fatal("did not prefer active exact account")
		}
	}
}
func TestUsageFileReadHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < cap(usageFileSlots); i++ {
		usageFileSlots <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(usageFileSlots); i++ {
			<-usageFileSlots
		}
	}()
	start := time.Now()
	if _, err := readUsageFile(ctx, "unused"); err == nil {
		t.Fatal("canceled reader accepted")
	}
	if time.Since(start) > time.Second {
		t.Fatal("blocked reader slots stalled caller")
	}
}
