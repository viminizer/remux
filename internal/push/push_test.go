package push

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/viminizer/remux/internal/agent"
	"github.com/viminizer/remux/internal/tmux"
)

func TestVAPIDKeysGenerateAndPersist(t *testing.T) {
	dir := t.TempDir()

	s, err := OpenIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	pub := s.PublicKey()
	if pub == "" {
		t.Fatal("no public key generated")
	}

	// The private key is a real credential; it must not be world-readable.
	info, err := os.Stat(filepath.Join(dir, "vapid.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("vapid.json is %o, want 600", perm)
	}

	// A second open must reuse the keypair, or every restart would silently
	// invalidate every subscription the phone has.
	again, err := OpenIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.PublicKey() != pub {
		t.Error("public key changed across restarts")
	}
}

func TestSubscriptionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenIn(dir)
	if err != nil {
		t.Fatal(err)
	}

	raw := `{"endpoint":"https://fcm.googleapis.com/fcm/send/abc","keys":{"p256dh":"BPk","auth":"xyz"}}`
	var sub Subscription
	if err := json.Unmarshal([]byte(raw), &sub); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(sub); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 1 {
		t.Fatalf("count = %d, want 1", s.Count())
	}

	// Re-subscribing the same endpoint must replace, not duplicate, or
	// reopening the app would notify twice.
	if err := s.Add(sub); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 1 {
		t.Errorf("count = %d after re-adding the same endpoint, want 1", s.Count())
	}

	// It has to survive a restart.
	reopened, err := OpenIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Count() != 1 {
		t.Errorf("count = %d after reopen, want 1", reopened.Count())
	}

	if err := reopened.Remove(sub.Endpoint); err != nil {
		t.Fatal(err)
	}
	if reopened.Count() != 0 {
		t.Errorf("count = %d after remove, want 0", reopened.Count())
	}
}

func testWatcher(t *testing.T) *Watcher {
	t.Helper()
	s, err := OpenIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewWatcher(tmux.New(), s)
}

func pane() *tmux.Pane {
	return &tmux.Pane{ID: "%99", Command: "codex", SessionName: "saas", WindowName: "api"}
}

// TestFiresOnTransitionIntoWaiting is the phase 8 exit test for the watcher.
func TestFiresOnTransitionIntoWaiting(t *testing.T) {
	w := testWatcher(t)
	if !w.CheckTransition(pane(), agent.Busy, agent.Waiting) {
		t.Error("busy -> waiting did not fire")
	}
	if !w.CheckTransition(pane(), agent.Idle, agent.Waiting) {
		t.Error("idle -> waiting did not fire")
	}
}

// A pane that is already waiting is not news. Re-notifying it every five
// seconds would train anyone to ignore the notification entirely.
func TestDoesNotFireWhenAlreadyWaiting(t *testing.T) {
	w := testWatcher(t)
	if w.CheckTransition(pane(), agent.Waiting, agent.Waiting) {
		t.Error("waiting -> waiting fired")
	}
}

func TestDoesNotFireOnUninterestingTransitions(t *testing.T) {
	w := testWatcher(t)
	for _, c := range []struct{ from, to agent.Status }{
		{agent.Idle, agent.Busy},
		{agent.Busy, agent.Busy},
		{agent.Shell, agent.Idle},
		{agent.Waiting, agent.Busy},
	} {
		if w.CheckTransition(pane(), c.from, c.to) {
			t.Errorf("%s -> %s fired", c.from, c.to)
		}
	}
}

// busy -> idle is the "your long task finished" signal, and it is off by
// default because it is the chattier of the two.
func TestDoneNotificationIsOffByDefault(t *testing.T) {
	w := testWatcher(t)
	if w.CheckTransition(pane(), agent.Busy, agent.Idle) {
		t.Error("busy -> idle fired with the default (off) setting")
	}

	w2 := testWatcher(t)
	w2.NotifyDone = func() bool { return true }
	if !w2.CheckTransition(pane(), agent.Busy, agent.Idle) {
		t.Error("busy -> idle did not fire when enabled")
	}
}

func TestWaitingCanBeDisabled(t *testing.T) {
	w := testWatcher(t)
	w.NotifyWaiting = func() bool { return false }
	if w.CheckTransition(pane(), agent.Idle, agent.Waiting) {
		t.Error("fired while waiting notifications were disabled")
	}
}

// Suppressed when the phone already has that pane open - Kevin is looking at
// the question right now.
func TestSuppressedWhenPaneIsOnScreen(t *testing.T) {
	w := testWatcher(t)
	w.Store.SetFocus("%99")
	if w.CheckTransition(pane(), agent.Idle, agent.Waiting) {
		t.Error("fired for the pane the phone currently has open")
	}
	w.Store.SetFocus("%1")
	if !w.CheckTransition(pane(), agent.Idle, agent.Waiting) {
		t.Error("did not fire when a different pane was on screen")
	}
}

// The payload names the pane but never quotes it: this is the one part of
// remux that leaves the tailnet, passing through the browser's push service.
func TestPayloadCarriesNoPaneContent(t *testing.T) {
	p := Payload{
		Title: "saas / api",
		Body:  "codex needs an answer",
		Pane:  "%99",
		Tag:   "%99:waiting",
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"lines", "screen", "preview", "text"} {
		if _, ok := got[k]; ok {
			t.Errorf("payload carries %q", k)
		}
	}
	if len(got) != 4 {
		t.Errorf("payload has %d fields, want 4: %v", len(got), got)
	}
}

// Every agent the watcher follows has to be called by its own name. The old
// label() here recognised five agents and named three, so opencode and crush
// were announced as "claude" - wrong on the one surface Kevin cannot check
// against the screen.
func TestNotificationNamesTheRightAgent(t *testing.T) {
	for cmd, want := range map[string]string{
		"codex":    "codex needs an answer",
		"claude":   "claude needs an answer",
		"aider":    "aider needs an answer",
		"opencode": "opencode needs an answer",
		"crush":    "crush needs an answer",
		"2.1.263":  "claude needs an answer", // Claude Code reports its version
		"zsh":      "zsh needs an answer",
	} {
		if got := body(cmd, "needs an answer"); got != want {
			t.Errorf("%s: got %q, want %q", cmd, got, want)
		}
	}
}
