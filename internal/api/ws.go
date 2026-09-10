package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/viminizer/remux/internal/agent"
	"github.com/viminizer/remux/internal/tmux"
)

// The WebSocket carries both the workspace tree and the focused pane's screen,
// so the phone needs exactly one connection.
//
// Polling is server-side and per connection. The phone never polls: it sends
// "unsub" when the screen goes off and the poller stops entirely, which is the
// whole battery story.

type wsIn struct {
	T     string `json:"t"`
	Pane  string `json:"pane,omitempty"`
	Lines int    `json:"lines,omitempty"`
}

type snapMeta struct {
	Cmd    string `json:"cmd"`
	Title  string `json:"title"`
	W      int    `json:"w"`
	H      int    `json:"h"`
	InMode bool   `json:"inMode"`
	Alt    bool   `json:"alt"`
	Status string `json:"status"`
}

type conn struct {
	ws *websocket.Conn
	mu sync.Mutex // one writer at a time
}

func (c *conn) send(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageText, b)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	// The socket is same-origin: the UI is served by this very binary, so
	// there is no cross-origin case to allow.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: false,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		log.Printf("ws accept: %v", err)
		return
	}
	defer ws.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	c := &conn{ws: ws}
	p := &poller{
		srv:    s,
		conn:   c,
		lines:  s.Cfg.Lines,
		gate:   tmux.NewActivityGate(),
		status: map[string]string{},
	}

	go p.loop(ctx)

	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var msg wsIn
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.T {
		case "sub":
			if tmux.ValidPaneID(msg.Pane) {
				lines := s.Cfg.Lines
				if msg.Lines > 0 {
					lines = clamp(msg.Lines, 20, 5000)
				}
				p.subscribe(msg.Pane, lines)
			}
		case "unsub":
			p.subscribe("", 0)
		case "resume":
			p.forceNext()
		case "focus":
			// The phone tells us which pane it is looking at so the push
			// watcher does not notify about a pane already on screen.
			p.setFocus(msg.Pane)
		case "ping":
			_ = c.send(ctx, map[string]any{"t": "pong"})
		}
	}
}

// poller owns all polling for one connection.
type poller struct {
	srv   *Server
	conn  *conn
	mu    sync.Mutex
	pane  string
	lines int
	force bool

	paneHash [32]byte
	treeHash [32]byte
	hasPane  bool
	hasTree  bool

	gate   *tmux.ActivityGate
	status map[string]string // last verdict per pane, for the ones not recaptured

	// ghSummary is the last GitHub badge line sent, so an unchanged one
	// costs nothing.
	ghSummary ghSummary
	hasGH     bool

	// tree is the last workspace tree pollTree fetched, reused by pollPane
	// for the subscribed pane's metadata. See paneMeta.
	tree *tmux.Tree
}

func (p *poller) subscribe(pane string, lines int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pane = pane
	if lines > 0 {
		p.lines = lines
	}
	// A fresh subscription must always deliver one snapshot, even if the
	// screen happens to hash the same as the previous pane's.
	p.hasPane = false
	p.force = true
}

func (p *poller) forceNext() {
	p.mu.Lock()
	p.force, p.hasPane, p.hasTree = true, false, false
	p.mu.Unlock()
	// "resume" means the phone was away and does not trust what it has. The
	// gate would otherwise skip every quiet window and hand back verdicts from
	// before the screen went off.
	p.gate.Forget()
}

