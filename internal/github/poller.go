package github

import (
	"context"
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// Snapshot is the whole GitHub screen at one instant: what the poller has, and
// how it went getting it.
type Snapshot struct {
	Viewer string `json:"viewer,omitempty"`
	Repos  []Repo `json:"repos"`
	Inbox  Inbox  `json:"inbox"`
	// At is when this data was read. The stale banner is (now - At), so an
	// unchanged snapshot after a failed poll keeps its original age rather
	// than pretending to be fresh.
	At time.Time `json:"at"`
	// Error and ErrorKind describe the most recent failure. They ride
	// alongside the data instead of replacing it: a phone that loses the
	// network should keep showing the last good list, dimmed.
	Error     string `json:"error,omitempty"`
	ErrorKind string `json:"errorKind,omitempty"`

	// Panes maps every repo checked out in a tmux pane right now to those
	// pane ids, filled in by the API layer. It covers repos outside the
	// watchlist too, because the inbox spans them: an issue assigned to
	// Kevin in a repo he never added still deserves its pane chip.
	Panes map[string][]string `json:"panes,omitempty"`
	// Muted are the inbox rows the phone has dismissed, lifted out of the
	// three lists above by the API layer. They ride along rather than being
	// dropped so the screen can say how many there are and put one back.
	Muted []InboxItem `json:"muted,omitempty"`
	// PanesBlocked is set when the filesystem refused to answer where a
	// pane's repo is. On macOS that is the privacy control: the service has
	// not been granted access to the folders the repos live in, so the
	// screen says so instead of quietly dropping every pane chip.
	PanesBlocked bool `json:"panesBlocked,omitempty"`
}

// ErrorKind names a failure in one word the UI can switch on.
func ErrorKind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrNoAuth):
		return "auth"
	case errors.Is(err, ErrNoGH):
		return "nogh"
	case errors.Is(err, ErrOffline):
		return "offline"
	case errors.Is(err, ErrTimeout):
		return "timeout"
	case errors.Is(err, ErrRateLimited):
		return "ratelimit"
	case errors.Is(err, ErrNotFound):
		return "notfound"
	}
	return "error"
}

// Poller keeps one Snapshot up to date for the whole server.
//
// One poller, not one per connection. Phase 2 taught this the hard way with
// pane capture: a poller per WebSocket meant two phones doubled the work and
// the battery cost. Here it would also double the API quota, so the rule is
// the same - the server polls once and everybody reads the same snapshot.
//
// Three gh calls per tick cover the entire screen: repo counts, the inbox
// searches, and notifications. Per-repo pull request lists are not polled;
// they are fetched when a repo's PRs tab is actually opened, because the
// rolled-up state the inbox already carries is what the summary rows need.
type Poller struct {
	Client   *Client
	Interval time.Duration
	// Watchlist is read fresh every tick so adding a repo takes effect on
	// the next poll without restarting anything.
	Watchlist func() []string
	// OnSnapshot fires after every poll that produced a new snapshot. The
	// WebSocket broadcast and the push watcher hang off this.
	OnSnapshot func(Snapshot)

	mu   sync.RWMutex
	snap Snapshot

	kick chan struct{}
	once sync.Once
}

func (p *Poller) interval() time.Duration {
	if p.Interval <= 0 {
		return time.Minute
	}
	return p.Interval
}

func (p *Poller) init() {
	p.once.Do(func() { p.kick = make(chan struct{}, 1) })
}

// Snapshot returns the most recent read. It never blocks on the network, so a
// page load is instant even mid-poll.
//
// Repos is always a list, never nil. Before the first poll this is the zero
// value, and a nil slice serialises as JSON null - which the screen then reads
// a length off and crashes on. "Nothing yet" is an empty list.
func (p *Poller) Snapshot() Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	snap := p.snap
	if snap.Repos == nil {
		snap.Repos = []Repo{}
	}
	return snap
}

// Kick asks for a poll now, for the refresh button and for just-added repos.
// It never blocks and never queues more than one.
func (p *Poller) Kick() {
	p.init()
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

// Run polls until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	p.init()
	t := time.NewTimer(0)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-p.kick:
			if !t.Stop() {
				select {
				case <-t.C:
				default:
				}
			}
		}

		wait := p.poll(ctx)
		t.Reset(wait)
	}
}

