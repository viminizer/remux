package push

import (
	"fmt"
	"testing"
	"time"

	gh "github.com/viminizer/remux/internal/github"
)

func snapshot(items ...gh.InboxItem) gh.Snapshot {
	return gh.Snapshot{At: time.Now(), Inbox: gh.Inbox{NeedsYou: items}}
}

func pr(number int, checks gh.Checks, review string, conflicts bool) gh.InboxItem {
	return gh.InboxItem{
		Kind: gh.KindPR, Repo: "viminizer/remux", Number: number,
		Title: "a change", Checks: checks, Review: review, Conflicts: conflicts,
	}
}

// fired names what decide would send, so the tests read as transitions.
func fired(w *GitHubWatcher, snap gh.Snapshot) []string {
	var out []string
	for _, n := range w.decide(snap) {
		out = append(out, fmt.Sprintf("%s#%d %s", n.item.Repo, n.item.Number, n.state))
	}
	return out
}

// The first snapshot after a restart is a baseline. Without that rule,
// restarting the service announces every red check Kevin already knew about.
func TestGitHubWatcherBaseline(t *testing.T) {
	w := NewGitHubWatcher(nil)
	if got := fired(w, snapshot(pr(1, gh.ChecksFail, "", false))); len(got) != 0 {
		t.Errorf("first snapshot fired %v, want nothing", got)
	}
	// Unchanged on the next read is not news either.
	if got := fired(w, snapshot(pr(1, gh.ChecksFail, "", false))); len(got) != 0 {
		t.Errorf("unchanged snapshot fired %v", got)
	}
}

func TestGitHubWatcherTransitions(t *testing.T) {
	w := NewGitHubWatcher(nil)
	fired(w, snapshot()) // baseline, empty

	got := fired(w, snapshot(pr(1, gh.ChecksFail, "", false)))
	if len(got) != 1 || got[0] != "viminizer/remux#1 checks failed" {
		t.Fatalf("new red PR fired %v", got)
	}

	// Same state again: silent.
	if got := fired(w, snapshot(pr(1, gh.ChecksFail, "", false))); len(got) != 0 {
		t.Errorf("repeat fired %v", got)
	}

	// A different problem on the same PR is a real change and is worth
	// saying again.
	got = fired(w, snapshot(pr(1, gh.ChecksPass, "", true)))
	if len(got) != 1 || got[0] != "viminizer/remux#1 has conflicts" {
		t.Errorf("conflict fired %v", got)
	}

	// It goes green and leaves the list, then comes back red. The second
	// failure has to be announced, which is what forgetting departed items
	// is for.
	fired(w, snapshot())
	got = fired(w, snapshot(pr(1, gh.ChecksFail, "", false)))
	if len(got) != 1 {
		t.Errorf("red again fired %v, want one", got)
	}
}

// A failed poll carries the previous data forward. Treating that as fresh
// would re-announce everything the moment GitHub came back.
func TestGitHubWatcherIgnoresStale(t *testing.T) {
	w := NewGitHubWatcher(nil)
	w.Store = nil
	stale := snapshot(pr(1, gh.ChecksFail, "", false))
	stale.ErrorKind = "offline"
	w.OnSnapshot(stale) // must not panic, must not record a baseline
	if w.first {
		t.Error("a failed poll was taken as a baseline")
	}
}

func TestGitHubReason(t *testing.T) {
	cases := []struct {
		name string
		item gh.InboxItem
		want string
	}{
		{"red", pr(1, gh.ChecksFail, "", false), "checks failed"},
		{"conflict", pr(1, gh.ChecksPass, "", true), "has conflicts"},
		{"changes", pr(1, gh.ChecksPass, "CHANGES_REQUESTED", false), "changes requested"},
		{"review", pr(1, gh.ChecksPass, "REVIEW_REQUIRED", false), "wants your review"},
		{"mention", gh.InboxItem{Kind: gh.KindMention}, "mentioned you"},
		// An assigned issue reaches Needs you through nothing urgent, and
		// notifying about it would make the whole feature noise.
		{"plain issue", gh.InboxItem{Kind: gh.KindIssue}, ""},
	}
	for _, c := range cases {
		if got := reason(c.item); got != c.want {
			t.Errorf("%s: reason = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestGitHubRoute(t *testing.T) {
	cases := []struct {
		name string
		item gh.InboxItem
		want string
	}{
		{"issue", gh.InboxItem{Kind: gh.KindIssue}, "i"},
		{"pr", gh.InboxItem{Kind: gh.KindPR}, "pr"},
		// A mention carries no kind of its own, so the URL decides.
		{"mention on a pr", gh.InboxItem{
			Kind: gh.KindMention, URL: "https://github.com/o/n/pull/12"}, "pr"},
		{"mention on an issue", gh.InboxItem{
			Kind: gh.KindMention, URL: "https://github.com/o/n/issues/12"}, "i"},
	}
	for _, c := range cases {
		if got := itemPath(c.item); got != c.want {
			t.Errorf("%s: itemPath = %q, want %q", c.name, got, c.want)
		}
	}
}
