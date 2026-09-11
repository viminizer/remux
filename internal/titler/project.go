package titler

import "strings"

// maxProject is how long a project prefix may be.
//
// It is short on purpose. The prefix is the part you scan past to reach the
// words that tell two panes apart, so every character it takes is one the task
// does not get - and on the phone the row is about two hundred pixels wide.
const maxProject = 12

// Short picks a display name for each repo, keyed by the repo as the matcher
// reports it ("owner/name").
//
// The rule is to drop the longest run of leading segments a repo shares with
// another repo in the same workspace, because that run is precisely the part
// that cannot tell them apart:
//
//	seoul-wedding-api     ┐ share "seoul-wedding"  ->  api
//	seoul-wedding-client  ┘                        ->  client
//	seoulwomen-react      ┐ share "seoulwomen"     ->  react
//	seoulwomen-docs       ┘                        ->  docs
//	shortlist             no sibling               ->  shortlist
//
// This is deliberately arithmetic rather than a model's judgement, and that is
// the whole point. The prefix has to be byte-identical on every pass: it is
// the fixed left edge a reader's eye skips, and a column that shifts between
// "api" and "wedding-api" is worse than one with a duller name in it. It is
// also a constant - a repo's name does not change - so paying a model to
// re-derive it every ninety seconds would be paying for the same answer
// forever, with drift thrown in.
//
// The rule was checked against the names Kevin had already typed by hand for
// these panes. It reproduces them: he called them "react" and "client" too.
func Short(repos []string) map[string]string {
	names := make(map[string][]string, len(repos))
	for _, r := range repos {
		if n := leaf(r); n != "" {
			names[r] = strings.Split(n, "-")
		}
	}

	out := make(map[string]string, len(names))
	for repo, segs := range names {
		drop := 0
		for other, oseg := range names {
			if other == repo {
				continue
			}
			if n := common(segs, oseg); n > drop {
				drop = n
			}
		}
		// Never drop the whole name. A repo whose every segment is shared -
		// "shortlist" next to "shortlist-server" - still has to be called
		// something, and its last segment is the only honest answer.
		if drop >= len(segs) {
			drop = len(segs) - 1
		}
		out[repo] = clamp(segs[drop:])
	}
	return out
}

// leaf is the name half of "owner/name", or the whole string when there is no
// owner.
func leaf(repo string) string {
	if i := strings.LastIndexByte(repo, '/'); i >= 0 {
		return repo[i+1:]
	}
	return repo
}

// common counts the leading segments two names share.
func common(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// clamp keeps the result inside maxProject, preferring to lose whole segments
// over cutting a word in half - "wedding-api" reads, "wedding-ap" does not.
func clamp(segs []string) string {
	s := strings.Join(segs, "-")
	for len(s) > maxProject && len(segs) > 1 {
		segs = segs[1:]
		s = strings.Join(segs, "-")
	}
	if len(s) > maxProject {
		s = s[:maxProject]
	}
	return s
}
