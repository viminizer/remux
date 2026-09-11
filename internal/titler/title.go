package titler

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/viminizer/remux/internal/agent"
	"github.com/viminizer/remux/internal/tmux"
)

// askEvery is the floor between two model calls about the same pane.
//
// The activity gate alone is not enough here, and that is worth being explicit
// about. The gate exists to skip panes nothing is writing to - it does its job
// overnight, and it is why nothing runs while Kevin sleeps. But a working
// agent writes to its window constantly, so the gate passes it through on
// every tick, and at a two-second tree poll that would be a model call every
// two seconds per busy pane. The gate saves the idle panes; this saves the
// busy ones.
//
// Ninety seconds is chosen against how fast the answer can change. A task that
// deserves a new name takes minutes, not seconds, and SAME makes a call that
// finds nothing new nearly free anyway.
const askEvery = 90 * time.Second

// maxScreenChars bounds one pane's contribution to the batched prompt, so
// twenty panes cannot turn one call into an expensive one. The tail is what
// matters: the last thing on screen is what the agent is doing now.
const maxScreenChars = 1200

// maxTitle is the longest task title that reaches tmux. The rule asked for is
// three to five words; this is the guard for when a model answers with a
// paragraph anyway.
const maxTitle = 48

// job is one pane's worth of question, copied out of the tree so the model
// call can outlive the tick that started it.
type job struct {
	ID     string
	Dir    string
	Task   string // what the pane is called now, for the SAME check
	Screen string
}

// dueForNaming decides whether to spend a model call on this pane.
func (p *Pass) dueForNaming(pane *tmux.Pane) bool {
	if len(p.Chain) == 0 {
		return false
	}
	if p.Enabled != nil && !p.Enabled() {
		return false
	}
	return p.asked.due(pane.ID, askEvery)
}

// startNaming runs one batched call in the background.
//
// Batched because the per-call floor is process startup, not tokens: measured,
// `claude -p --model haiku` takes 4.6s for a trivial prompt and about 3s of
// that is the CLI booting. Eight panes in one call cost about what one pane
// costs.
//
// Backgrounded because that 4.6s is shared with a loop that also keeps the
// pane/repo mapping fresh, and one call already in flight means the next tick
// skips rather than queues. A pile-up of model calls is the one way this
// feature could become expensive by accident.
func (p *Pass) startNaming(ctx context.Context, due []job) {
	if len(due) == 0 {
		return
	}
	// Checked here rather than per pane: reading ioreg costs an exec, and
	// there is no point paying it on a pass that has nothing to ask about.
	if away() {
		return
	}
	if !p.naming.CompareAndSwap(false, true) {
		return
	}
	for _, j := range due {
		p.asked.mark(j.ID)
	}
	go func() {
		defer p.naming.Store(false)
		p.name(ctx, due)
	}()
}

// name asks the chain, then writes what came back.
func (p *Pass) name(ctx context.Context, due []job) {
	titles := p.ask(ctx, due)
	for _, j := range due {
		title, answered := titles[j.ID]

		// SAME means "the name it has is still right", so it is only an
		// answer when the pane has a name. On a pane that has none it is the
		// model declining - the screen said too little - and leaving it there
		// would strand exactly the panes worth naming. Measured on this
		// laptop the first real run left five of twenty-one blank this way.
		if title == sameAnswer {
			if j.Task != "" {
				continue
			}
			answered = false
		}

		if !answered || title == "" {
			// Tier 4: no model answered, this pane was missing from an answer
			// that otherwise parsed, or the model declined. The last thing
			// typed into the pane is a worse name than a model's and an
			// enormously better one than nothing - and unlike the three tiers
			// above it cannot fail, which is what makes the chain terminate.
			title = lastUserLine(j.Screen)
		}
		if title == "" {
			continue
		}
		p.writeTaskByID(ctx, j.ID, j.Task, clean(title))
	}
}

// ask walks the chain until one runner answers with something that parses.
func (p *Pass) ask(ctx context.Context, due []job) map[string]string {
	prompt := buildPrompt(due)
	for _, r := range p.Chain {
		out, err := r.Run(ctx, prompt)
		if err != nil {
			log.Printf("titler: %v", err)
			continue
		}
		titles := parseAnswer(out, due)
		if len(titles) == 0 {
			log.Printf("titler: %s answered nothing usable", r.Name())
			continue
		}
		return titles
	}
	return nil
}

const sameAnswer = "SAME"