// paneMeta returns the tree entry for one pane, reusing the tree pollTree
// already fetched instead of asking tmux for a second one.
//
// pollPane runs every 400 ms while a pane is changing, and it used to call
// Tree() on every one of those ticks. Measured on this laptop that call is
// 9.2 ms against 27 panes - 2.3% of a core on its own, next to the 2.8% the
// capture costs - so roughly 40% of what remux burns while you watch a working
// agent went on re-reading a command name and a width that had not changed.
//
// The trade is that Title can now be up to one tree poll (2 s) old, and Title
// is the one field here that is not slow-moving: Claude Code rewrites
// pane_title every render. Two things make that acceptable. The drawer already
// shows titles from this very tree, so the top bar now agrees with it instead
// of running ahead; and agent.Classify ignores its title argument entirely, so
// no verdict depends on the fresher value.
//
// A pane the cached tree has never seen is still fetched, so a pane created
// since the last tree poll is never blank. That is the only case that pays.
func (p *poller) paneMeta(ctx context.Context, id string) *tmux.Pane {
	p.mu.Lock()
	cached := p.tree
	p.mu.Unlock()

	if cached != nil {
		if pn := cached.Pane(id); pn != nil {
			return pn
		}
	}

	tree, err := p.srv.Tmux.Tree(ctx)
	if err != nil {
		return nil
	}
	p.mu.Lock()
	p.tree = tree
	p.mu.Unlock()
	return tree.Pane(id)
}

func (p *poller) setFocus(pane string) {
	if p.srv.Push != nil {
		p.srv.Push.SetFocus(pane)
	}
}

func (p *poller) loop(ctx context.Context) {
	paneTick := time.NewTicker(p.srv.Cfg.Poll())
	treeTick := time.NewTicker(p.srv.Cfg.TreePoll())
	defer paneTick.Stop()
	defer treeTick.Stop()

	p.pollTree(ctx)
	p.pollGitHub(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-paneTick.C:
			p.pollPane(ctx)
		case <-treeTick.C:
			p.pollTree(ctx)
			p.pollGitHub(ctx)
		}
	}
}

// pollPane captures the subscribed pane and pushes only when the screen
// actually changed.
//
// This is the difference between a poller that is invisible and one that
// drains the phone's battery: in a browser both look identical, because both
// show current output. Hashing is what makes a quiet pane cost zero frames.
func (p *poller) pollPane(ctx context.Context) {
	p.mu.Lock()
	pane, lines, force := p.pane, p.lines, p.force
	p.force = false
	p.mu.Unlock()

	if pane == "" {
		return
	}

	text, err := p.srv.Tmux.Capture(ctx, pane, lines, true)
	if err != nil {
		// The pane went away while the phone was reading it. Say so
		// explicitly - the UI has a real screen for this.
		if strings.Contains(err.Error(), "can't find pane") ||
			strings.Contains(err.Error(), "no such pane") {
			_ = p.conn.send(ctx, map[string]any{"t": "gone", "pane": pane})
			p.mu.Lock()
			if p.pane == pane {
				p.pane = ""
			}
			p.mu.Unlock()
		}
		return
	}

	sum := sha256.Sum256([]byte(text))

	p.mu.Lock()
	// The capture ran outside the lock, so the phone may have unsubscribed
	// or moved to another pane while it was in flight. Sending anyway would
	// deliver a frame after the screen went off, which is exactly what
	// unsub exists to prevent.
	stale := p.pane != pane
	unchanged := p.hasPane && sum == p.paneHash && !force
	if !stale {
		p.paneHash, p.hasPane = sum, true
	}
	p.mu.Unlock()

	if stale || unchanged {
		return
	}

	meta := snapMeta{}
	if pn := p.paneMeta(ctx, pane); pn != nil {
		meta = snapMeta{
			Cmd: pn.Command, Title: pn.Title,
			W: pn.Width, H: pn.Height,
			InMode: pn.InMode, Alt: pn.Alt,
			Status: string(agent.Classify(pn.Command, pn.Title, text)),
		}
	}

	_ = p.conn.send(ctx, map[string]any{
		"t":     "snap",
		"pane":  pane,
		"rev":   time.Now().UnixMilli(),
		"lines": strings.Split(text, "\n"),
		"meta":  meta,
	})
}

