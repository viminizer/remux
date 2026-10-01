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
	agent := fs.String("agent", "claude", "claude or codex")
	repo := fs.String("repo", "", "the repo's main checkout")
	session := fs.String("session", "", "this loop's tmux session")
	scope := fs.String("scope", "", "the scope name; empty means full")
	instr := fs.String("instructions", "", "session instructions")
	mode := fs.String("mode", "add", "add to the project defaults, or replace them")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *repo == "" || !tmux.ValidLoopName(*session) {
		return fmt.Errorf("loop needs --repo and a loop-* --session")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	l := &harness.Loop{Role: *role, Agent: *agent, Repo: *repo, Session: *session, Tmux: tmux.New(),
		Scope: *scope, Instructions: *instr, InstrMode: *mode}
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
