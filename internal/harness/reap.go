package harness

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"
)

// A claim outlives its loop when the loop is stopped or restarted in the
// middle of a run: measured on educenter, two issues kept by:<session> and
// wip:backend for hours after their loops were gone, looked in progress to
// every other loop, and held five more issues behind them.
//
// reapClaims takes a claim off when its loop is no longer on that item: the
// session is gone, or it is working on something else. A claim with no wip:*
// label yet may be one a loop is making right now, so it goes only when its
// session is gone - a crash can leave a lone by:<session> too (measured on
// educenter, a PR kept one after the Mac died). Every claim is checked twice,
// ReapWait apart, so a loop that started between the two reads keeps its own.

var ReapWait = 10 * time.Second

func (l *Loop) reapClaims(ctx context.Context) {
	stale := l.staleClaims(ctx)
	if len(stale) == 0 {
		return
	}
	sleep(ctx, ReapWait)
	again := l.staleClaims(ctx)
	for key, it := range stale {
		if _, still := again[key]; !still {
			continue
		}
		label := key[strings.Index(key, " ")+1:]
		l.gh.RemoveLabel(ctx, it.Number, label)
		// The wip label goes only when no live loop's claim is left on it.
		if live := liveClaims(it, again); live == 0 {
			for _, x := range it.Labels {
				if strings.HasPrefix(x.Name, "wip:") {
					l.gh.RemoveLabel(ctx, it.Number, x.Name)
				}
			}
		}
		log.Printf("released stale claim %s on #%d", label, it.Number)
	}
}

// staleClaims maps "<number> by:<session>" to its item, for every finished
// claim whose loop is not on that item now.
func (l *Loop) staleClaims(ctx context.Context) map[string]Item {
	loops, err := l.Tmux.Loops(ctx)
	if err != nil {
		return nil
	}
	on := map[string]int{}
	for _, o := range loops {
		// No role is an empty shell tmux restored after a crash, not a loop.
		if o.Role != "" {
			on[o.Name] = o.Issue
		}
	}
	out := map[string]Item{}
	for _, kind := range []string{"issue", "pr"} {
		var items []Item
		if err := l.gh.JSON(ctx, &items, kind, "list", "--state", "open", "--limit", "500",
			"--json", itemFields); err != nil {
			return nil
		}
		for k, it := range staleIn(items, on) {
			out[k] = it
		}
	}
	return out
}

// staleIn is the rule itself: a claim whose loop is gone, or a finished
// claim whose loop is on another item. on maps each loop session to the item
// it is working on.
func staleIn(items []Item, on map[string]int) map[string]Item {
	out := map[string]Item{}
	for _, it := range items {
		wip := it.HasPrefix("wip:")
		for _, x := range it.Labels {
			name, ok := strings.CutPrefix(x.Name, "by:")
			issue, alive := on[name]
			if ok && (!alive || wip && issue != it.Number) {
				out[itemKey(it.Number, x.Name)] = it
			}
		}
	}
	return out
}

func liveClaims(it Item, stale map[string]Item) int {
	n := 0
	for _, x := range it.Labels {
		if strings.HasPrefix(x.Name, "by:") {
			if _, dead := stale[itemKey(it.Number, x.Name)]; !dead {
				n++
			}
		}
	}
	return n
}

func itemKey(n int, label string) string { return strconv.Itoa(n) + " " + label }
