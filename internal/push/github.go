package push

import (
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"

	gh "github.com/viminizer/remux/internal/github"
)

// GitHubWatcher turns snapshots into notifications.
//
// It follows the same rule as the pane watcher, and for the same reason: fire
// on a *transition*, never on a state. A pull request that has been red since
// yesterday is not news, and re-announcing it every minute would teach Kevin
// to swipe these away without reading them.
//
// There is no polling here at all. The GitHub poller already reads once for
// the whole server and calls this with each snapshot, so a notification costs
// nothing beyond the read that was happening anyway.
type GitHubWatcher struct {
	Store *Store
	// Enabled gates the whole thing, read fresh so the Settings toggle
	// takes effect immediately.
	Enabled func() bool

	mu    sync.Mutex
	seen  map[string]string // repo#number -> the state last announced
	first bool              // has a baseline been taken yet
}

func NewGitHubWatcher(store *Store) *GitHubWatcher {
	return &GitHubWatcher{Store: store, seen: map[string]string{}}
}

// OnSnapshot is wired to the poller. It is safe to call before anything is
// subscribed and while the feature is switched off.
func (w *GitHubWatcher) OnSnapshot(snap gh.Snapshot) {
	if w.Store == nil || w.Store.Count() == 0 {
		return
	}
	if w.Enabled != nil && !w.Enabled() {
		return
	}
	for _, n := range w.decide(snap) {
		w.send(n)
	}
}

// decide is the whole rule, split out so it can be tested without a push
// store: what in this snapshot is new enough to be worth a notification.
func (w *GitHubWatcher) decide(snap gh.Snapshot) []notice {
	// A failed poll carries the previous data forward, so treating it as
	// fresh would re-announce everything the moment GitHub came back.
	if snap.ErrorKind != "" || snap.At.IsZero() {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// The first snapshot after a restart is a baseline, not news. Without
	// this, restarting the service announces every red check you already
	// knew about.
	baseline := !w.first
	w.first = true

	var fire []notice
	live := map[string]bool{}
	for _, it := range snap.Inbox.NeedsYou {
		key := fmt.Sprintf("%s#%d", it.Repo, it.Number)
		live[key] = true
		state := reason(it)
		if state == "" || w.seen[key] == state {
			continue
		}
		w.seen[key] = state
		if !baseline {
			fire = append(fire, notice{item: it, state: state})
		}
	}
	// Forget anything that has left the list, so a pull request that goes
	// green and then red again is announced the second time too.
	for key := range w.seen {
		if !live[key] {
			delete(w.seen, key)
		}
	}
	return fire
}

type notice struct {
	item  gh.InboxItem
	state string
}

// reason is the one line that says why this needs a person, and doubles as the
// change key: a pull request that goes from red checks to a conflict has
// genuinely changed and is worth saying again.
func reason(it gh.InboxItem) string {
	switch {
	case it.Kind == gh.KindMention:
		return "mentioned you"
	case it.Checks == gh.ChecksFail:
		return "checks failed"
	case it.Conflicts:
		return "has conflicts"
	case it.Review == "CHANGES_REQUESTED":
		return "changes requested"
	case it.Kind == gh.KindPR:
		// It reached Needs you through a review request.
		return "wants your review"
	}
	return ""
}

func (w *GitHubWatcher) send(n notice) {
	it := n.item
	err := w.Store.Send(Payload{
		Title: fmt.Sprintf("%s #%d", it.Repo, it.Number),
		Body:  n.state + " · " + it.Title,
		Tag:   fmt.Sprintf("gh:%s#%d", it.Repo, it.Number),
		// Straight to the item, not to the workspace. The notification
		// already found the thing; making the reader hunt for it again
		// would waste the one advantage it has.
		Route: fmt.Sprintf("#/gh/%s/%s/%d",
			itemPath(it), url.PathEscape(it.Repo), it.Number),
	})
	if err != nil {
		log.Printf("push github: %v", err)
	}
}

// itemPath matches the routes in web/src/router.ts.
//
// A mention does not say which of the two it is - /notifications reports the
// subject type but the row is just "someone said your name" - so the URL
// decides, since GitHub spells the two differently there.
func itemPath(it gh.InboxItem) string {
	if it.Kind == gh.KindPR || strings.Contains(it.URL, "/pull/") {
		return "pr"
	}
	return "i"
}
