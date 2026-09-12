package titler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The ledger is the one piece of naming state that outlives the process.
//
// journal.go argues that a run is interesting for minutes and belongs in a
// ring buffer in memory, and that still holds. A month's bill is the opposite
// kind of fact: it is worth nothing until it has been accumulating for weeks,
// and remux restarts several times on a day Kevin is working on it. Answering
// "what is this costing me every month" from process uptime would answer for
// the last eleven minutes and call it a month.
//
// So this is a file, and it is a file rather than the ring buffer's schema
// problem because it is four numbers that never grow: the month in progress
// and the one before it. There is nothing to rotate and nothing to page
// through. Two months is what the screen can use - this month, and last month
// as the only honest thing to compare it against.
type Ledger struct {
	mu   sync.Mutex
	path string
	book book
}

type book struct {
	This Month  `json:"this"`
	Prev *Month `json:"prev,omitempty"`
}

// Month is one calendar month of spending.
type Month struct {
	Month string  `json:"month"` // "2026-09"
	USD   float64 `json:"usd"`
	Runs  int     `json:"runs"`
	// Unmetered counts runs that billed without reporting a number - a tier
	// that answers in plain text, or a CLI whose envelope could not be read.
	// Kept so the total can say "at least this much" rather than implying the
	// unmeasured calls were free.
	Unmetered int `json:"unmetered"`
}

// SpendPath is where the ledger lives, beside the rest of remux's state.
func SpendPath(dir string) string { return filepath.Join(dir, "naming-spend.json") }

// OpenLedger reads the ledger at path, or starts an empty one.
//
// A missing or unreadable file is not an error worth failing a service over:
// the worst case is that one month's total restarts from zero, and refusing to
// name panes because a bookkeeping file was corrupt would be the wrong trade
// by a distance.
func OpenLedger(path string) *Ledger {
	l := &Ledger{path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &l.book)
	}
	return l
}

// Add files one finished run and writes the ledger back.
//
// Every run, rather than on a timer or at shutdown: a LaunchAgent under
// KeepAlive is killed, not asked to stop, so anything held for later is
// eventually lost. The file is a few hundred bytes and a run happens at most
// every thirty seconds.
func (l *Ledger) Add(r Run, now time.Time) {
	if r.Tier == "" && r.USD == 0 && r.Metered == 0 {
		// Nothing ran, or nothing was spent. Not worth a write.
		return
	}
	l.mu.Lock()
	l.roll(now)
	l.book.This.USD += r.USD
	l.book.This.Runs++
	if r.Metered == 0 {
		l.book.This.Unmetered++
	}
	b, err := json.Marshal(l.book)
	l.mu.Unlock()
	if err == nil {
		l.write(b)
	}
}

// Report returns the two months, rolled forward to now.
//
// The roll matters here and not only in Add: a Mac left running over a month
// boundary with nothing to name would otherwise keep showing August's total
// under September's heading until the next call.
func (l *Ledger) Report(now time.Time) (this Month, prev *Month) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.roll(now)
	if l.book.Prev != nil {
		p := *l.book.Prev
		prev = &p
	}
	return l.book.This, prev
}

// roll moves to a new month when the clock has. Caller holds the lock.
func (l *Ledger) roll(now time.Time) {
	m := now.Format("2006-01")
	if l.book.This.Month == m {
		return
	}
	if l.book.This.Month != "" {
		p := l.book.This
		l.book.Prev = &p
	}
	l.book.This = Month{Month: m}
}

// write replaces the file in one step.
//
// Temp file and rename, because the alternative is a truncated file: the
// service is killed rather than stopped, and a half-written ledger is one that
// OpenLedger silently reads as zero. Same reason config.json wants this - see
// #46.
func (l *Ledger) write(b []byte) {
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, l.path); err != nil {
		_ = os.Remove(tmp)
	}
}

// project extrapolates a month-to-date total to the whole month.
//
// Straight-line on elapsed days, which is the only shape that does not need a
// model of how Kevin works. It is stated as a projection on the screen, and it
// is honest about being one: on the first of the month it is one day of data
// multiplied by thirty, and the screen says how far in we are so that number
// can be read with the right amount of trust.
func project(m Month, now time.Time) float64 {
	if m.USD == 0 {
		return 0
	}
	days := float64(now.Day()) - 1 + float64(now.Hour())/24
	if days < 0.25 {
		days = 0.25
	}
	in := daysIn(now)
	return m.USD / days * in
}

// daysIn is how many days the month containing now has.
func daysIn(now time.Time) float64 {
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	return float64(first.AddDate(0, 1, 0).Sub(first).Hours() / 24)
}
