package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/viminizer/remux/internal/config"
	"github.com/viminizer/remux/internal/push"
	"github.com/viminizer/remux/internal/tmux"
)

// A fake executable exercises the real tree/parser/capture path and counts
// subprocesses without connecting to the laptop's tmux server.
func fakeWorkspaceTmux(t *testing.T) (*tmux.Client, string, string) {
	t.Helper()
	dir := t.TempDir()
	// Previews are cached process-wide by pane ID; isolate this fake from
	// other tests and repeated runs in the same process.
	pane := fmt.Sprintf("%%%d", time.Now().UnixNano())
	files := map[string]string{
		"tmux": `#!/bin/sh
cd "$(dirname "$0")" || exit 1
echo "$1" >> calls
case "$1" in
list-panes) cat tree ;;
list-sessions) echo '$1|~|0|~|test' ;;
capture-pane) cat screen ;;
*) exit 1 ;;
esac
`,
		"tree":   strings.Join([]string{"$1", "test", "0", "@1", "0", "agent", "1", "1", pane, "0", "codex", "/tmp", "1", "80", "24", "0", "0", "0", "0", "", "", "", "", "agent"}, "|~|") + "\n",
		"screen": "Working (esc to interrupt)\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return &tmux.Client{Bin: filepath.Join(dir, "tmux")}, dir, pane
}

func workspaceCalls(t *testing.T, dir string, command string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), command+"\n")
}

func TestPushWatcherReusesSharedWorkspace(t *testing.T) {
	tm, dir, pane := fakeWorkspaceTmux(t)
	srv := NewServer(config.Default(), tm, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if tree := srv.workspace(ctx, time.Hour, true); tree == nil || tree.Pane(pane) == nil || tree.Pane(pane).Status != "busy" {
		t.Fatalf("could not seed a classified tree: %+v", tree)
	}
	// Past the preview cache TTL, the independent watcher gate would capture
	// this quiet pane again despite its already-published verdict.
	time.Sleep(1600 * time.Millisecond)
	store, err := push.OpenIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(push.Subscription{Endpoint: "https://example.invalid/push"}); err != nil {
		t.Fatal(err)
	}
	w := push.NewWatcher(func(ctx context.Context) *tmux.Tree {
		// Keep TTL-boundary scheduling from affecting this sharing check.
		return srv.ClassifiedWorkspace(ctx, time.Hour)
	}, store)
	w.Interval = 10 * time.Millisecond
	ticks := make(chan struct{}, 10)
	w.NotifyWaiting = func() bool {
		select {
		case ticks <- struct{}{}:
		default:
		}
		return true
	}
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	// Reaching the second tick means the first has completed its whole pass.
	for range 2 {
		select {
		case <-ticks:
		case <-time.After(5 * time.Second):
			t.Fatal("watcher never ticked")
		}
	}
	cancel()
	<-done
	for _, command := range []string{"list-panes", "capture-pane"} {
		if got := workspaceCalls(t, dir, command); got != 1 {
			t.Errorf("shared request + watcher ran %s %d times, want 1", command, got)
		}
	}
}

func TestClassifiedWorkspaceWithoutSocket(t *testing.T) {
	tm, dir, pane := fakeWorkspaceTmux(t)
	srv := NewServer(config.Default(), tm, nil)
	ctx := context.Background()
	plain := srv.Workspace(ctx, time.Hour)
	if plain == nil || plain.Pane(pane) == nil || plain.Pane(pane).Status != "" {
		t.Fatal("expected an unclassified background tree")
	}
	if got := workspaceCalls(t, dir, "capture-pane"); got != 0 {
		t.Fatalf("background tree captured %d panes with no status consumer", got)
	}
	// A push watcher and a boot request arriving together upgrade the plain
	// tree once. Neither needs a WebSocket to demand status classification.
	var wg sync.WaitGroup
	trees := make([]*tmux.Tree, 8)
	for i := range trees {
		wg.Go(func() { trees[i] = srv.ClassifiedWorkspace(ctx, time.Hour) })
	}
	wg.Wait()
	for _, tree := range trees {
		if tree == nil || tree.Pane(pane) == nil || tree.Pane(pane).Status != "busy" {
			t.Fatal("push received no verdict with no socket open")
		}
		if tree != trees[0] {
			t.Error("concurrent consumers got separate sweeps")
		}
	}
	if got := workspaceCalls(t, dir, "capture-pane"); got != 1 {
		t.Fatalf("status upgrade captured %d panes, want 1", got)
	}
	// Expire the publication without sleeping. The next background read is
	// deliberately unclassified, so push must upgrade it again; the shared
	// activity gate retains the quiet pane's previous verdict.
	srv.ws.pub.Lock()
	srv.ws.at = time.Now().Add(-time.Hour)
	srv.ws.pub.Unlock()
	if tree := srv.Workspace(ctx, time.Second); tree == nil || tree == plain {
		t.Fatal("expired background tree did not refresh")
	}
	quiet := srv.ClassifiedWorkspace(ctx, time.Second)
	if quiet == nil || quiet == trees[0] || quiet.Pane(pane).Status != "busy" {
		t.Fatal("push did not obtain a fresh classified tree")
	}
	if got := workspaceCalls(t, dir, "capture-pane"); got != 1 {
		t.Errorf("quiet refresh captured again: %d captures, want 1", got)
	}
	if srv.Watching() {
		t.Fatal("test accidentally used a WebSocket")
	}
}

func TestClassifiedWorkspaceRetriesMissingFirstCapture(t *testing.T) {
	tm, dir, pane := fakeWorkspaceTmux(t)
	srv := NewServer(config.Default(), tm, nil)
	ctx := context.Background()
	if err := os.Remove(filepath.Join(dir, "screen")); err != nil {
		t.Fatal(err)
	}
	tree := srv.ClassifiedWorkspace(ctx, time.Hour)
	if tree == nil || tree.Pane(pane) == nil || tree.Pane(pane).Status != "" {
		t.Fatal("failed first capture must leave status absent")
	}
	if err := os.WriteFile(filepath.Join(dir, "screen"), []byte("Working (esc to interrupt)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.ws.pub.Lock()
	srv.ws.at = time.Now().Add(-time.Hour)
	srv.ws.pub.Unlock()
	tree = srv.ClassifiedWorkspace(ctx, time.Second)
	if tree == nil || tree.Pane(pane).Status != "busy" {
		t.Fatal("failed first capture was not retried on a quiet pane")
	}
}
