package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/viminizer/remux/internal/harness"
	"github.com/viminizer/remux/internal/tmux"
)

// cmdLoop is what runs inside a loop-* session. The server starts it there;
// nobody types it.
func cmdLoop(args []string) error {
	fs := flag.NewFlagSet("loop", flag.ContinueOnError)
	role := fs.String("role", "build", "build or review")
	reviewPolicy := fs.String("review-policy", "", "balanced or fixed; empty uses project settings")
	agent := fs.String("agent", "claude", "claude or codex")
	model := fs.String("model", "", "Claude model: sonnet or opus; empty uses CLI settings")
	repo := fs.String("repo", "", "the repo's main checkout")
	session := fs.String("session", "", "this loop's tmux session")
	scope := fs.String("scope", "", "the scope name; empty means full")
	instr := fs.String("instructions", "", "session instructions")
	mode := fs.String("mode", "add", "add to the project defaults, or replace them")
	task := fs.Int("task", 0, "one assigned issue or PR; exit after this task")
	budget := fs.String("budget-key", "", "usage account for an automatically spawned task")
	reserve := fs.Float64("reserve", 0, "shared usage headroom allowance")
	effort := fs.String("effort", "medium", "medium or high reasoning effort")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *repo == "" || !tmux.ValidLoopName(*session) {
		return fmt.Errorf("loop needs --repo and a loop-* --session")
	}
	if *task < 0 || (*task > 0 && *role != "build" && *role != "review") || (*effort != "medium" && *effort != "high") {
		return fmt.Errorf("custom tasks need a build/review role and medium/high effort")
	}
	if *reviewPolicy != "" && *reviewPolicy != "balanced" && *reviewPolicy != "fixed" {
		return fmt.Errorf("review-policy must be balanced or fixed")
	}
	if !harness.ValidModel(*agent, *model) {
		return fmt.Errorf("model must be sonnet or opus for Claude, or empty for CLI settings")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	l := &harness.Loop{Role: *role, Agent: *agent, Model: *model, Repo: *repo, Session: *session, Tmux: tmux.New(),
		Scope: *scope, Instructions: *instr, InstrMode: *mode, Task: *task, BudgetKey: *budget, Reserve: *reserve, Effort: *effort, ReviewPolicy: *reviewPolicy}
	if *task > 0 {
		defer func() {
			// Run has returned: its agent and claim defers have finished. Only
			// this task's own session may be removed, even with remain-on-exit.
			if custom, err := l.Tmux.LoopOption(context.Background(), *session, "@loop_custom"); err == nil && custom == "1" {
				l.Tmux.KillLoop(context.Background(), *session)
			}
		}()
	}
	return l.Run(ctx)
}

// cmdHarness is `remux harness init [dir]`: labels on GitHub, starter files
// in the repo. Safe to run again.
func cmdHarness(args []string) error {
	if len(args) == 0 || args[0] != "init" {
		return fmt.Errorf("usage: remux harness init [repo dir]")
	}
	dir := "."
	if len(args) > 1 {
		dir = args[1]
	}
	ctx := context.Background()
	slug, err := harness.Slug(ctx, dir)
	if err != nil {
		return err
	}
	if err := (harness.GH{Slug: slug, Dir: dir}).EnsureLabels(ctx); err != nil {
		return err
	}
	fmt.Printf("labels ready on %s\n", slug)
	wrote, err := harness.WriteStarterFiles(dir)
	for _, f := range wrote {
		fmt.Printf("wrote %s - fill it in and commit it\n", f)
	}
	return err
}
