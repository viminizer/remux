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
	ID    string `json:"id"`    // %14
	Index int    `json:"index"` // pane index within the window
	Title string `json:"title"` // pane_title - Claude Code puts the live task here
	// RemuxTitle is a name the user gave this pane from the phone, held in the
	// @remux_title pane option. It is separate from Title because pane_title
	// belongs to the running program: an agent rewrites it every render, so a
	// name written there survives less than a second.
	RemuxTitle string `json:"remuxTitle,omitempty"`
	// RemuxTask is the task title remux wrote for this pane, from reading the
	// screen. It is a separate option from RemuxTitle because RemuxTitle
	// belongs to Kevin: he types it from the phone, and a name he chose must
	// never be overwritten by a model ninety seconds later.
	RemuxTask string `json:"remuxTask,omitempty"`
	// RemuxState is the one-glyph agent verdict remux writes into the
	// @remux_state pane option, for the laptop's own status bar to render.
	// It is not the phone's source of truth - Status below is, computed
	// fresh per connection - and it exists so the status line on the laptop
	// can show which of twenty panes is blocked without running a classifier
	// of its own.
	RemuxState string `json:"remuxState,omitempty"`
	Command    string `json:"command"` // pane_current_command
	Path       string `json:"path"`    // pane_current_path
	Active     bool   `json:"active"`  // active pane in its window
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	InMode     bool   `json:"inMode"` // copy/view mode
	Alt        bool   `json:"alt"`    // alternate screen (vim, htop): no scrollback
	Dead       bool   `json:"dead"`
	History    int    `json:"history"` // history_size

	// Activity is window_activity: a unix second, bumped by tmux whenever
	// anything is written to any pane in this window. It is the cheap way to
	// know a screen cannot have changed, and it rides along in the tree so
	// asking costs nothing extra. Not serialised - it moves every second on a
	// working pane, and the tree is diffed by hash.
	Activity int64 `json:"-"`

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
// The three @remux_* options sit just before it and are safe there because
// setPaneOption is their only writer and it refuses a value containing the
// separator.
var treeFormat = strings.Join([]string{
	"#{session_id}", "#{session_name}", "#{session_attached}",
	"#{window_id}", "#{window_index}", "#{window_name}", "#{window_active}",
	"#{window_activity}",
	"#{pane_id}", "#{pane_index}", "#{pane_current_command}",
	"#{pane_current_path}", "#{pane_active}", "#{pane_width}", "#{pane_height}",
	"#{pane_in_mode}", "#{alternate_on}", "#{pane_dead}", "#{history_size}",
	"#{@remux_title}",
	"#{@remux_task}",
	"#{@remux_state}",
	"#{pane_title}",
}, fieldSep)

const treeFields = 23

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
		ID:         f[8],
		Index:      atoi(f[9]),
		Command:    f[10],
		Path:       f[11],
		Active:     f[12] == "1",
		Width:      atoi(f[13]),
		Height:     atoi(f[14]),
		InMode:     f[15] == "1",
		Alt:        f[16] == "1",
		Dead:       f[17] == "1",
		History:    atoi(f[18]),
		RemuxTitle: f[19],
		RemuxTask:  f[20],
		RemuxState: f[21],
		Title:      f[22],
		Activity:   int64(atoi(f[7])),
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
//
// Deliberately no -J. It looks like the fix for ragged output on the phone -
// join the lines tmux wrapped, let the narrow screen reflow them once - and
// it does work on output tmux itself wrapped: a 450-character line printed
// into a 195-column scratch pane comes back as three rows plain and one row
// with -J. It just almost never applies here. Scored read-only over the 27
// live panes on this machine, -J changed the line count of 21 of them by
// zero. Agents wrap their own text to the pane width and end each row with a
// real newline, so tmux never sets the wrapped flag and there is nothing for
// -J to join. The breaks the phone re-wraps were made by the agent, not by
// tmux, and no capture flag can undo those.
//
// Where it did fire it was mostly harm: on the panes that changed, the joins
// were overwhelmingly blank rows being absorbed into the previous line as
// trailing spaces (one Claude Code pane: 74 joins, nearly all of them blank
// lines), which deletes the paragraph breaks that make the output readable.
// -J also preserves trailing spaces everywhere, up to 220 lines of them on a
// single pane, which paints as blocks once -e carries a background colour.
//
// The classifier is the other reason to leave this alone: ws.go reuses this
// same capture for agent.Classify, whose idle patterns are anchored
// (`^\s*❯\s*$`), so joining rows would quietly change pane verdicts on the
// WebSocket path but not the tree path.
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

// PreviewLines is how much of a screen a status verdict needs. The classifier
// reads the last 30 lines (agent.tailLines) after trailing blanks are trimmed,
// so 40 leaves headroom. Every caller uses it: the cache is keyed by pane id
// alone, so a caller that asked for fewer lines would poison the entry for one
// that needs more.
const PreviewLines = 40

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
