package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Bound both wait time and the number of blocked filesystem readers. A stalled
// home directory must not pin every future usage request or grow goroutines.
var usageFileSlots = make(chan struct{}, 4)

func readUsageFile(ctx context.Context, path string) ([]byte, error) {
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	select {
	case usageFileSlots <- struct{}{}:
	case <-readCtx.Done():
		return nil, readCtx.Err()
	}
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() { defer func() { <-usageFileSlots }(); data, err := os.ReadFile(path); done <- result{data, err} }()
	select {
	case r := <-done:
		return r.data, r.err
	case <-readCtx.Done():
		return nil, readCtx.Err()
	}
}

// Keep selection aligned with tokscale's native Claude reader. Profile metadata
// is deliberately excluded: it can survive a CLI account switch.
type claudeCredential struct {
	token  string
	expiry *int64
}

func parseClaudeCredential(data []byte) claudeCredential {
	var document struct {
		OAuth struct {
			Token  string          `json:"accessToken"`
			Expiry json.RawMessage `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(data, &document) != nil {
		return claudeCredential{}
	}
	credential := claudeCredential{token: strings.TrimSpace(document.OAuth.Token)}
	var expiry int64
	if len(document.OAuth.Expiry) > 0 && string(document.OAuth.Expiry) != "null" && json.Unmarshal(document.OAuth.Expiry, &expiry) == nil {
		credential.expiry = &expiry
	}
	return credential
}
func selectClaudeCredential(file, keychain claudeCredential, now int64) claudeCredential {
	if keychain.token == "" {
		return file
	}
	if file.token != "" && keychain.expiry != nil && *keychain.expiry <= now && (file.expiry == nil || *file.expiry > now) {
		return file
	}
	return keychain
}
func readClaudeCredential(ctx context.Context) claudeCredential {
	home, err := os.UserHomeDir()
	if err != nil {
		return claudeCredential{}
	}
	fileData, fileErr := readUsageFile(ctx, filepath.Join(home, ".claude", ".credentials.json"))
	if errors.Is(fileErr, context.DeadlineExceeded) || errors.Is(fileErr, context.Canceled) {
		return claudeCredential{}
	}
	var keychainData []byte
	if runtime.GOOS == "darwin" {
		readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		command := exec.CommandContext(readCtx, "/usr/bin/security", "find-generic-password", "-s", "Claude Code-credentials", "-w")
		command.WaitDelay = time.Second
		if output, err := command.Output(); err == nil {
			keychainData = output
		}
		expired := readCtx.Err() != nil
		cancel()
		if expired {
			return claudeCredential{}
		}
	}
	return selectClaudeCredential(parseClaudeCredential(fileData), parseClaudeCredential(keychainData), time.Now().UnixMilli())
}
func claudeCredentialEmail(ctx context.Context, token string, client *http.Client) string {
	if token == "" {
		return ""
	}
	profileCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(profileCtx, http.MethodGet, "https://api.anthropic.com/api/oauth/profile", nil)
	if err != nil {
		return ""
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("anthropic-beta", "oauth-2025-04-20")
	response, err := client.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	var profile struct {
		Account struct {
			Email string `json:"email"`
		} `json:"account"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&profile) != nil {
		return ""
	}
	return strings.TrimSpace(profile.Account.Email)
}
