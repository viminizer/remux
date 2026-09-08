package tmux

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Pane is one tmux pane: the unit the phone UI treats as a "conversation".
type Pane struct {
	ID      string `json:"id"`      // %14
	Index   int    `json:"index"`   // pane index within the window
	Title   string `json:"title"`   // pane_title - Claude Code puts the live task here
	Command string `json:"command"` // pane_current_command
	Path    string `json:"path"`    // pane_current_path
	Active  bool   `json:"active"`  // active pane in its window
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	InMode  bool   `json:"inMode"` // copy/view mode
	Alt     bool   `json:"alt"`    // alternate screen (vim, htop): no scrollback
	Dead    bool   `json:"dead"`
	History int    `json:"history"` // history_size

	// Filled in by the API layer, not by tmux.
	Status  string `json:"status,omitempty"`  // agent status verdict
	Preview string `json:"preview,omitempty"` // last N lines, plain text

	// Denormalised so the flat drawer list needs no lookups.
	SessionID   string `json:"sessionId"`
	SessionName string `json:"sessionName"`
	WindowID    string `json:"windowId"`
	WindowIndex int    `json:"windowIndex"`
	WindowName  string `json:"windowName"`
}

type Window struct {
	ID     string  `json:"id"`
	Index  int     `json:"index"`
	Name   string  `json:"name"`
	Active bool    `json:"active"`
	Panes  []*Pane `json:"panes"`
}

type Session struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Attached bool      `json:"attached"`
	Windows  []*Window `json:"windows"`
}

type Tree struct {
	Sessions []*Session `json:"sessions"`
}

// Panes flattens the tree in tmux order.
func (t *Tree) Panes() []*Pane {
	var out []*Pane
	for _, s := range t.Sessions {
		for _, w := range s.Windows {
			out = append(out, w.Panes...)
		}
	}
	return out
}

