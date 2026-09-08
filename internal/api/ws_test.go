package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/viminizer/remux/internal/config"
	"github.com/viminizer/remux/internal/tmux"
)

// scratchSession is the only session these tests are allowed to write to.
// Everything else on this machine is real work.
const scratchSession = "remux-test"

// scratchPane gives the test its own throwaway pane inside the scratch
// session, and kills it afterwards.
//
// A fresh window per test, not a shared one: Go runs packages' tests in
// parallel, and sharing a single pane made two suites type into each other.
// The guard refuses anything outside the scratch session before a key is sent,
// because every other pane on this machine is real work.
func scratchPane(t *testing.T) (*tmux.Client, string) {
	t.Helper()
	tm := tmux.New()
	ctx := context.Background()

	tree, err := tm.Tree(ctx)
	if err != nil {
		t.Skipf("no tmux: %v", err)
	}

	var sessionID string
	for _, s := range tree.Sessions {
		if s.Name == scratchSession {
			sessionID = s.ID
		}
	}
	if sessionID == "" {
		t.Skipf("no %s session; create it with: tmux new-session -d -s %s",
			scratchSession, scratchSession)
	}

	winID, err := tm.NewWindow(ctx, sessionID, "ws"+strconv.FormatInt(time.Now().UnixNano()%1e6, 10), "")
	if err != nil {
		t.Fatalf("create scratch window: %v", err)
	}
	t.Cleanup(func() { _ = tm.KillWindow(context.Background(), winID) })

	tree, err = tm.Tree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range tree.Panes() {
		if p.WindowID != winID {
			continue
		}
		sess, err := tm.SessionOf(ctx, p.ID)
		if err != nil || sess != scratchSession {
			t.Fatalf("pane %s is not in %s (got %q) - refusing to write", p.ID, scratchSession, sess)
		}
		time.Sleep(600 * time.Millisecond) // let the shell settle before hashing
		return tm, p.ID
	}
	t.Fatalf("no pane in the window just created")
	return nil, ""
}

func testServer(t *testing.T, tm *tmux.Client) *httptest.Server {
	t.Helper()
	cfg := config.Default()
	cfg.PollMS = 200
	cfg.TreeMS = 60000 // keep tree traffic out of the pane counts
	srv := NewServer(cfg, tm, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func dial(t *testing.T, ts *httptest.Server) (*websocket.Conn, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	return ws, ctx
}

func send(t *testing.T, ws *websocket.Conn, ctx context.Context, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := ws.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("write: %v", err)
	}
}

type frame struct {
	T     string   `json:"t"`
	Pane  string   `json:"pane"`
	Lines []string `json:"lines"`
}

// reader pumps frames into a channel on a long-lived context.
//
// Reading with a per-call deadline does not work here: coder/websocket closes
// the connection when the context passed to Read is cancelled, so a timed-out
// read would tear down the socket the next assertion needs.
type reader struct {
	ch chan frame
}

func newReader(t *testing.T, ws *websocket.Conn, ctx context.Context) *reader {
	t.Helper()
	r := &reader{ch: make(chan frame, 256)}
	go func() {
		defer close(r.ch)
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var f frame
			if json.Unmarshal(data, &f) == nil {
				select {
				case r.ch <- f:
				default:
				}
			}
		}
	}()
	return r
}

// take collects frames of one type for a fixed window.
func (r *reader) take(want string, d time.Duration) []frame {
	deadline := time.After(d)
	var got []frame
	for {
		select {
		case f, ok := <-r.ch:
			if !ok {
				return got
			}
			if f.T == want {
				got = append(got, f)
			}
		case <-deadline:
			return got
		}
	}
}

// TestSnapOnlyOnChange is the phase 2 exit test, and the one that actually
// matters for battery life.
//
// A poller that pushes on every tick looks identical to this one in a browser:
// both show current output. The difference only shows up as heat in a pocket.
// So the quiet case is asserted explicitly, not assumed.
func TestSnapOnlyOnChange(t *testing.T) {
	tm, pane := scratchPane(t)
	ts := testServer(t, tm)
	ws, ctx := dial(t, ts)
	rd := newReader(t, ws, ctx)

	send(t, ws, ctx, map[string]any{"t": "sub", "pane": pane, "lines": 100})

	// A fresh subscription must always deliver exactly one snapshot.
	first := rd.take("snap", 2*time.Second)
	if len(first) != 1 {
		t.Fatalf("on subscribe: got %d snaps, want exactly 1", len(first))
	}
	if first[0].Pane != pane {
		t.Fatalf("snap for %q, want %q", first[0].Pane, pane)
	}

	// Quiet pane: the poller ticks five times per second here, so anything
	// above zero means it is pushing unchanged screens.
	quiet := rd.take("snap", 3*time.Second)
	if len(quiet) != 0 {
		t.Errorf("pane was quiet but got %d snaps - the hash check is not working", len(quiet))
	}

	// Now change the screen.
	marker := "remux-ws-probe-" + time.Now().Format("150405")
	if err := tm.SendText(context.Background(), pane, "echo "+marker, true); err != nil {
		t.Fatalf("send: %v", err)
	}

	changed := rd.take("snap", 3*time.Second)
	if len(changed) == 0 {
		t.Fatal("pane changed but no snap arrived")
	}
	found := false
	for _, f := range changed {
		if strings.Contains(strings.Join(f.Lines, "\n"), marker) {
			found = true
		}
	}
	if !found {
		t.Errorf("no snap carried the echoed marker %q", marker)
	}

	// And it must go quiet again once the echo has settled.
	time.Sleep(1 * time.Second)
	after := rd.take("snap", 2*time.Second)
	if len(after) != 0 {
		t.Errorf("got %d snaps after the pane settled, want 0", len(after))
	}
}

// TestUnsubStopsPolling covers the visibilitychange path: when the phone's
// screen goes off it sends unsub, and polling must stop entirely.
func TestUnsubStopsPolling(t *testing.T) {
	tm, pane := scratchPane(t)
	ts := testServer(t, tm)
	ws, ctx := dial(t, ts)
	rd := newReader(t, ws, ctx)

	send(t, ws, ctx, map[string]any{"t": "sub", "pane": pane})
	rd.take("snap", 2*time.Second)

	send(t, ws, ctx, map[string]any{"t": "unsub"})
	time.Sleep(300 * time.Millisecond)

	marker := "remux-unsub-" + time.Now().Format("150405")
	if err := tm.SendText(context.Background(), pane, "echo "+marker, true); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := rd.take("snap", 2*time.Second); len(got) != 0 {
		t.Errorf("got %d snaps after unsub, want 0", len(got))
	}
}
