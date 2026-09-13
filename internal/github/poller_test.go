package github

import (
	"testing"
	"time"
)

func TestRetainDropsUnwatched(t *testing.T) {
	p := &Poller{}
	p.snap.Repos = []Repo{{Full: "viminizer/remux"}, {Full: "Apache/ShardingSphere"}}

	// Case matters nowhere else in the watchlist, so it must not matter here.
	p.Retain([]string{"apache/shardingsphere"})

	got := p.Snapshot().Repos
	if len(got) != 1 || got[0].Full != "Apache/ShardingSphere" {
		t.Fatalf("kept %v", got)
	}

	p.Retain(nil)
	if got := p.Snapshot().Repos; len(got) != 0 {
		t.Fatalf("empty watchlist kept %v", got)
	}
}

// The mentions come from a second request and are merged into an inbox that
// has already been deduped. A PR with a review requested from Kevin that he is
// also @-mentioned on is one row, not two - and one badge, not two.
func TestMentionsDoNotDuplicateInboxRows(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	in := Inbox{
		NeedsYou: []InboxItem{{Kind: KindPR, Repo: "viminizer/remux", Number: 7, Updated: t0}},
		YourPRs:  []InboxItem{{Kind: KindPR, Repo: "viminizer/remux", Number: 9, Updated: t0}},
	}

	// What the poller does with what Notifications returned.
	in.AddMentions([]InboxItem{
		{Kind: KindMention, Repo: "viminizer/remux", Number: 7, Updated: t0.Add(time.Hour)},
		{Kind: KindMention, Repo: "viminizer/remux", Number: 9, Updated: t0.Add(2 * time.Hour)},
		{Kind: KindMention, Repo: "viminizer/remux", Number: 11, Updated: t0.Add(3 * time.Hour)},
	})

	if in.Count() != 2 {
		t.Errorf("badge = %d, want 2: %+v", in.Count(), in.NeedsYou)
	}
	seen := map[int]int{}
	for _, it := range in.NeedsYou {
		seen[it.Number]++
	}
	if seen[7] != 1 {
		t.Errorf("#7 rendered %d times, want 1", seen[7])
	}
	if seen[9] != 0 {
		t.Errorf("#9 is already under Your PRs but got %d rows in Needs you", seen[9])
	}
	if seen[11] != 1 {
		t.Errorf("#11 is only a mention and must survive, got %d rows", seen[11])
	}
}