// pollTree keeps the drawer live without the phone polling for it. Same
// hash-and-diff rule: an unchanged workspace sends nothing.
//
// Sending nothing used to cost as much as sending everything, because the
// status dots meant capturing all 27 panes every two seconds to find out. Most
// of those captures could not tell us anything new: a shell's verdict comes
// from its command, and a pane whose window has produced no output still has
// the verdict we gave it last time. So only the rest are captured, and the
// statuses we already knew are carried forward.
// ghSummary is the drawer badge: small enough to ride the tree tick, and all
// the phone needs until somebody actually opens the GitHub screen.
type ghSummary struct {
	T         string `json:"t"`
	Count     int    `json:"count"`    // things that need a person
	Assigned  int    `json:"assigned"` // issues with your name on them
	Red       int    `json:"red"`      // your pull requests that are blocked
	Repos     int    `json:"repos"`    // how many are watched
	At        int64  `json:"at"`       // when GitHub was last read, unix ms
	ErrorKind string `json:"errorKind,omitempty"`
}

// pollGitHub piggybacks on the tree tick.
//
// It reads the shared poller's cached snapshot, so this is a struct copy and
// never a request - the badge stays live without any connection adding load to
// the GitHub API. Nothing is sent unless the numbers actually changed.
func (p *poller) pollGitHub(ctx context.Context) {
	if p.srv.GH == nil {
		return
	}
	// Through ghBase, not the poller directly: a muted row must leave the
	// drawer badge at the same moment it leaves the list.
	snap := p.srv.ghBase()
	next := ghSummary{
		T:         "gh",
		Count:     snap.Inbox.Count(),
		Assigned:  len(snap.Inbox.Assigned),
		Repos:     len(snap.Repos),
		ErrorKind: snap.ErrorKind,
	}
	for _, it := range snap.Inbox.NeedsYou {
		if it.Checks == "fail" {
			next.Red++
		}
	}
	if !snap.At.IsZero() {
		next.At = snap.At.UnixMilli()
	}

	p.mu.Lock()
	same := p.hasGH && p.ghSummary == next
	p.ghSummary, p.hasGH = next, true
	p.mu.Unlock()
	if same {
		return
	}
	_ = p.conn.send(ctx, next)
}

func (p *poller) pollTree(ctx context.Context) {
	tree, err := p.srv.Tmux.Tree(ctx)
	if err != nil {
		return
	}
	p.mu.Lock()
	p.tree = tree
	p.mu.Unlock()

	panes := tree.Panes()
	live := make([]*tmux.Pane, 0, len(panes))
	for _, pn := range panes {
		if !agent.IsShell(pn.Command) {
			live = append(live, pn)
		}
	}
	stale := map[string]bool{}
	for _, id := range p.gate.Changed(live) {
		stale[id] = true
	}
	ids := make([]string, 0, len(stale))
	for _, pn := range live {
		if stale[pn.ID] {
			ids = append(ids, pn.ID)
		}
	}
	screens := p.srv.Tmux.Previews(ctx, ids, tmux.PreviewLines)

	p.mu.Lock()
	for _, pn := range panes {
		if agent.IsShell(pn.Command) || stale[pn.ID] {
			pn.Status = string(agent.Classify(pn.Command, pn.Title, screens[pn.ID]))
			p.status[pn.ID] = pn.Status
			continue
		}
		pn.Status = p.status[pn.ID]
	}
	for id := range p.status {
		if tree.Pane(id) == nil {
			delete(p.status, id)
		}
	}
	p.mu.Unlock()

	b, err := json.Marshal(tree)
	if err != nil {
		return
	}
	sum := sha256.Sum256(b)

	p.mu.Lock()
	unchanged := p.hasTree && sum == p.treeHash
	p.treeHash, p.hasTree = sum, true
	p.mu.Unlock()

	if unchanged {
		return
	}
	_ = p.conn.send(ctx, map[string]any{
		"t":        "tree",
		"rev":      time.Now().UnixMilli(),
		"sessions": tree.Sessions,
	})
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
