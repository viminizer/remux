package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/viminizer/remux/internal/sv"
	"github.com/viminizer/remux/internal/tmux"
)

// cmdSV is `remux sv ...`: the driver and worker processes the server
// starts, and the tools the supervisor agent runs. The agent's own task
// comes from REMUX_SV_TASK, which the driver sets; a tool that acts on "my
// workers" acts on that task's and no other.
func cmdSV(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: remux sv <list|read|msg|models|spawn|wait|workers|tell|resume|stop|status|log|inbox>")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	tm := tmux.New()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	own := os.Getenv("REMUX_SV_TASK")
	cmd, rest := args[0], args[1:]
	needOwn := func() error {
		if !sv.ValidID(own) {
			return errors.New("this tool is for a supervisor: REMUX_SV_TASK is not set")
		}
		return nil
	}
	fs := flag.NewFlagSet("sv "+cmd, flag.ContinueOnError)

	switch cmd {
	case "run":
		if len(rest) != 1 {
			return errors.New("usage: remux sv run <task>")
		}
		return sv.Drive(ctx, tm, exe, rest[0])

	case "worker":
		resume := fs.Bool("resume", false, "continue the worker's session with resume.md")
		if err := parseAll(fs, rest); err != nil || fs.NArg() != 1 {
			return errors.New("usage: remux sv worker <task>/w<nn> [--resume]")
		}
		err := sv.RunWorker(ctx, exe, fs.Arg(0), *resume)
		if err != nil {
			sv.FailWorker(fs.Arg(0), err)
		}
		return err

	case "list":
		for _, t := range sv.List() {
			fmt.Printf("%s  %s  %s  %s\n", t.ID, t.Phase, t.Repo, t.Goal)
			if t.Summary != "" {
				fmt.Printf("    now: %s\n", t.Summary)
			}
			if len(t.Areas) > 0 {
				fmt.Printf("    areas: %s\n", strings.Join(t.Areas, ", "))
			}
		}
		return nil

	case "read":
		sv.RunningWorkers(ctx, tm) // a worker whose session died reads as failed, not running
		if len(rest) < 1 || len(rest) > 2 {
			return errors.New("usage: remux sv read <task>[/w<nn>] [status|plan|events|report|stream|retro|chat]")
		}
		what := ""
		if len(rest) == 2 {
			what = rest[1]
		}
		out, err := svRead(rest[0], what)
		if err != nil {
			return err
		}
		fmt.Print(out)
		if sv.ValidID(own) && !strings.HasPrefix(rest[0], own) {
			sv.AddEvent(own, sv.Event{Kind: "read", Text: strings.TrimSpace("read " + rest[0] + " " + what)})
		}
		return nil

	case "msg":
		if len(rest) != 2 || !sv.ValidID(rest[0]) {
			return errors.New(`usage: remux sv msg <task> "<text>"`)
		}
		if _, err := sv.Load(rest[0]); err != nil {
			return err
		}
		from := orDefault(own, "kevin")
		if err := sv.Send(rest[0], 0, from, rest[1]); err != nil {
			return err
		}
		// Both sides log it, so Kevin's status shows the coordination.
		sv.AddEvent(rest[0], sv.Event{Kind: "msg", Text: "from " + from + ": " + rest[1]})
		if sv.ValidID(own) {
			sv.AddEvent(own, sv.Event{Kind: "msg", Text: "to " + rest[0] + ": " + rest[1]})
		}
		fmt.Println("sent")
		return nil

	case "models":
		fmt.Print(sv.ModelsReport())
		return nil

	case "spawn":
		if err := needOwn(); err != nil {
			return err
		}
		var s sv.Spec
		fs.StringVar(&s.Role, "role", "", "short role name")
		fs.StringVar(&s.Agent, "agent", "claude", "claude or codex")
		fs.StringVar(&s.Model, "model", "", "empty: the default model")
		fs.StringVar(&s.Dir, "dir", "", "where it works: its worktree, or the repo for read-only roles")
		fs.StringVar(&s.Why, "why", "", "why this worker")
		fs.BoolVar(&s.ReadOnly, "read-only", false, "may not change anything")
		if err := parseAll(fs, rest); err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
		if err != nil {
			return err
		}
		s.Instructions = string(b)
		w, err := sv.Spawn(ctx, tm, exe, own, s)
		if err != nil {
			return err
		}
		fmt.Println(w.ID)
		return nil

	case "wait":
		if err := needOwn(); err != nil {
			return err
		}
		timeout := fs.Duration("timeout", 5*time.Minute, "return after this long")
		if err := parseAll(fs, rest); err != nil {
			return err
		}
		return svWait(ctx, tm, own, fs.Args(), *timeout)

	case "workers":
		if err := needOwn(); err != nil {
			return err
		}
		sv.RunningWorkers(ctx, tm)
		for _, w := range sv.Workers(own) {
			fmt.Printf("%s  %s  %s %s  %s  %s\n", w.ID, w.State, w.Agent, orDefault(w.Model, "default"), w.Role, w.Dir)
		}
		return nil

	case "tell", "resume":
		if err := needOwn(); err != nil {
			return err
		}
		if len(rest) != 2 {
			return fmt.Errorf(`usage: remux sv %s <worker> "<text>"`, cmd)
		}
		n, err := ownWorker(own, rest[0])
		if err != nil {
			return err
		}
		if cmd == "resume" {
			return sv.Resume(ctx, tm, exe, own, n, rest[1])
		}
		if err := sv.Send(own, n, "your supervisor", rest[1]); err != nil {
			return err
		}
		sv.AddEvent(own, sv.Event{Kind: "msg", Worker: fmt.Sprintf("%s/w%02d", own, n), Text: "told: " + rest[1]})
		fmt.Println("sent")
		return nil

	case "stop":
		if err := needOwn(); err != nil {
			return err
		}
		if len(rest) != 1 {
			return errors.New("usage: remux sv stop <worker>")
		}
		n, err := ownWorker(own, rest[0])
		if err != nil {
			return err
		}
		return sv.StopWorker(ctx, tm, own, n)

	case "status":
		if err := needOwn(); err != nil {
			return err
		}
		summary := fs.String("summary", "", "one line: where it is")
		phase := fs.String("phase", "", "blocked or done")
		question := fs.String("question", "", "with --phase blocked: what you need from Kevin")
		patch := fs.Bool("json", false, "read steps, changes, evidence and areas as JSON on stdin")
		if err := parseAll(fs, rest); err != nil {
			return err
		}
		return svStatus(ctx, tm, own, *summary, *phase, *question, *patch)

	case "log":
		if err := needOwn(); err != nil {
			return err
		}
		kind := fs.String("kind", "note", "decision, change, failure, merge or note")
		if err := parseAll(fs, rest); err != nil || fs.NArg() != 1 {
			return errors.New(`usage: remux sv log --kind <kind> "<text>"`)
		}
		switch *kind {
		case "decision", "change", "failure", "merge", "note":
		default:
			return errors.New("kind is decision, change, failure, merge or note")
		}
		return sv.AddEvent(own, sv.Event{Kind: *kind, Text: fs.Arg(0)})

	case "inbox":
		// --hook is Claude's PostToolUse hook: print nothing when the box is
		// empty, else the messages as context for the agent's next step.
		hook := fs.Bool("hook", false, "answer as a Claude Code hook")
		if err := parseAll(fs, rest); err != nil {
			return err
		}
		if !sv.ValidID(own) {
			return nil
		}
		n := 0
		if wid := os.Getenv("REMUX_SV_WORKER"); wid != "" {
			if _, wn, ok := sv.ParseWorker(wid); ok {
				n = wn
			}
		}
		text := sv.FormatInbox(sv.Take(own, n))
		if text == "" {
			if !*hook {
				fmt.Println("no messages")
			}
			return nil
		}
		if *hook {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"hookSpecificOutput": map[string]string{
				"hookEventName": "PostToolUse", "additionalContext": text}})
		}
		fmt.Print(text)
		return nil
	}
	return fmt.Errorf("unknown command sv %s", cmd)
}