// poll does one read and returns how long to wait before the next one.
func (p *Poller) poll(ctx context.Context) time.Duration {
	prev := p.Snapshot()
	prevKind := prev.ErrorKind
	if prev.At.IsZero() {
		// Nothing has ever succeeded, so the first result is worth a line
		// whichever way it goes.
		prevKind = "\x00"
	}
	next := p.read(ctx, prev)

	p.mu.Lock()
	p.snap = next
	p.mu.Unlock()

	if p.OnSnapshot != nil {
		p.OnSnapshot(next)
	}

	// A poller that fails silently is a screen with no explanation on it.
	// This logs the first failure and every change of kind, not every tick,
	// so a long outage is one line rather than one a minute - and logs the
	// recovery too, which is the line that says when the data got good
	// again.
	if next.ErrorKind != prevKind {
		if next.ErrorKind == "" {
			log.Printf("github: recovered - %d repos, %d needing you",
				len(next.Repos), next.Inbox.Count())
		} else {
			log.Printf("github: %s: %s", next.ErrorKind, next.Error)
		}
	}

	// Backing off is the only useful response to a rate limit, and polling
	// through a logged-out gh just burns processes to print the same error.
	switch next.ErrorKind {
	case "ratelimit":
		return 5 * time.Minute
	case "auth", "nogh":
		return 2 * time.Minute
	}
	return p.interval()
}

// read does one full pass. A failure is reported inside the snapshot rather
// than returned, because every caller wants the carried-forward data either
// way and the kind of failure is what the screen switches on.
func (p *Poller) read(ctx context.Context, prev Snapshot) Snapshot {
	// Carry the previous data forward. Every failure below returns this
	// value, so the screen keeps its contents and only gains a banner.
	next := prev
	next.Error, next.ErrorKind = "", ""

	fail := func(err error) Snapshot {
		next.Error = err.Error()
		next.ErrorKind = ErrorKind(err)
		return next
	}

	if next.Viewer == "" {
		login, err := p.Client.Viewer(ctx)
		if err != nil {
			return fail(err)
		}
		next.Viewer = login
	}

	var list []string
	if p.Watchlist != nil {
		list = p.Watchlist()
	}
	repos, err := p.Client.Repos(ctx, list)
	if err != nil {
		return fail(err)
	}

	inbox, err := p.Client.Inbox(ctx, next.Viewer)
	if err != nil {
		return fail(err)
	}

	// Notifications are the one optional part. They add mentions and a
	// reply count; losing them should not cost the rest of the screen, so
	// a failure here is recorded and the snapshot still lands.
	mentions, replies, nErr := p.Client.Notifications(ctx, next.Viewer)
	if nErr == nil {
		inbox.NeedsYou = append(inbox.NeedsYou, mentions...)
		byRecency(inbox.NeedsYou)
		inbox.Replies = replies
	}

	next.Repos = withPRState(repos, inbox)
	next.Inbox = inbox
	next.At = time.Now()
	if nErr != nil {
		next.Error = nErr.Error()
		next.ErrorKind = ErrorKind(nErr)
	}
	return next
}

// withPRState fills each repo's "n of yours, m red" line from the inbox.
//
// The alternative is a `gh pr list` per repo per tick, which on
// apache/shardingsphere alone means pulling 22 pull requests and their 76
// check runs each - seven seconds and a lot of JSON to render one word. The
// inbox already knows which pull requests are Kevin's and which are red, so
// the summary is free.
func withPRState(repos []Repo, in Inbox) []Repo {
	mine := map[string]int{}
	red := map[string]int{}
	for _, group := range [][]InboxItem{in.NeedsYou, in.YourPRs} {
		for _, it := range group {
			if it.Kind != KindPR {
				continue
			}
			mine[it.Repo]++
			if it.blocked() {
				red[it.Repo]++
			}
		}
	}
	for i := range repos {
		repos[i].MyPRs = mine[repos[i].Full]
		repos[i].RedPRs = red[repos[i].Full]
	}
	return repos
}

// SortWatchlist orders the Repos tab: anything broken first so it cannot be
// missed, then whatever has your attention, then most recently pushed.
func SortWatchlist(rs []Repo) {
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if a.Missing != b.Missing {
			return a.Missing
		}
		if (a.RedPRs > 0) != (b.RedPRs > 0) {
			return a.RedPRs > 0
		}
		if (a.MyPRs > 0) != (b.MyPRs > 0) {
			return a.MyPRs > 0
		}
		return a.Pushed.After(b.Pushed)
	})
}

// NormalizeWatchlist drops blanks, duplicates and anything that is not
// owner/name, keeping the order Kevin added them in. It is the gate between
// the settings file and every call that builds a URL out of a repo name.
func NormalizeWatchlist(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(strings.Trim(strings.TrimSpace(s), "/"))
		if !ValidRepo(s) {
			continue
		}
		key := strings.ToLower(s)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

// Retain drops cached repos that are no longer on the watchlist.
//
// Removing a repo needs no network - the answer is already known - but the
// snapshot is only rebuilt by a poll, which takes seconds against GitHub.
// Without this the repo stays on the screen for the whole of that, so the tap
// reads as if it did nothing. Adding a repo still has to wait: there is no
// data for it yet.
func (p *Poller) Retain(list []string) {
	keep := make(map[string]bool, len(list))
	for _, r := range list {
		keep[strings.ToLower(r)] = true
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Repo, 0, len(p.snap.Repos))
	for _, r := range p.snap.Repos {
		if keep[strings.ToLower(r.Full)] {
			out = append(out, r)
		}
	}
	p.snap.Repos = out
}
