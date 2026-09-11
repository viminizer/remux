package tmux

import (
	"testing"
	"time"
)

// The preview cache is bounded by the panes that are live, not by every pane
// the process has ever seen.
//
// remux runs as a LaunchAgent for weeks. put only ever inserted, so 40 lines of
// screen were kept for a pane that had been killed in the morning and would
// never be asked about again.
func TestPreviewCacheDropsWhatExpired(t *testing.T) {
	pc := &previewCache{ttl: 20 * time.Millisecond, m: map[string]previewEntry{}}

	pc.put("%1", "old")
	pc.put("%2", "old")
	if len(pc.m) != 2 {
		t.Fatalf("cache holds %d, want 2", len(pc.m))
	}

	time.Sleep(40 * time.Millisecond)
	pc.put("%3", "new")

	if len(pc.m) != 1 {
		t.Errorf("cache holds %d after the sweep, want 1: %v", len(pc.m), pc.m)
	}
	if _, ok := pc.get("%3"); !ok {
		t.Error("the entry just written is gone")
	}
	// An expired entry was already unreachable, so nothing was lost.
	if _, ok := pc.get("%1"); ok {
		t.Error("an expired entry came back")
	}
}
