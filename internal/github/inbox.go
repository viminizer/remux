package github

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// InboxKind is what a row is, which decides its glyph and its chips.
type InboxKind string

const (
	KindIssue   InboxKind = "issue"
	KindPR      InboxKind = "pr"
	KindMention InboxKind = "mention"
)

// InboxItem is one row. It is deliberately one flat type rather than issue and
// PR variants: the inbox interleaves them, and the fields a PR does not have
// simply stay zero.
type InboxItem struct {
	Kind    InboxKind `json:"kind"`
	Repo    string    `json:"repo"`
	Number  int       `json:"number"`
	Title   string    `json:"title"`
	URL     string    `json:"url"`
	Updated time.Time `json:"updated"`
	Labels  []Label   `json:"labels,omitempty"`

	Draft     bool   `json:"draft,omitempty"`
	Checks    Checks `json:"checks,omitempty"`
	Review    string `json:"review,omitempty"`
	Conflicts bool   `json:"conflicts,omitempty"`

	// Panes is filled in by the API layer from the repo, so a row can offer
	// "open the pane already working on this".
	Panes []string `json:"panes,omitempty"`
}

// Inbox is the answer to "what is actually waiting for me", in the order the
// screen renders it.
type Inbox struct {
	// NeedsYou is anything blocked on a person: your PR with red checks, a
	// conflict, changes requested, a review someone asked you for, or a
	// mention. This is what the drawer badge counts.
	NeedsYou []InboxItem `json:"needsYou"`
	// Assigned is issues with your name on them that are not already above.
	Assigned []InboxItem `json:"assigned"`
	// YourPRs is your open pull requests that are not already above.
	YourPRs []InboxItem `json:"yourPRs"`
	// Replies is how many unread notifications are just activity on threads
	// you opened. It is a count and not a list on purpose: 14 of Kevin's 15
	// unread notifications were this, and listing them buries everything
	// that actually needs him.
	Replies int `json:"replies"`
}

// Count is the badge number: everything that needs a person.
func (in Inbox) Count() int { return len(in.NeedsYou) }

// inboxQuery pulls the three "mine" searches in one document.
//
// GitHub's search is the only thing that answers "assigned to me anywhere"
// without one request per repository, and GraphQL lets all three run as one
// request. The inline fragments are needed because a search of type ISSUE
// returns a union of Issue and PullRequest.
//
// statusCheckRollup is reached through the head commit rather than the
// pull request, because the field only exists on Commit. It gives one rolled
// up state, which is exactly what a row has room for.
const inboxQuery = `query($assigned: String!, $mine: String!, $review: String!) {
  assigned: search(query: $assigned, type: ISSUE, first: 50) { nodes { __typename ...issueBits } }
  mine:     search(query: $mine,     type: ISSUE, first: 50) { nodes { __typename ...prBits } }
  review:   search(query: $review,   type: ISSUE, first: 50) { nodes { __typename ...prBits } }
}
fragment issueBits on Issue {
  number title url updatedAt
  repository { nameWithOwner }
  labels(first: 5) { nodes { name color } }
}
fragment prBits on PullRequest {
  number title url updatedAt isDraft mergeable reviewDecision
  repository { nameWithOwner }
  labels(first: 5) { nodes { name color } }
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
}
`

