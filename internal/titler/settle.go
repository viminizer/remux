package titler

import (
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// This file is the answer to "how often should a pane be renamed", and the
// answer turned out to be "once".
//
// Kevin runs one session per problem. He opens it on the first request, keeps
// it until the work is merged and the branch is cleaned up, and then clears or
// starts a new one rather than repurposing it. So the goal of a pane is fixed
// in its first minute and does not move again for hours.
//
// The titler was built as if the opposite were true. It re-asked every pane
// every askEvery forever, with no exit, and every one of those calls was a
// fresh chance to replace a correct name with the step the agent happened to
// be on - so a pane that spent an afternoon on one issue was named "open pr to
// bump jq", then "commit 185 changes", then "push pane title fix". Measured on
// the live workspace: $14.76 across 432 runs in thirteen days, nearly all of
// it re-deciding a question that had one answer.
//
// Two ideas replace the timer.
//
// The first is that the model can say when it is finished. SAME already means
// "the name it has is still right". Two of those in a row is the pane telling
// us the question is settled, and a settled pane leaves the rotation entirely.
// No arbitrary number of minutes, no guess about how long a task takes.
//
// The second is that a new session announces itself for free. Both agents put
// their own name in pane_title, remux already reads that field on every tree
// poll, and it changes exactly when the session does. That is the one signal
// worth spending a model call on, and detecting it costs nothing.

// settleAfter is how many consecutive SAME answers lock a pane.
//
// One is too few: the first SAME can come from a screen that happens to have
// nothing new on it, which is not the same as a name that has been confirmed.
// Two means the name survived two independent looks at two different screens.
// Past that the extra calls buy confidence in a name that is already being
// left alone.
const settleAfter = 2

// settleState is one pane's progress towards being left alone.
type settleState struct {
	sames int    // consecutive SAME answers; settleAfter of them locks
	title string // normalised agent title when last seen, for change detection
	seen  bool   // whether title has ever been recorded, so "" is not a change
}

// settler holds that progress for every live pane.
//
// Keyed by pane id in a process that runs for weeks, so it is pruned by keep
// for the same reason cooldown is.
type settler struct {
	mu sync.Mutex
	m  map[string]*settleState
}

func newSettler() *settler { return &settler{m: map[string]*settleState{}} }

func (s *settler) get(id string) *settleState {
	st := s.m[id]
	if st == nil {
		st = &settleState{}
		s.m[id] = st
	}
	return st
}

// locked reports whether this pane is done being asked about.
func (s *settler) locked(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.m[id]
	return st != nil && st.sames >= settleAfter
}

// same records one SAME answer.
func (s *settler) same(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.get(id).sames++
}

// unsettle puts a pane back in the rotation. Called when a name is written or
// cleared: a name that just changed has not been confirmed by anything.
func (s *settler) unsettle(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.get(id).sames = 0
}

// observe records the pane's current agent title and unsettles it if that
// title has changed - a new session, so a new goal, so the name has to be
// asked again.
//
// The first sighting of a pane is not a change. Without that, every pane is
// unsettled once on the pass that first sees it, which is harmless but makes
// the tests lie about what a change is.
func (s *settler) observe(id, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.get(id)
	if st.seen && st.title != title {
		st.sames = 0
	}
	st.seen, st.title = true, title
}

// keep drops every pane that is no longer in the tree.
func (s *settler) keep(live map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.m {
		if !live[id] {
			delete(s.m, id)
		}
	}
}

// ── normalising an agent's own title ──────────────────────────────────────

// spinnerRe matches the animated glyph both agents prefix their title with.
//
// Claude Code cycles ✻ ✽ ✳ ✶ ✢ ·; Codex uses the braille block. The glyph
// changes several times a second, so the raw title is useless for "has this
// changed" - measured on one pane, "⠧ Fix GitHub issues" and "⠹ Fix GitHub
// issues" seconds apart.
//
// Only these glyphs, not "any leading punctuation": a real title like
// "#339 venue filter" starts with a character that a looser rule would eat.
var spinnerRe = regexp.MustCompile(`^[\x{2800}-\x{28FF}✻✽✳✶✢✷✴·⏺]+\s*`)

var spaceRe = regexp.MustCompile(`\s+`)

// placeholders are titles that carry no information about the work.
//
// "claude code" is what Claude Code calls itself before it has generated a
// title, so it appears on a pane that is running but has not been asked
// anything yet. Treating it as a title would mean a session that has just
// started reads as settled, and - worse - the transition from it to a real
// title would not register as a change.
var placeholders = map[string]bool{
	"claude code": true,
	"codex":       true,
	"zsh":         true,
	"bash":        true,
	"fish":        true,
}

// normalizeTitle turns pane_title into something stable enough to compare and
// clean enough to show a model.
//
// dir and project are what the trailing " | name" suffix is checked against.
// Codex appends its working directory to every title - "Fix GitHub issues |
// shortlist" - and only that suffix is stripped, never an arbitrary pipe: a
// title is the agent's own text and may legitimately contain one.
//
// The empty string means "this pane is not telling us anything", which is a
// different thing from a title that has not been read yet.
func normalizeTitle(raw, dir, project string) string {
	t := spinnerRe.ReplaceAllString(strings.TrimSpace(raw), "")
	t = strings.TrimSpace(t)

	if i := strings.LastIndex(t, "|"); i >= 0 {
		head, tail := strings.TrimSpace(t[:i]), strings.TrimSpace(t[i+1:])
		if head != "" && (strings.EqualFold(tail, filepath.Base(dir)) || strings.EqualFold(tail, project)) {
			t = head
		}
	}

	t = strings.ToLower(spaceRe.ReplaceAllString(strings.TrimSpace(t), " "))
	if placeholders[t] {
		return ""
	}
	// A pane showing nothing but its own directory is showing the shell's
	// default, not a title. Six of the twenty-six live panes are in this
	// state, and naming one after its directory would repeat what the status
	// line already prints beside the name.
	if dir != "" && strings.EqualFold(t, filepath.Base(dir)) {
		return ""
	}
	return t
}
