package titler

import (
	"context"
	"testing"

	"github.com/viminizer/remux/internal/tmux"
)

// The workspace this was written against, and the answer it has to produce.
// Kevin had already named two of these panes by hand - "react" and "client" -
// so the rule agreeing with him is the evidence that it picks the segment a
// person would.
func TestShortDropsWhatSiblingsShare(t *testing.T) {
	got := Short([]string{
		"yegadev/seoul-wedding-api",
		"yegadev/seoul-wedding-client",
		"yegadev/seoulwomen-react",
		"yegadev/seoulwomen-docs",
		"viminizer/shortlist",
		"viminizer/remux",
	})
	want := map[string]string{
		"yegadev/seoul-wedding-api":    "api",
		"yegadev/seoul-wedding-client": "client",
		"yegadev/seoulwomen-react":     "react",
		"yegadev/seoulwomen-docs":      "docs",
		"viminizer/shortlist":          "shortlist",
		"viminizer/remux":              "remux",
	}
	for repo, w := range want {
		if got[repo] != w {
			t.Errorf("%s -> %q, want %q", repo, got[repo], w)
		}
	}
}

// A repo whose every segment is shared with a longer sibling still has to be
// called something.
func TestShortNeverDropsTheWholeName(t *testing.T) {
	got := Short([]string{"a/shortlist", "a/shortlist-server"})
	if got["a/shortlist"] != "shortlist" {
		t.Errorf("shortlist -> %q, want %q", got["a/shortlist"], "shortlist")
	}
	if got["a/shortlist-server"] != "server" {
		t.Errorf("shortlist-server -> %q, want %q", got["a/shortlist-server"], "server")
	}
}

// A name with no sibling to trim against is still capped, and it loses whole
// segments before it loses letters: "wedding-api" reads, "wedding-ap" does not.
func TestShortClampsOnSegmentBoundaries(t *testing.T) {
	got := Short([]string{"a/some-very-long-project-api"})
	if g := got["a/some-very-long-project-api"]; g != "project-api" {
		t.Errorf("-> %q, want %q", g, "project-api")
	}

	// Nothing to drop: one segment, longer than the cap. Cutting is all that
	// is left.
	got = Short([]string{"a/shardingsphereincubator"})
	if g := got["a/shardingsphereincubator"]; len(g) != maxProject {
		t.Errorf("-> %q (%d chars), want %d", g, len(g), maxProject)
	}
}

func TestShortIgnoresEmptyAndOwnerless(t *testing.T) {
	got := Short([]string{"", "remux"})
	if _, ok := got[""]; ok {
		t.Error("an empty repo got a name")
	}
	if got["remux"] != "remux" {
		t.Errorf("ownerless repo -> %q, want %q", got["remux"], "remux")
	}
}

// The project is written outside the activity gate, and outside the presence
// gate too.
//
// It needs no model and no screen - just the repo, which came along in the
// tree that was read anyway - so making it wait on a pane producing output, or
// on Kevin being at the desk, would hold back the free half of the name for no
// reason. Writing only on a change is what keeps that honest: a pane that has
// not moved directory costs nothing.
func TestProjectIsWrittenWithoutGateOrPresence(t *testing.T) {
	f := newFake()
	p := New(f) // no Chain: the naming half is off entirely
	p.Away = func() bool { return true }

	quiet := int64(100)
	panes := func() []*tmux.Pane {
		return []*tmux.Pane{
			{ID: "%1", Command: "codex", Repo: "yegadev/seoul-wedding-api"},
			{ID: "%2", Command: "codex", Repo: "yegadev/seoul-wedding-client"},
			{ID: "%3", Command: "zsh", Repo: "viminizer/remux", RemuxProject: "remux"},
		}
	}

	p.OnTree(context.Background(), tree(quiet, panes()...))

	if got := f.projects["%1"]; got != "api" {
		t.Errorf("%%1 project = %q, want %q", got, "api")
	}
	if got := f.projects["%2"]; got != "client" {
		t.Errorf("%%2 project = %q, want %q", got, "client")
	}
	// A shell is not an agent pane, so it carries nothing - and one that was
	// an agent has its old project cleared with the rest.
	if got, wrote := f.projects["%3"]; !wrote || got != "" {
		t.Errorf("shell kept project %q", got)
	}
}

// A pane that has not changed directory is not rewritten, so the option does
// not churn on a loop that runs every two seconds.
func TestProjectIsNotRewrittenUnchanged(t *testing.T) {
	f := newFake()
	p := New(f)

	pane := &tmux.Pane{
		ID: "%1", Command: "codex",
		Repo: "viminizer/remux", RemuxProject: "remux",
	}
	p.OnTree(context.Background(), tree(100, pane))

	if _, wrote := f.projects["%1"]; wrote {
		t.Errorf("rewrote an unchanged project: %q", f.projects["%1"])
	}
}