// buildPrompt asks for every pane at once.
//
// The rules are strict about shape rather than content because the value of
// twenty titles is that they were written to one rule: mixed lengths and
// capitalisation are exactly what makes Claude Code's own generated titles
// unusable here, even though they are free.
func buildPrompt(due []job) string {
	var b strings.Builder
	b.WriteString(`You are naming tmux panes so a developer can tell twenty running coding agents apart at a glance.

Below is the tail of each pane's screen. For each one, name the task the agent is working on.

Rules:
- 3 to 5 words
- lowercase, no punctuation, no quotes
- name the work, not the tool: "venue filter pagination", not "claude code session"
- if the pane's current name still describes the work, answer exactly: SAME
- if the screen says too little to tell, answer exactly: SAME
- answer one line per pane, in the form "<number>: <name>", and nothing else

`)
	for i, j := range due {
		fmt.Fprintf(&b, "## Pane %d\n", i+1)
		if j.Dir != "" {
			fmt.Fprintf(&b, "directory: %s\n", j.Dir)
		}
		if j.Task != "" {
			fmt.Fprintf(&b, "current name: %s\n", j.Task)
		}
		b.WriteString("screen:\n")
		b.WriteString(tailChars(agent.StripANSI(j.Screen), maxScreenChars))
		b.WriteString("\n\n")
	}
	return b.String()
}

var answerRe = regexp.MustCompile(`^\s*(?:##\s*Pane\s*)?(\d+)\s*[:.)]\s*(.+?)\s*$`)

// parseAnswer maps "3: token refresh race" back onto the pane that was third
// in the prompt. Anything else on the line is ignored rather than treated as a
// failure: these CLIs print their own chatter, and one stray line should not
// throw away nineteen good titles.
func parseAnswer(out string, due []job) map[string]string {
	titles := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		m := answerRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 1 || n > len(due) {
			continue
		}
		titles[due[n-1].ID] = m[2]
	}
	return titles
}

// wordRe is what survives into a title. The leading "#" is deliberate: "pr
// #364 review" is a better name than "pr 364 review", and an issue number is
// one of the few bits of punctuation worth keeping.
var wordRe = regexp.MustCompile(`#?[a-z0-9][a-z0-9._/#-]*`)

// clean enforces the shape the prompt asked for, whichever tier produced the
// answer.
//
// Doing it here rather than trusting the model is the difference between a set
// of titles that reads as one list and a set that reads as four different
// tools' opinions. SetPaneTask sanitizes too, but that is about not corrupting
// a tmux record; this is about the titles being worth reading.
func clean(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Trim(s, `"'`)
	words := wordRe.FindAllString(s, 6)
	if len(words) == 0 {
		return ""
	}
	if len(words) > 5 {
		words = words[:5]
	}
	out := strings.Join(words, " ")
	if len(out) > maxTitle {
		out = strings.TrimSpace(out[:maxTitle])
	}
	return out
}

var promptLineRe = regexp.MustCompile(`^\s*[>❯›»]\s*(.*\S.*)$`)

// lastUserLine is tier 4: the most recent thing that looks like it was typed
// into the pane, straight off the screen.
//
// It reads the composer line every one of these TUIs draws, which is the same
// bet the classifier makes and for the same reason - the screen is the one
// interface every agent has.
func lastUserLine(screen string) string {
	lines := strings.Split(agent.StripANSI(screen), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		m := promptLineRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		// The composer's placeholder and shortcut hints sit on this line too
		// when nothing has been typed.
		t := strings.TrimSpace(m[1])
		if t == "" || strings.HasPrefix(t, "?") || strings.Contains(t, "for shortcuts") {
			continue
		}
		// A slash command is something done to the agent, not the work it is
		// doing. Two panes on the first run came back named "clear", off a
		// "/clear" still sitting on screen - and a name that is confidently
		// wrong is worse than the blank it replaced.
		if strings.HasPrefix(t, "/") {
			continue
		}
		return t
	}
	return ""
}

// tailChars keeps the last n characters, cut at a line boundary so the model
// is never handed half a line.
func tailChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// ── writing ───────────────────────────────────────────────────────────────

func (p *Pass) writeTask(ctx context.Context, pane *tmux.Pane, want string) {
	p.writeTaskByID(ctx, pane.ID, pane.RemuxTask, want)
}

func (p *Pass) writeTaskByID(ctx context.Context, paneID, have, want string) {
	if have == want {
		return
	}
	p.report(ctx, paneID, p.Tmux.SetPaneTask(ctx, paneID, want))
}

// ── cooldown ──────────────────────────────────────────────────────────────

// cooldown remembers when each pane was last asked about. It is keyed by pane
// id and never pruned on its own; Forget clears it wholesale, which is what a
// caller wants after a change that invalidates every answer.
type cooldown struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func newCooldown() *cooldown { return &cooldown{seen: map[string]time.Time{}} }

func (c *cooldown) due(id string, every time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	last, had := c.seen[id]
	return !had || time.Since(last) >= every
}

// mark is called when the question is asked, not when the answer lands, so a
// model that is failing is retried on the cooldown rather than on every tick.
func (c *cooldown) mark(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen[id] = time.Now()
}
