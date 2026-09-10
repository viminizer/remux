package github

import "testing"

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