// Pane finds a pane by id.
func (t *Tree) Pane(id string) *Pane {
	for _, p := range t.Panes() {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// fieldSep separates the fields of one -F record.
//
// It is deliberately printable, and that is not a style choice. tmux passes
// format output through its vis-escaper, and what survives depends on whether
// the tmux command has a controlling terminal:
//
//	separator      from a terminal   under launchd
//	\t (tab)       kept              becomes "_"
//	\x1f, \x1e     "\037" literal    "\037" literal
//	| or |~|       kept              kept
//
// A tab therefore works perfectly in every interactive test and silently
// collapses every record into a single field when remux runs as a LaunchAgent,
// which is how it actually ships. A plain "|" is no good either: Claude Code's
// own status line reads "Opus 5 (1M context) | remux | ...", so it occurs in
// real pane titles.
const fieldSep = "|~|"

// treeFormat pulls session, window and pane fields in a single list-panes -a
// call, so the whole hierarchy costs one exec (~10 ms) instead of one per
// level. Field order must match parseTreeLine.
//
// pane_title is last on purpose. It is the wildest field - agents write
// arbitrary text into it - so parsing splits at most treeFields-1 times and
// lets anything in the title, separator included, survive verbatim.
var treeFormat = strings.Join([]string{
	"#{session_id}", "#{session_name}", "#{session_attached}",
	"#{window_id}", "#{window_index}", "#{window_name}", "#{window_active}",
	"#{pane_id}", "#{pane_index}", "#{pane_current_command}",
	"#{pane_current_path}", "#{pane_active}", "#{pane_width}", "#{pane_height}",
	"#{pane_in_mode}", "#{alternate_on}", "#{pane_dead}", "#{history_size}",
	"#{pane_title}",
}, fieldSep)

const treeFields = 19

var sessionFormat = strings.Join([]string{
	"#{session_id}", "#{session_attached}", "#{session_name}",
}, fieldSep)

// Tree returns the full workspace hierarchy.
//
// "no server running" is not an error here: it means an empty workspace, and
// the UI has a real empty state for it.
func (c *Client) Tree(ctx context.Context) (*Tree, error) {
	out, err := c.run(ctx, "list-panes", "-a", "-F", treeFormat)
	if err != nil {
		if err == ErrNoServer {
			return &Tree{Sessions: []*Session{}}, nil
		}
		return nil, err
	}

	t := &Tree{Sessions: []*Session{}}
	sessions := map[string]*Session{}
	windows := map[string]*Window{}

	seen, parsed := 0, 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		seen++
		s, w, p, ok := parseTreeLine(line)
		if !ok {
			continue
		}
		parsed++

		sess := sessions[s.ID]
		if sess == nil {
			sess = s
			sessions[s.ID] = sess
			t.Sessions = append(t.Sessions, sess)
		}

		win := windows[w.ID]
		if win == nil {
			win = w
			windows[w.ID] = win
			sess.Windows = append(sess.Windows, win)
		}

		p.SessionID, p.SessionName = sess.ID, sess.Name
		p.WindowID, p.WindowIndex, p.WindowName = win.ID, win.Index, win.Name
		win.Panes = append(win.Panes, p)
	}

	// tmux printed records but none of them parsed. That is a broken format
	// contract, not an empty workspace, and saying "no tmux server running"
	// here would be a confident wrong answer - which is exactly how the tab
	// separator hid for a whole build.
	if seen > 0 && parsed == 0 {
		return nil, fmt.Errorf(
			"tmux returned %d panes but none could be parsed; the field separator %q "+
				"did not survive tmux's output escaping", seen, fieldSep)
	}

	// A session with no windows cannot appear in list-panes output, so
	// backfill empty sessions from list-sessions.
	if err := c.backfillEmpty(ctx, t, sessions); err != nil {
		return nil, err
	}
	return t, nil
}

func parseTreeLine(line string) (*Session, *Window, *Pane, bool) {
	// SplitN, so a separator inside pane_title - the trailing field - stays
	// part of the title instead of shifting every column.
	f := strings.SplitN(line, fieldSep, treeFields)
	if len(f) < treeFields {
		return nil, nil, nil, false
	}
	s := &Session{ID: f[0], Name: f[1], Attached: f[2] == "1", Windows: []*Window{}}
	w := &Window{ID: f[3], Index: atoi(f[4]), Name: f[5], Active: f[6] == "1", Panes: []*Pane{}}
	p := &Pane{
		ID:      f[7],
		Index:   atoi(f[8]),
		Command: f[9],
		Path:    f[10],
		Active:  f[11] == "1",
		Width:   atoi(f[12]),
		Height:  atoi(f[13]),
		InMode:  f[14] == "1",
		Alt:     f[15] == "1",
		Dead:    f[16] == "1",
		History: atoi(f[17]),
		Title:   f[18],
	}
	return s, w, p, true
}

func (c *Client) backfillEmpty(ctx context.Context, t *Tree, seen map[string]*Session) error {
	out, err := c.run(ctx, "list-sessions", "-F", sessionFormat)
	if err != nil {
		if err == ErrNoServer {
			return nil
		}
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		// Session name is last here for the same reason pane_title is in
		// treeFormat: it is the only free-form field.
		f := strings.SplitN(strings.TrimSpace(line), fieldSep, 3)
		if len(f) < 3 || f[0] == "" || seen[f[0]] != nil {
			continue
		}
		s := &Session{ID: f[0], Attached: f[1] == "1", Name: f[2], Windows: []*Window{}}
		seen[s.ID] = s
		t.Sessions = append(t.Sessions, s)
	}
	return nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// ── capture ───────────────────────────────────────────────────────────────

// Capture returns the rendered screen of a pane.
//
// escapes=true keeps the SGR sequences (-e) so the phone can render the real
// colours; the preview path strips them because it only needs text.
func (c *Client) Capture(ctx context.Context, paneID string, lines int, escapes bool) (string, error) {
	if err := CheckPaneID(paneID); err != nil {
		return "", err
	}
	if lines <= 0 {
		lines = 400
	}
	args := []string{"capture-pane", "-p", "-t", paneID, "-S", "-" + strconv.Itoa(lines)}
	if escapes {
		// -e keeps the SGR escapes so the phone renders the real colours.
		args = append(args, "-e")
	}
	out, err := c.exec(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(out, "\n"), nil
}

// ── previews ──────────────────────────────────────────────────────────────

type previewEntry struct {
	text string
	at   time.Time
}

type previewCache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]previewEntry
}

var previews = &previewCache{ttl: 1500 * time.Millisecond, m: map[string]previewEntry{}}

func (pc *previewCache) get(id string) (string, bool) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	e, ok := pc.m[id]
	if !ok || time.Since(e.at) > pc.ttl {
		return "", false
	}
	return e.text, true
}

func (pc *previewCache) put(id, text string) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.m[id] = previewEntry{text: text, at: time.Now()}
}

// Previews captures the last n lines of many panes concurrently.
//
// Serialised, 26 panes at ~11 ms each is ~300 ms - too slow for a tree
// request. A pool of 8 brings it to ~40 ms, and a 1.5 s cache keeps a burst of
// requests from re-running the whole set.
func (c *Client) Previews(ctx context.Context, paneIDs []string, n int) map[string]string {
	const workers = 8

	out := make(map[string]string, len(paneIDs))
	var mu sync.Mutex
	todo := make([]string, 0, len(paneIDs))

	for _, id := range paneIDs {
		if text, ok := previews.get(id); ok {
			out[id] = text
			continue
		}
		todo = append(todo, id)
	}

	ch := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range ch {
				text, err := c.Capture(ctx, id, n, false)
				if err != nil {
					continue
				}
				previews.put(id, text)
				mu.Lock()
				out[id] = text
				mu.Unlock()
			}
		}()
	}
	for _, id := range todo {
		ch <- id
	}
	close(ch)
	wg.Wait()
	return out
}
