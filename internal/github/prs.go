package github

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// prFields is what one PR row needs.
//
// Three of these - statusCheckRollup, reviewDecision, mergeable - do not exist
// on the REST pulls endpoint at any page size. gh answers `pr list` from
// GraphQL, so asking for them costs nothing extra, and that is the whole
// reason this file shells out to `gh pr list` rather than calling c.api.
//
// comments is deliberately absent. gh has no comment count, only the full
// thread, and pulling every comment body for every open PR to render one
// number on a row is not a trade worth making.
var prFields = strings.Join([]string{
	"number", "title", "url", "isDraft", "author", "labels",
	"baseRefName", "headRefName", "additions", "deletions",
	"mergeable", "reviewDecision", "statusCheckRollup", "reviewRequests",
	"updatedAt",
}, ",")

// PRs lists every open pull request in a repo, most recently updated first.
//
// This deliberately fetches the whole list rather than the filtered one. A PR
// list is short - 22 is the largest across Kevin's repos - and holding all of
// it means the Yours/All toggle is instant instead of a round trip, and the
// same payload answers "is anything red" for the drawer badge.
func (c *Client) PRs(ctx context.Context, full string, limit int) ([]PR, error) {
	if _, _, err := SplitRepo(full); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}

	out, err := c.run(ctx, "pr", "list",
		"-R", full,
		"--state", "open",
		"--limit", strconv.Itoa(limit),
		"--json", prFields)
	if err != nil {
		return nil, err
	}

	var raw []ghPR
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("gh pr list %s: bad json: %w", full, err)
	}

	// Hoisted: Ignored reaches into the server for the check list and copies
	// a slice, and it cannot change while this loop runs.
	ig := c.ignored()
	prs := make([]PR, len(raw))
	for i, g := range raw {
		prs[i] = g.pr(ig)
	}
	sort.SliceStable(prs, func(i, j int) bool { return prs[i].Updated.After(prs[j].Updated) })
	return prs, nil
}

// PR loads one pull request in full, for the detail screen.
func (c *Client) PR(ctx context.Context, full string, number int) (PR, error) {
	if _, _, err := SplitRepo(full); err != nil {
		return PR{}, err
	}
	if number < 1 {
		return PR{}, fmt.Errorf("invalid pr number %d", number)
	}

	// body and comments are only asked for here. Adding them to the list
	// query would pull every review on every open pull request.
	out, err := c.run(ctx, "pr", "view", strconv.Itoa(number),
		"-R", full, "--json", prFields+",body,comments")
	if err != nil {
		return PR{}, err
	}
	var g ghPR
	if err := json.Unmarshal(out, &g); err != nil {
		return PR{}, fmt.Errorf("gh pr view %s#%d: bad json: %w", full, number, err)
	}
	return g.detail(c.ignored()), nil
}

// Mine reports whether this PR is one Kevin has to do something about: he
// opened it, or somebody asked him to review it.
func (p PR) Mine(viewer string) bool {
	if viewer == "" {
		return false
	}
	if strings.EqualFold(p.Author, viewer) {
		return true
	}
	for _, r := range p.Reviewers {
		if strings.EqualFold(r, viewer) {
			return true
		}
	}
	return false
}

// Blocked reports whether a PR needs a human before it can merge. It is what
// the drawer badge counts and what a push notification fires on.
func (p PR) Blocked() bool {
	return p.Checks == ChecksFail || p.Conflicts || p.Review == "CHANGES_REQUESTED"
}
