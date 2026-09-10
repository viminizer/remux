package github

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestSplitRepo(t *testing.T) {
	ok := []struct{ in, owner, name string }{
		{"viminizer/remux", "viminizer", "remux"},
		{"apache/shardingsphere", "apache", "shardingsphere"},
		{"a/b", "a", "b"},
		{"some-org/dot.name_here", "some-org", "dot.name_here"},
		{"viminizer/.github", "viminizer", ".github"},
	}
	for _, c := range ok {
		o, n, err := SplitRepo(c.in)
		if err != nil || o != c.owner || n != c.name {
			t.Errorf("SplitRepo(%q) = %q,%q,%v", c.in, o, n, err)
		}
	}

	bad := []string{
		"", "noslash", "/name", "owner/", "owner/name/extra",
		// A leading dash would be read by gh as a flag, not a value.
		"-owner/name", "owner/-name",
		"own er/name", "owner/na me", "owner/na;me",
		`owner/na"me`, "owner/..", "owner/.",
		"owner/name\n--json",
	}
	for _, c := range bad {
		if _, _, err := SplitRepo(c); err == nil {
			t.Errorf("SplitRepo(%q) accepted", c)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		want   error
	}{
		{"offline", "error connecting to api.github.com\ncheck your internet connection", ErrOffline},
		{"dns", "Get \"https://api.github.com/user\": dial tcp: lookup api.github.com: no such host", ErrOffline},
		{"bad token", "gh: Bad credentials (HTTP 401)", ErrNoAuth},
		{"logged out", "To get started with GitHub CLI, please run: gh auth login", ErrNoAuth},
		{"gone", "gh: Not Found (HTTP 404)", ErrNotFound},
		{"rate", "gh: API rate limit exceeded for user ID 1. (HTTP 403)", ErrRateLimited},
		{"secondary", "gh: You have exceeded a secondary rate limit (HTTP 429)", ErrRateLimited},
	}
	for _, c := range cases {
		if got := classify(c.stderr, []string{"api", "user"}); !errors.Is(got, c.want) {
			t.Errorf("%s: classify = %v, want %v", c.name, got, c.want)
		}
	}

	// A 403 that is not a rate limit is a permission problem, and telling
	// the poller to back off would be wrong.
	got := classify("gh: Resource not accessible by integration (HTTP 403)", []string{"api", "user"})
	for _, sentinel := range []error{ErrRateLimited, ErrNoAuth, ErrOffline, ErrNotFound} {
		if errors.Is(got, sentinel) {
			t.Errorf("plain 403 classified as %v", sentinel)
		}
	}
	if got == nil {
		t.Error("plain 403 classified as success")
	}
}

func TestChecksRollup(t *testing.T) {
	rollup := func(js string) Checks {
		var g ghPR
		if err := json.Unmarshal([]byte(`{"statusCheckRollup":`+js+`}`), &g); err != nil {
			t.Fatal(err)
		}
		return g.checks(nil)
	}

	cases := []struct {
		name string
		js   string
		want Checks
	}{
		{"no checks", `[]`, ChecksNone},
		{"all green", `[{"status":"COMPLETED","conclusion":"SUCCESS"}]`, ChecksPass},
		{"one red", `[{"status":"COMPLETED","conclusion":"SUCCESS"},
		               {"status":"COMPLETED","conclusion":"FAILURE"}]`, ChecksFail},
		{"still running", `[{"status":"IN_PROGRESS","conclusion":""}]`, ChecksPending},
		{"queued", `[{"status":"QUEUED","conclusion":""}]`, ChecksPending},
		// A failure outranks anything still running: the answer is already
		// known and the row should be red now, not in ten minutes.
		{"red beats pending", `[{"status":"IN_PROGRESS","conclusion":""},
		                        {"status":"COMPLETED","conclusion":"FAILURE"}]`, ChecksFail},
		{"skipped is fine", `[{"status":"COMPLETED","conclusion":"SKIPPED"},
		                      {"status":"COMPLETED","conclusion":"NEUTRAL"}]`, ChecksPass},
		{"timed out is red", `[{"status":"COMPLETED","conclusion":"TIMED_OUT"}]`, ChecksFail},
		// StatusContext nodes report state and have no status field at all.
		{"legacy status green", `[{"state":"SUCCESS"}]`, ChecksPass},
		{"legacy status red", `[{"state":"ERROR"}]`, ChecksFail},
		{"legacy status pending", `[{"state":"PENDING"}]`, ChecksPending},
	}
	for _, c := range cases {
		if got := rollup(c.js); got != c.want {
			t.Errorf("%s: checks = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPRMineAndBlocked(t *testing.T) {
	mine := PR{Author: "viminizer"}
	if !mine.Mine("viminizer") || mine.Mine("someone") || mine.Mine("") {
		t.Error("Mine by author wrong")
	}
	asked := PR{Author: "someone", Reviewers: []string{"Viminizer"}}
	if !asked.Mine("viminizer") {
		t.Error("Mine should match a review request, case-insensitively")
	}

	if (PR{}).Blocked() {
		t.Error("a clean PR is not blocked")
	}
	for _, p := range []PR{
		{Checks: ChecksFail},
		{Conflicts: true},
		{Review: "CHANGES_REQUESTED"},
	} {
		if !p.Blocked() {
			t.Errorf("%+v should be blocked", p)
		}
	}
	// Still running is not yet a problem.
	if (PR{Checks: ChecksPending}).Blocked() {
		t.Error("pending checks are not a blocker")
	}
}
