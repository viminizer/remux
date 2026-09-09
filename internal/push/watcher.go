package push

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/viminizer/remux/internal/agent"
	"github.com/viminizer/remux/internal/tmux"
)

// Watcher polls every pane and fires a notification when one starts waiting.
//
// Two rules keep it quiet enough to live with:
//
//   - It only fires on a *transition* into waiting. A pane that has been
//     waiting for an hour is not news, and re-notifying it would train Kevin to
//     ignore the notification.
//   - It only runs while at least one subscription is registered, so the
//     idle-cost story holds when the phone is not using the app.
type Watcher struct {
	Tmux     *tmux.Client
	Store    *Store
	Interval time.Duration
	Cooldown time.Duration

	// NotifyDone also fires on busy -> idle, for long tasks. Off by default:
	// it is the chattier of the two signals.
	NotifyDone func() bool
	// NotifyWaiting gates the primary signal.
	NotifyWaiting func() bool

	mu       sync.Mutex
	last     map[string]agent.Status
	lastSent map[string]time.Time
}

func NewWatcher(tm *tmux.Client, store *Store) *Watcher {
	return &Watcher{
		Tmux:     tm,
		Store:    store,
		Interval: 5 * time.Second,
		Cooldown: 5 * time.Minute,
		last:     map[string]agent.Status{},
		lastSent: map[string]time.Time{},
	}
}

func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if w.Store.Count() == 0 {
				continue
			}
			w.tick(ctx)
		}
	}
}

// tick captures every pane in parallel (~40 ms for 26 panes) and compares each
// verdict with the previous one.
func (w *Watcher) tick(ctx context.Context) {
	tree, err := w.Tmux.Tree(ctx)
	if err != nil {
		return
	}
	panes := tree.Panes()
	ids := make([]string, 0, len(panes))
	for _, p := range panes {
		ids = append(ids, p.ID)
	}
	screens := w.Tmux.Previews(ctx, ids, 15)

	seen := map[string]bool{}
	for _, p := range panes {
		seen[p.ID] = true
		status := agent.Classify(p.Command, p.Title, screens[p.ID])
		w.transition(p, status)
	}

	// Forget panes that no longer exist, so a recycled pane id does not
	// inherit a stale previous state.
	w.mu.Lock()
	for id := range w.last {
		if !seen[id] {
			delete(w.last, id)
			delete(w.lastSent, id)
		}
	}
	w.mu.Unlock()
}

// transition decides whether this status change is worth a notification.
func (w *Watcher) transition(p *tmux.Pane, now agent.Status) {
	w.mu.Lock()
	prev, had := w.last[p.ID]
	w.last[p.ID] = now
	w.mu.Unlock()

	if !had || prev == now {
		return
	}

	switch {
	case now == agent.Waiting && enabled(w.NotifyWaiting, true):
		w.fire(p, body(p.Command, "needs an answer"), "waiting")
	case now == agent.Idle && prev == agent.Busy && enabled(w.NotifyDone, false):
		w.fire(p, body(p.Command, "finished"), "done")
	}
}

// body is the single line a lock screen shows.
//
// The name comes from agent.DisplayCommand, which holds the one rule that
// matters here: Claude Code reports its version number as
// pane_current_command, so the raw value is "2.1.265". This file used to carry
// its own copy of that rule, and the copy had drifted - it named codex, claude
// and aider, then fell everything else through to "claude", so an opencode or
// crush pane announced itself as claude on the one surface where you cannot
// check.
func body(cmd, what string) string {
	return agent.DisplayCommand(cmd) + " " + what
}

func (w *Watcher) fire(p *tmux.Pane, body, kind string) {
	// Suppressed if the phone already has this pane open - it is looking at
	// the answer prompt right now.
	if w.Store.Focus() == p.ID {
		return
	}

	key := p.ID + ":" + kind
	w.mu.Lock()
	if t, ok := w.lastSent[key]; ok && time.Since(t) < w.Cooldown {
		w.mu.Unlock()
		return
	}
	w.lastSent[key] = time.Now()
	w.mu.Unlock()

	err := w.Store.Send(Payload{
		Title: fmt.Sprintf("%s / %s", p.SessionName, p.WindowName),
		Body:  body,
		Pane:  p.ID,
		Tag:   key,
	})
	if err != nil {
		log.Printf("push: %v", err)
	}
}

func enabled(f func() bool, def bool) bool {
	if f == nil {
		return def
	}
	return f()
}

// CheckTransition exposes the transition rule for tests, which need to drive
// it without a tmux server. It reports whether the change would notify.
func (w *Watcher) CheckTransition(p *tmux.Pane, from, to agent.Status) bool {
	w.mu.Lock()
	w.last[p.ID] = from
	delete(w.lastSent, p.ID+":waiting")
	delete(w.lastSent, p.ID+":done")
	w.mu.Unlock()

	w.transition(p, to)

	w.mu.Lock()
	defer w.mu.Unlock()
	_, waiting := w.lastSent[p.ID+":waiting"]
	_, done := w.lastSent[p.ID+":done"]
	return waiting || done
}