type searchNode struct {
	Typename       string    `json:"__typename"`
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	URL            string    `json:"url"`
	UpdatedAt      time.Time `json:"updatedAt"`
	IsDraft        bool      `json:"isDraft"`
	Mergeable      string    `json:"mergeable"`
	ReviewDecision string    `json:"reviewDecision"`
	Repository     struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Labels struct {
		Nodes []Label `json:"nodes"`
	} `json:"labels"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

func (n searchNode) item() InboxItem {
	it := InboxItem{
		Kind:      KindIssue,
		Repo:      n.Repository.NameWithOwner,
		Number:    n.Number,
		Title:     n.Title,
		URL:       n.URL,
		Updated:   n.UpdatedAt,
		Labels:    n.Labels.Nodes,
		Draft:     n.IsDraft,
		Review:    n.ReviewDecision,
		Conflicts: n.Mergeable == "CONFLICTING",
	}
	if n.Typename == "PullRequest" {
		it.Kind = KindPR
	}
	if len(n.Commits.Nodes) > 0 {
		if r := n.Commits.Nodes[0].Commit.StatusCheckRollup; r != nil {
			it.Checks = checksFromState(r.State)
		}
	}
	return it
}

// checksFromState maps a rolled-up StatusState to the same three words the
// per-check rollup in model.go produces.
func checksFromState(state string) Checks {
	switch strings.ToUpper(state) {
	case "SUCCESS":
		return ChecksPass
	case "FAILURE", "ERROR":
		return ChecksFail
	case "PENDING", "EXPECTED":
		return ChecksPending
	}
	return ChecksNone
}

func (it InboxItem) blocked() bool {
	return it.Checks == ChecksFail || it.Conflicts || it.Review == "CHANGES_REQUESTED"
}

// Inbox builds the whole screen for one login.
//
// It is deliberately not scoped to the watchlist. The watchlist answers "what
// am I keeping an eye on"; the inbox answers "what is waiting for me", and an
// issue assigned to Kevin in a repo he never added is exactly the thing he
// would want to find out about here.
func (c *Client) Inbox(ctx context.Context, viewer string) (Inbox, error) {
	if viewer == "" {
		return Inbox{}, ErrNoAuth
	}
	vars := map[string]string{
		"assigned": "is:open is:issue assignee:" + viewer,
		"mine":     "is:open is:pr author:" + viewer,
		"review":   "is:open is:pr review-requested:" + viewer,
	}

	var data struct {
		Assigned struct{ Nodes []searchNode } `json:"assigned"`
		Mine     struct{ Nodes []searchNode } `json:"mine"`
		Review   struct{ Nodes []searchNode } `json:"review"`
	}
	if err := c.graphql(ctx, inboxQuery, vars, &data); err != nil {
		return Inbox{}, err
	}

	var in Inbox
	seen := map[string]bool{}
	take := func(it InboxItem, into *[]InboxItem) {
		key := it.Repo + "#" + strconv.Itoa(it.Number)
		if seen[key] {
			return
		}
		seen[key] = true
		*into = append(*into, it)
	}

	// Order matters: a row is claimed by the most urgent bucket that wants
	// it, and the dedupe above keeps it from appearing twice. A PR you
	// opened whose checks are red belongs under Needs you, not under Your
	// open PRs where it would read as fine.
	for _, n := range data.Review.Nodes {
		take(n.item(), &in.NeedsYou)
	}
	for _, n := range data.Mine.Nodes {
		if it := n.item(); it.blocked() {
			take(it, &in.NeedsYou)
		}
	}
	for _, n := range data.Mine.Nodes {
		take(n.item(), &in.YourPRs)
	}
	for _, n := range data.Assigned.Nodes {
		take(n.item(), &in.Assigned)
	}

	for _, s := range []*[]InboxItem{&in.NeedsYou, &in.Assigned, &in.YourPRs} {
		byRecency(*s)
	}
	return in, nil
}

func byRecency(items []InboxItem) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Updated.After(items[j].Updated)
	})
}

// ── notifications ─────────────────────────────────────────────────────────

// Notifications adds the two things search cannot see: mentions, and the
// volume of replies on threads Kevin opened.
//
// It is a separate call because /notifications has no GraphQL equivalent, and
// a separate concern because it is the only part of the screen whose contents
// depend on what he has already read.
func (c *Client) Notifications(ctx context.Context, viewer string) (mentions []InboxItem, replies int, err error) {
	var raw []struct {
		Reason     string    `json:"reason"`
		Unread     bool      `json:"unread"`
		UpdatedAt  time.Time `json:"updated_at"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Subject struct {
			Title string `json:"title"`
			URL   string `json:"url"`
			Type  string `json:"type"`
		} `json:"subject"`
	}
	if err := c.api(ctx, "notifications?per_page=50", &raw); err != nil {
		return nil, 0, err
	}

	for _, n := range raw {
		if !n.Unread {
			continue
		}
		switch n.Reason {
		case "mention", "team_mention":
			num, web := subjectNumber(n.Subject.URL, n.Repository.FullName, n.Subject.Type)
			if num == 0 {
				continue
			}
			mentions = append(mentions, InboxItem{
				Kind:    KindMention,
				Repo:    n.Repository.FullName,
				Number:  num,
				Title:   n.Subject.Title,
				URL:     web,
				Updated: n.UpdatedAt,
			})
		case "author":
			replies++
		}
	}
	byRecency(mentions)
	return mentions, replies, nil
}

// subjectNumber turns an API subject URL into the issue number and the web
// URL a person can actually open.
//
//	https://api.github.com/repos/o/n/pulls/415  ->  415, https://github.com/o/n/pull/415
func subjectNumber(apiURL, repo, kind string) (int, string) {
	i := strings.LastIndexByte(apiURL, '/')
	if i < 0 || repo == "" {
		return 0, ""
	}
	num, err := strconv.Atoi(apiURL[i+1:])
	if err != nil || num < 1 {
		return 0, ""
	}
	path := "issues"
	if kind == "PullRequest" {
		path = "pull"
	}
	return num, fmt.Sprintf("https://github.com/%s/%s/%d", repo, path, num)
}
