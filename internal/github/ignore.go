package github

import "strings"

// Ignored names checks that do not count against a pull request.
//
// Vercel is the case this exists for. Two of Kevin's pull requests sat in
// "Needs you" permanently because a preview deploy failed - which is a thing
// to look at eventually, not a thing the pull request is blocked on him for.
// The rolled-up state is one word for the whole commit, so a single noisy
// check turns a green pull request red and there is no way to see past it.
//
// Matching is case-insensitive substring, so "vercel" covers "Vercel",
// "vercel/preview" and "Vercel - Preview". That is deliberately loose: these
// names are chosen by whoever wrote the workflow and change without notice,
// and the cost of matching one check too many is a row that stays out of an
// inbox, not a lost failure - the check itself is still listed, still red, on
// the pull request's own screen.
type Ignored []string

// Match reports whether this check name is one to disregard.
func (ig Ignored) Match(name string) bool {
	if name == "" {
		return false
	}
	name = strings.ToLower(name)
	for _, p := range ig {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" && strings.Contains(name, p) {
			return true
		}
	}
	return false
}

// ignored is the current list, or none when the caller never set one.
func (c *Client) ignored() Ignored {
	if c == nil || c.Ignored == nil {
		return nil
	}
	return c.Ignored()
}

// rollupChecks turns a commit's rolled-up state into one word, disregarding
// the checks on the ignore list.
//
// The rolled-up state stays the authority whenever the context list came back
// truncated. contexts(last: N) is not ordered by state, so a failure beyond N
// would otherwise be read as a pass - and quietly calling a broken pull
// request green is the one mistake this must not make.
func rollupChecks(state string, contexts []rollupNode, total int, ig Ignored) Checks {
	base := checksFromState(state)
	if base != ChecksFail || len(ig) == 0 || total > len(contexts) {
		return base
	}

	kept := make([]rollupNode, 0, len(contexts))
	for _, n := range contexts {
		if !ig.Match(n.name()) {
			kept = append(kept, n)
		}
	}
	// Nothing left is not a reason to fall back to the rollup. Both of the
	// pull requests this was built for run only Vercel checks - one of them
	// runs nothing else at all - and "every check here is one you told me to
	// disregard" is the same answer as "no checks worth reporting".
	return foldChecks(kept)
}

// foldChecks collapses a set of rollup nodes to one word, worst first. It is
// the shared half of ghPR.checks, which applies the same rule to the list gh
// returns for a repo's pull requests.
func foldChecks(nodes []rollupNode) Checks {
	pending := false
	for _, n := range nodes {
		switch n.verdict() {
		case ChecksFail:
			return ChecksFail
		case ChecksPending:
			pending = true
		}
	}
	switch {
	case pending:
		return ChecksPending
	case len(nodes) > 0:
		return ChecksPass
	}
	return ChecksNone
}
