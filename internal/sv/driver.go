package sv

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/viminizer/remux/internal/tmux"
)

// Drive is what runs in the task's own tmux session. It moves the task
// through its phases and runs the supervisor agent's turns; the agent does
// the thinking. It only reads and writes the task folder, so it can be
// killed and started again at any point.
func Drive(ctx context.Context, tm *tmux.Client, exe, id string) error {
	d := &driver{tm: tm, exe: exe, id: id}
	for ctx.Err() == nil {
		t, err := Load(id)
		if err != nil {
			return err
		}
		switch t.Phase {
		case Discussing, Review:
			if msgs := Unanswered(id); len(msgs) > 0 {
				d.discuss(ctx, t, msgs)
				continue
			}
		case Planning:
			d.plan(ctx, t)
			continue
		case Queued:
			if ok, why := takeSlot(id); ok {
				continue
			} else {
				Update(id, func(t *Task) error { t.Summary = why; return nil })
				sleep(ctx, 15*time.Second)
				continue
			}
		case Running:
			d.run(ctx, t)
			continue
		case Blocked:
			if msgs := Unanswered(id); len(msgs) > 0 {
				AddMessage(id, "supervisor", "Thanks. I continue with that.")
				Update(id, func(t *Task) error { t.Phase, t.Question = Running, ""; return nil })
				d.runTurn(ctx, AnswerTurn(msgs))
				continue
			}
		case Done, Stopped:
			for _, w := range Workers(id) {
				if w.State == "running" {
					StopWorker(ctx, tm, id, w.N)
				}
			}
			return nil
		}
		sleep(ctx, 3*time.Second)
	}
	return nil
}

type driver struct {
	tm      *tmux.Client
	exe, id string
	stalls  int // run turns in a row that ended without progress
}

// turn runs one supervisor agent turn. A new session gets the brief first;
// a session that is gone is replaced by a new one that catches up from the
// files. Messages waiting in the mailbox go in front of the prompt.
func (d *driver) turn(ctx context.Context, prompt string) Result {
	t, err := Load(d.id)
	if err != nil {
		return Result{Code: -1, Final: err.Error()}
	}
	if inbox := Take(d.id, 0); len(inbox) > 0 {
		prompt = FormatInbox(inbox) + "\n" + prompt
	}
	run := func(session, prompt string) Result {
		if session == "" {
			prompt = Brief(t, d.exe) + "\n\n" + prompt
		}
		r := &Run{Agent: t.Agent, Effort: "high", Dir: t.Repo, Prompt: prompt, Session: session,
			Stream: filepath.Join(Dir(d.id), "supervisor.jsonl"), Hook: "'" + d.exe + "' sv inbox --hook",
			Env: []string{"REMUX_SV_TASK=" + d.id}, Timeout: 12 * time.Hour}
		return r.Exec(ctx)
	}
	res := run(t.Session, prompt)
	if res.Missing {
		log.Printf("session %s is gone; starting a new one", t.Session)
		res = run("", CatchUp(t)+"\n\n"+prompt)
	}
	if res.Session != "" && res.Session != t.Session {
		Update(d.id, func(t *Task) error { t.Session = res.Session; return nil })
	}
	if !res.Limit.IsZero() {
		msg := "Usage limit, back at " + res.Limit.Format("15:04")
		Update(d.id, func(t *Task) error { t.Summary = msg; return nil })
		AddEvent(d.id, Event{Kind: "note", Text: msg})
		sleep(ctx, time.Until(res.Limit)+time.Minute)
	}
	return res
}

func (d *driver) discuss(ctx context.Context, t *Task, msgs []Message) {
	res := d.turn(ctx, DiscussTurn(msgs, t.Session == ""))
	if !res.Limit.IsZero() {
		return // the message is still unanswered; it is answered after the reset
	}
	reply := strings.TrimSpace(res.Final)
	if res.Code != 0 || reply == "" {
		reply = "(I could not answer: " + short(orStr(res.Final, fmt.Sprintf("exit %d", res.Code)), 300) + ". Send it again.)"
	}
	AddMessage(d.id, "supervisor", reply)
}

func (d *driver) plan(ctx context.Context, t *Task) {
	feedback := Unanswered(d.id)
	planFile := filepath.Join(Dir(d.id), "plan.md")
	before, _ := os.Stat(planFile)
	res := d.turn(ctx, PlanTurn(t, feedback))
	if !res.Limit.IsZero() {
		return
	}
	after, err := os.Stat(planFile)
	wrote := err == nil && after.Size() > 0 && (before == nil || after.ModTime().After(before.ModTime()))
	if res.Code != 0 || !wrote {
		AddMessage(d.id, "supervisor", "(I could not write the plan: "+short(orStr(res.Final, fmt.Sprintf("exit %d", res.Code)), 300)+". Ask for it again.)")
		Update(d.id, func(t *Task) error { t.Phase = Discussing; return nil })
		return
	}
	AddMessage(d.id, "supervisor", orStr(strings.TrimSpace(res.Final), "The plan is ready."))
	Update(d.id, func(t *Task) error {
		if t.Phase == Planning {
			t.Phase, t.Summary = Review, "The plan waits for your approval"
		}
		return nil
	})
}

// run starts or continues the work. A turn that ends while the task is
// still running did not finish; after three such turns in a row with no
// event in between, it is stuck, and Kevin is asked.
func (d *driver) run(ctx context.Context, t *Task) {
	prompt := RunTurn()
	if hasRunEvents(d.id) {
		prompt = ContinueTurn("")
	}
	before := len(Events(d.id, 0))
	res := d.runTurn(ctx, prompt)
	t, err := Load(d.id)
	if err != nil || t.Phase != Running || !res.Limit.IsZero() {
		d.stalls = 0
		return
	}
	if len(Events(d.id, 0)) > before+1 {
		d.stalls = 0
	} else {
		d.stalls++
	}
	if d.stalls >= 3 {
		Update(d.id, func(t *Task) error {
			t.Phase = Blocked
			t.Question = "I stopped three times without progress. Last message: " + short(res.Final, 300) + " What should I do?"
			return nil
		})
		d.stalls = 0
	}
}

func (d *driver) runTurn(ctx context.Context, prompt string) Result {
	AddEvent(d.id, Event{Kind: "note", Text: "supervisor turn"})
	return d.turn(ctx, prompt)
}

// hasRunEvents reports whether the run phase has already started once.
func hasRunEvents(id string) bool {
	for _, e := range Events(id, 0) {
		if e.Kind == "note" && e.Text == "supervisor turn" {
			return true
		}
	}
	return false
}

// takeSlot moves a queued task to running when the limits allow: one active
// supervisor per repo and a total across repos, both from Settings.
func takeSlot(id string) (bool, string) {
	unlock, err := lock(filepath.Join(Root(), "slots.lock"))
	if err != nil {
		return false, err.Error()
	}
	defer unlock()
	t, err := Load(id)
	if err != nil {
		return false, err.Error()
	}
	lim := CurrentLimits()
	total, same := 0, 0
	for _, o := range List() {
		if o.ID == id || !Active(o.Phase) {
			continue
		}
		total++
		if o.Repo == t.Repo {
			same++
		}
	}
	switch {
	case same >= lim.PerRepo:
		return false, "Waiting: another supervisor runs in this repo"
	case total >= lim.Total:
		return false, fmt.Sprintf("Waiting: %d supervisors run already", total)
	}
	_, err = Update(id, func(t *Task) error {
		if t.Phase == Queued {
			t.Phase, t.Summary = Running, "Starting"
		}
		return nil
	})
	return err == nil, ""
}

func orStr(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