// parseAll parses flags wherever they appear, before or after the other
// arguments. An agent writes "wait w03 --timeout 9m" as often as the other
// way round, and Go's flag parsing stops at the first non-flag.
func parseAll(fs *flag.FlagSet, args []string) error {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	return fs.Parse(append([]string{"--"}, pos...))
}

// ownWorker accepts "w03" or "<own task>/w03", and nothing of another task.
func ownWorker(own, ref string) (int, error) {
	if !strings.Contains(ref, "/") {
		ref = own + "/" + ref
	}
	task, n, ok := sv.ParseWorker(ref)
	if !ok {
		return 0, fmt.Errorf("%q is not a worker id", ref)
	}
	if task != own {
		return 0, errors.New("you can only act on your own workers; message their supervisor instead")
	}
	return n, nil
}

func svRead(ref, what string) (string, error) {
	if task, n, ok := sv.ParseWorker(ref); ok {
		dir := sv.WorkerDir(task, n)
		if dir == "" {
			return "", fmt.Errorf("no worker %s", ref)
		}
		switch what {
		case "", "report":
			w, err := sv.LoadWorker(task, n)
			if err != nil {
				return "", err
			}
			b, _ := os.ReadFile(dir + "/report.md")
			return fmt.Sprintf("%s  %s  %s %s  %s\n\n%s", w.ID, w.State, w.Agent, orDefault(w.Model, "default"), w.Role, b), nil
		case "stream":
			b, err := os.ReadFile(dir + "/stream.jsonl")
			return string(b), err
		case "instructions":
			b, err := os.ReadFile(dir + "/instructions.md")
			return string(b), err
		}
		return "", errors.New("a worker has report, stream and instructions")
	}
	if !sv.ValidID(ref) {
		return "", fmt.Errorf("%q is not a task or worker id", ref)
	}
	t, err := sv.Load(ref)
	if err != nil {
		return "", err
	}
	switch what {
	case "", "status":
		b, _ := json.MarshalIndent(t, "", "  ")
		return string(b) + "\n", nil
	case "plan":
		return sv.ReadFile(ref, "plan.md"), nil
	case "retro":
		return sv.ReadFile(ref, "retro.md"), nil
	case "events":
		var b strings.Builder
		for _, e := range sv.Events(ref, 200) {
			fmt.Fprintf(&b, "%s %-8s %s %s\n", e.At.Local().Format("01-02 15:04"), e.Kind, e.Worker, e.Text)
		}
		return b.String(), nil
	case "chat":
		var b strings.Builder
		for _, m := range sv.Chat(ref) {
			fmt.Fprintf(&b, "[%s] %s\n\n", m.From, m.Text)
		}
		return b.String(), nil
	}
	return "", errors.New("a task has status, plan, events, retro and chat")
}

