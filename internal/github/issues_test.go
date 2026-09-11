package github

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stubGH writes a fake gh that prints body on stdout and exits 0.
func stubGH(t *testing.T, body string) *Client {
	t.Helper()
	p := filepath.Join(t.TempDir(), "gh")
	script := "#!/bin/sh\ncat <<'JSON'\n" + body + "\nJSON\n"
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return &Client{Bin: p, Timeout: 5 * time.Second}
}

// An empty Yours tab has to marshal as [], not null.
//
// The phone reads null as "still loading" and spins the skeleton forever, so a
// repo with nothing of Kevin's in it - the common case, and the tab a repo
// opens on - looked broken rather than empty. The All path never had this,
// which is what made the two paths in one file disagree.
func TestIssuesMineEmptyIsNotNull(t *testing.T) {
	c := stubGH(t, `{"data":{"repository":{
		"assigned":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]},
		"created":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]}
	}}}`)

	page, err := c.issuesMine(context.Background(), "viminizer", "remux", "viminizer")
	if err != nil {
		t.Fatalf("issuesMine: %v", err)
	}
	if page.Issues == nil {
		t.Error("Issues is nil; the phone reads that as still loading")
	}

	b, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"issues":[]}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
