package titler

import "time"

// The journal is what makes the one part of remux that spends money
// accountable.
//
// Everything else here can be checked by looking at a pane: the glyph is right
// or it is wrong, the name reads well or it does not. A model call leaves no
// trace at all. Before this, "is it running, how often, which tier answered,
// and what did it write" could only be answered by reading remux.log over ssh
// - and the log holds failures only, because logging every success would fill
// it. So the questions that actually get asked - is it working right now, how
// much has it done today, why is that pane still blank - had no answer.
//
// It is in memory and nowhere else. A run is interesting for as long as you
// might wonder about it, which is minutes; a file would mean rotation, growth
// and a schema, and none of that buys anything a ring buffer does not.

// kept is how many runs are remembered.
//
// At the ninety-second floor between calls this is somewhere over half an hour
// of history, which is as far back as "what has it been doing" ever reaches.
// It also bounds the whole feature: this is the only per-run state a process
// that runs for weeks holds on to.
const kept = 24

// Run is one batched model call - the unit the money is actually spent in.
type Run struct {
	At    int64  `json:"at"`    // unix ms, so the phone can say "2 min ago"
	MS    int64  `json:"ms"`    // wall time, ~3s of which is CLI startup
	Tier  string `json:"tier"`  // the runner that answered; empty when none did
	Panes int    `json:"panes"` // how many went into the one prompt
	Chars int    `json:"chars"` // prompt size
	// USD is what this run cost at list price, summed over every tier that
	// billed - not only the one that answered.
	USD float64 `json:"usd"`
	// Metered is how many tiers reported a number. Zero with a Tier set means
	// the run cost something nobody counted, which the screen says plainly
	// rather than showing it as free.
	Metered int `json:"metered"`
	// Tokens across the same tiers. Cache reads dominate a warm call and are
	// the reason a repeat costs a tenth of a first.
	In         int     `json:"in,omitempty"`
	Out        int     `json:"out,omitempty"`
	CacheRead  int     `json:"cacheRead,omitempty"`
	CacheWrite int     `json:"cacheWrite,omitempty"`
	Names      []Named `json:"names"`
	// Cleared counts panes the model said had nothing on them to name. A run
	// that clears stale names rather than writing new ones has done its job,
	// and without this it reads in the journal as a run that did nothing.
	Cleared int      `json:"cleared"`
	Notes   []string `json:"notes"` // one line per tier that failed, in order
}

// add folds one tier's bill into the run.
func (r *Run) add(a Answer) {
	if !a.Metered {
		return
	}
	r.Metered++
	r.USD += a.USD
	r.In += a.In
	r.Out += a.Out
	r.CacheRead += a.CacheRead
	r.CacheWrite += a.CacheWrite
}

// Named is one pane that came out of a run with a new name on it.
type Named struct {
	Pane    string `json:"pane"`
	Project string `json:"project"`
	Title   string `json:"title"`
	From    string `json:"from"` // model, or screen for the tier-4 fallback
}

const (
	fromModel  = "model"
	fromScreen = "screen"
)

// Naming is the whole answer to "what is the background AI doing".
type Naming struct {
	// Enabled is the switch on the Settings screen. Chain is what that switch
	// would run - empty means no CLI was found, which looks identical from the
	// phone and is a completely different problem.
	Enabled bool     `json:"enabled"`
	Chain   []string `json:"chain"`
	// Working means a call is in flight right now.
	Working bool `json:"working"`
	// Away is the last presence answer, not a fresh one. Asking would fork
	// ioreg on every poll of this endpoint, and the value moves on a
	// fifteen-minute scale.
	Away bool `json:"away"`
	// RetryMS is how long the chain is being left alone after failing at
	// every tier. Zero when it is not.
	RetryMS int64 `json:"retryMs"`

	Calls  int `json:"calls"`  // model calls since remux started
	Wrote  int `json:"wrote"`  // names actually written to a pane
	Failed int `json:"failed"` // calls where every tier failed
	// Chars is every character ever sent to a model by this process.
	Chars int `json:"chars"`
	// USD is what this process has spent, at list price. Since the service
	// started, like Calls and Wrote beside it - Spend is the number that
	// survives a restart.
	USD float64 `json:"usd"`

	// Spend is the bill, kept across restarts. Nil when there is no ledger,
	// which is every test and any run where ~/.config could not be written.
	Spend *SpendReport `json:"spend,omitempty"`

	Runs []Run `json:"runs"` // newest first
}

// SpendReport is what the Naming screen shows about money.
type SpendReport struct {
	Month string  `json:"month"` // "2026-09"
	USD   float64 `json:"usd"`   // this month so far
	Runs  int     `json:"runs"`
	// Unmetered is how many of those runs billed without reporting a number.
	// Above zero, the total is a floor rather than a figure.
	Unmetered int `json:"unmetered"`
	// Projected is USD extrapolated to the whole month on elapsed days.
	Projected float64 `json:"projected"`
	// Through is how far into the month the total covers, in days, so the
	// screen can say how much the projection is standing on.
	Through float64 `json:"through"`

	PrevMonth string  `json:"prevMonth,omitempty"`
	PrevUSD   float64 `json:"prevUsd,omitempty"`

	// Basis is what the number means. The CLI reports list price, which is
	// what the calls would bill at - not necessarily what Kevin is charged,
	// since a subscription covers them instead.
	Basis string `json:"basis"`
}

// Report is a snapshot for the phone.
func (p *Pass) Report() Naming {
	// Before the lock: Enabled reads the server's settings under the server's
	// own mutex, and OnTree takes them in that order too. Taking p.mu first
	// here would be the one path that takes them the other way round.
	enabled := p.Enabled == nil || p.Enabled()

	chain := make([]string, 0, len(p.Chain))
	for _, r := range p.Chain {
		chain = append(chain, r.Name())
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	n := Naming{
		Enabled: enabled,
		Chain:   chain,
		Working: p.naming.Load(),
		Away:    p.wasAway,
		Calls:   p.calls,
		Wrote:   p.wrote,
		Failed:  p.fails,
		Chars:   p.chars,
		USD:     p.usd,
		Runs:    make([]Run, 0, len(p.runs)),
	}
	if d := time.Until(p.retryAt); d > 0 {
		n.RetryMS = d.Milliseconds()
	}
	for i := len(p.runs) - 1; i >= 0; i-- {
		n.Runs = append(n.Runs, p.runs[i])
	}
	if p.Ledger != nil {
		now := time.Now()
		this, prev := p.Ledger.Report(now)
		sr := &SpendReport{
			Month:     this.Month,
			USD:       this.USD,
			Runs:      this.Runs,
			Unmetered: this.Unmetered,
			Projected: project(this, now),
			Through:   float64(now.Day()-1) + float64(now.Hour())/24,
			Basis:     "list",
		}
		if prev != nil {
			sr.PrevMonth, sr.PrevUSD = prev.Month, prev.USD
		}
		n.Spend = sr
	}
	return n
}

// record files one finished run.
func (p *Pass) record(r Run) {
	p.mu.Lock()
	p.calls++
	p.wrote += len(r.Names)
	p.chars += r.Chars
	p.usd += r.USD
	if r.Tier == "" {
		p.fails++
	}
	p.runs = append(p.runs, r)
	if len(p.runs) > kept {
		p.runs = p.runs[len(p.runs)-kept:]
	}
	p.mu.Unlock()

	// Outside p.mu, and not deferred: the ledger takes its own lock and
	// writes a file, and holding the pass lock across a disk write would
	// stall the tree poller this hangs off.
	if p.Ledger != nil {
		p.Ledger.Add(r, time.Now())
	}
}