// svWait blocks until the given workers (default: all of own's running
// ones) finish or the timeout passes, and prints each finished one's report
// and any messages for the supervisor.
func svWait(ctx context.Context, tm *tmux.Client, own string, refs []string, timeout time.Duration) error {
	var want []int
	for _, r := range refs {
		n, err := ownWorker(own, r)
		if err != nil {
			return err
		}
		want = append(want, n)
	}
	if len(want) == 0 {
		for _, w := range sv.Workers(own) {
			if w.State == "running" {
				want = append(want, w.N)
			}
		}
	}
	deadline := time.Now().Add(timeout)
	printed := map[int]bool{}
	for {
		sv.RunningWorkers(ctx, tm) // marks workers whose session died
		running := []string{}
		for _, n := range want {
			w, err := sv.LoadWorker(own, n)
			if err != nil {
				return err
			}
			if w.State == "running" {
				running = append(running, fmt.Sprintf("%s (%s)", w.ID, time.Since(w.Started).Round(time.Minute)))
				continue
			}
			if !printed[n] {
				printed[n] = true
				out, _ := svRead(w.ID, "report")
				fmt.Printf("── finished: %s\n", out)
			}
		}
		if inbox := sv.FormatInbox(sv.Take(own, 0)); inbox != "" {
			fmt.Print("── messages:\n" + inbox)
		}
		if len(running) == 0 {
			if len(want) == 0 {
				fmt.Println("no workers running")
			}
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			fmt.Printf("── still running: %s\n", strings.Join(running, ", "))
			return nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
	}
}

// svStatus applies the supervisor's own status update. The phases it may
// set are blocked and done; done needs the retro and no running worker.
func svStatus(ctx context.Context, tm *tmux.Client, own, summary, phase, question string, patch bool) error {
	var p struct {
		Steps    *[]sv.Step `json:"steps"`
		Changes  *[]string  `json:"changes"`
		Evidence *[]string  `json:"evidence"`
		Areas    *[]string  `json:"areas"`
	}
	if patch {
		if err := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20)).Decode(&p); err != nil {
			return fmt.Errorf("the JSON patch: %w", err)
		}
	}
	switch phase {
	case "":
	case sv.Blocked:
		if strings.TrimSpace(question) == "" {
			return errors.New("blocked needs --question: what you need from Kevin")
		}
	case sv.Done:
		if strings.TrimSpace(sv.ReadFile(own, "retro.md")) == "" {
			return errors.New("write retro.md before you call it done")
		}
		sv.RunningWorkers(ctx, tm)
		for _, w := range sv.Workers(own) {
			if w.State == "running" {
				return fmt.Errorf("%s is still running", w.ID)
			}
		}
	default:
		return errors.New("you may set the phase to blocked or done")
	}
	_, err := sv.Update(own, func(t *sv.Task) error {
		if phase != "" && !sv.Active(t.Phase) {
			return fmt.Errorf("the task is %s", t.Phase)
		}
		if summary != "" {
			t.Summary = summary
		}
		if phase != "" {
			t.Phase = phase
		}
		if phase == sv.Blocked {
			t.Question = question
		}
		if p.Steps != nil {
			t.Steps = *p.Steps
		}
		if p.Changes != nil {
			t.Changes = *p.Changes
		}
		if p.Evidence != nil {
			t.Evidence = *p.Evidence
		}
		if p.Areas != nil {
			t.Areas = *p.Areas
		}
		return nil
	})
	if err == nil {
		fmt.Println("status updated")
	}
	return err
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
