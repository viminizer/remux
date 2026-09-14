package push

import (
	"context"
	"testing"
	"time"

	"github.com/viminizer/remux/internal/agent"
	"github.com/viminizer/remux/internal/tmux"
)

func TestWatcherObservesSharedVerdicts(t *testing.T) {
	w := testWatcher(t)
	p := pane()
	tree := &tmux.Tree{Sessions: []*tmux.Session{{Windows: []*tmux.Window{{Panes: []*tmux.Pane{p}}}}}}
	w.Workspace = func(context.Context) *tmux.Tree { return tree }
	w.NotifyDone = func() bool { return true }
	ctx := context.Background()

	// No successful capture yet: do not establish a false baseline.
	w.tick(ctx)
	if len(w.last) != 0 {
		t.Fatal("missing capture became a status observation")
	}
	p.Status = string(agent.Waiting)
	w.tick(ctx)
	if len(w.lastSent) != 0 {
		t.Fatal("initial waiting baseline notified")
	}
	p.Status = string(agent.Busy)
	w.tick(ctx)
	// Failed tree refresh must leave the baseline intact.
	tree = nil
	w.tick(ctx)
	if w.last[p.ID] != agent.Busy {
		t.Fatal("failed refresh discarded the previous verdict")
	}
	tree = &tmux.Tree{Sessions: []*tmux.Session{{Windows: []*tmux.Window{{Panes: []*tmux.Pane{p}}}}}}
	p.Status = ""
	w.tick(ctx)
	if w.last[p.ID] != agent.Busy {
		t.Fatal("missing verdict overwrote the previous observation")
	}
	p.Status = string(agent.Idle)
	w.tick(ctx)
	if _, ok := w.lastSent[p.ID+":done"]; !ok {
		t.Fatal("busy -> idle did not notify after a failed observation")
	}
	p.Status = string(agent.Waiting)
	w.tick(ctx)
	first := w.lastSent[p.ID+":waiting"]
	if first.IsZero() {
		t.Fatal("idle -> waiting did not notify")
	}
	w.tick(ctx)
	p.Status = string(agent.Busy)
	w.tick(ctx)
	p.Status = string(agent.Waiting)
	w.tick(ctx)
	if w.lastSent[p.ID+":waiting"] != first {
		t.Fatal("quiet/repeated waiting bypassed cooldown")
	}
	// Shells stay outside the notification history even if a provider gives
	// one a non-shell verdict. A successful empty tree forgets gone panes.
	p.Command = "zsh"
	p.Status = string(agent.Busy)
	w.tick(ctx)
	if w.last[p.ID] != agent.Waiting {
		t.Fatal("shell became a notification observation")
	}
	tree = &tmux.Tree{}
	w.tick(ctx)
	if len(w.last) != 0 {
		t.Fatal("gone pane retained a previous status")
	}
}

func TestWatcherRequestsWorkspaceOnlyWhenNotificationsCanBeSent(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		subscribed, waiting, done bool
	}{
		{"no subscriptions", false, true, true},
		{"both disabled", true, false, false},
		{"waiting enabled", true, true, false},
		{"done enabled", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := testWatcher(t)
			if tc.subscribed {
				if err := w.Store.Add(Subscription{Endpoint: "https://example.invalid/push"}); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			w.Workspace = func(context.Context) *tmux.Tree {
				calls++
				return &tmux.Tree{}
			}
			w.NotifyWaiting = func() bool { return tc.waiting }
			w.NotifyDone = func() bool { return tc.done }
			w.Interval = time.Millisecond
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			w.Run(ctx)
			want := tc.subscribed && (tc.waiting || tc.done)
			if (calls > 0) != want {
				t.Errorf("workspace calls = %d; want polling = %v", calls, want)
			}
		})
	}
}
