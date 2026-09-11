package titler

import (
	"context"

	"github.com/viminizer/remux/internal/agent"
	"github.com/viminizer/remux/internal/tmux"
)

// The glyphs, chosen to be readable in a status line at a glance rather than
// descriptive. Across twenty panes the expensive question is not what each
// agent is doing, it is which two or three are blocked on an answer - so "!"
// is the one that has to stand out.
const (
	glyphWaiting = "!"
	glyphBusy    = "✳"
	glyphDone    = "✓"
)

// glyph maps a verdict onto what gets written, with "" meaning "clear the
// option". A shell is not an agent and never carries a state; Unknown means
// the classifier did not recognise the screen, and inventing a glyph there
// would be a confident guess about a pane we cannot read.
func glyph(s agent.Status) string {
	switch s {
	case agent.Waiting:
		return glyphWaiting
	case agent.Busy:
		return glyphBusy
	case agent.Idle:
		return glyphDone
	default:
		return ""
	}
}

// writeState sets @remux_state, and only when the value actually changes.
//
// The comparison is against the value already on the pane, read from the tree,
// which is the truth on disk rather than a cache that can drift. So an agent
// that stays busy costs no tmux writes at all - #32 is the standing lesson
// about writes on a timer.
func (p *Pass) writeState(ctx context.Context, pane *tmux.Pane, want string) {
	if pane.RemuxState == want {
		return
	}
	p.report(ctx, pane.ID, p.Tmux.SetPaneState(ctx, pane.ID, want))
}
