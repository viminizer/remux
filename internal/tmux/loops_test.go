package tmux

import (
	"context"
	"testing"
)

func TestLoopNames(t *testing.T) {
	for _, ok := range []string{"loop-claude-remux", "loop-review-saas", "loop-codex-a1"} {
		if !ValidLoopName(ok) {
			t.Errorf("%q should be a loop name", ok)
		}
	}
	// Anything else is one of Kevin's sessions, or a tmux target trick.
	for _, bad := range []string{"", "loop-", "main", "remux-test", "loop-A", "loop-x:1", "loop-x;y", "=loop-x", "xloop-a"} {
		if ValidLoopName(bad) {
			t.Errorf("%q should be refused", bad)
		}
	}
	c := New()
	if err := c.SetLoopOption(context.Background(), "main", "@loop_state", "idle"); err == nil {
		t.Error("writing to a non-loop session must be refused")
	}
	if err := c.SetLoopOption(context.Background(), "loop-a", "status", "off"); err == nil {
		t.Error("writing a non-loop option must be refused")
	}
}

// Live: a throwaway loop session, written and read back, then killed.
func TestLoopSessionRoundTrip(t *testing.T) {
	c := New()
	ctx := context.Background()
	if _, err := c.Version(ctx); err != nil {
		t.Skipf("no tmux: %v", err)
	}
	name := "loop-test-roundtrip"
	if err := c.NewLoopSession(ctx, name, t.TempDir(), []string{"sleep", "30"}); err != nil {
		t.Fatal(err)
	}
	defer c.KillLoop(ctx, name)

	instr := "only the backend\nand nothing |~| else"
	for opt, v := range map[string]string{
		"@loop_role": "build", "@loop_agent": "claude", "@loop_state": "working",
		"@loop_issue": "12", "@loop_instr": instr, "@loop_title": "Fix: the |~| thing",
	} {
		if err := c.SetLoopOption(ctx, name, opt, v); err != nil {
			t.Fatalf("%s: %v", opt, err)
		}
	}
	got, err := c.LoopOption(ctx, name, "@loop_instr")
	if err != nil || got != instr {
		t.Fatalf("LoopOption = %q, %v; want %q", got, err, instr)
	}

	loops, err := c.Loops(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var l *Loop
	for i := range loops {
		if loops[i].Name == name {
			l = &loops[i]
		}
	}
	if l == nil {
		t.Fatalf("%s not listed in %+v", name, loops)
	}
	if l.Role != "build" || l.Agent != "claude" || l.State != "working" || l.Issue != 12 ||
		l.Instructions != instr || l.Title != "Fix: the |~| thing" || l.Pane == "" {
		t.Errorf("loop read back wrong: %+v", l)
	}

	if err := c.SetLoopOption(ctx, name, "@loop_issue", ""); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.LoopOption(ctx, name, "@loop_issue"); v != "" {
		t.Errorf("unset option still reads %q", v)
	}
}
