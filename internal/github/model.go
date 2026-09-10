package github

import (
	"strings"
	"time"
)

// Repo is one watched repository as the Repos screen draws it.
type Repo struct {
	Full        string    `json:"full"` // owner/name
	Owner       string    `json:"owner"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Private     bool      `json:"private"`
	Fork        bool      `json:"fork"`
	Issues      int       `json:"issues"` // open issues, pull requests excluded
	PRs         int       `json:"prs"`    // open pull requests
	Pushed      time.Time `json:"pushed,omitempty"`

	// MyPRs and RedPRs summarise the pull requests in this repo that Kevin
	// is personally on the hook for, and how many of those are blocked.
	// They come from the inbox, not from a per-repo list.
	MyPRs  int `json:"myPrs,omitempty"`
	RedPRs int `json:"redPrs,omitempty"`

	// Missing is set when GitHub has no such repository any more: renamed,
	// deleted, or turned private. The watchlist keeps the row and marks it
	// rather than dropping it silently, because only Kevin can decide
	// whether to fix the name or remove it.
	Missing bool `json:"missing,omitempty"`

	// Panes is filled in by the API layer: the tmux panes sitting inside a
	// checkout of this repo. It is what turns a list of GitHub rows into
	// something tied to the work already open on the laptop.
	Panes []string `json:"panes,omitempty"`
}

// Label is the subset of a GitHub label the UI draws: a name and a colour.
type Label struct {
	Name  string `json:"name"`
	Color string `json:"color"` // 6 hex digits, no leading #
}

// Issue is one open issue.
//
// Comments is the count, not the thread. A row shows how much discussion has
// happened; the detail screen fetches the thread when it is opened.
type Issue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Author    string    `json:"author"`
	Assignees []string  `json:"assignees,omitempty"`
	Labels    []Label   `json:"labels,omitempty"`
	Comments  int       `json:"comments"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	URL       string    `json:"url"`
	Body      string    `json:"body,omitempty"` // detail screen only
}

// Checks summarises a pull request's status check rollup into the one word a
// row has space for.
type Checks string

const (
	ChecksNone    Checks = ""        // no checks configured, or none have started
	ChecksPass    Checks = "pass"    //
	ChecksFail    Checks = "fail"    // at least one hard failure
	ChecksPending Checks = "pending" // still running
)

// PR is one open pull request.
//
// An issue row answers "what is this". A PR row answers "can this merge", so
// it carries a different set of fields: checks, review decision, conflicts.
type PR struct {
	Number    int     `json:"number"`
	Title     string  `json:"title"`
	Author    string  `json:"author"`
	Draft     bool    `json:"draft"`
	Labels    []Label `json:"labels,omitempty"`
	Base      string  `json:"base"`
	Head      string  `json:"head"`
	Additions int     `json:"additions"`
	Deletions int     `json:"deletions"`
	Checks    Checks  `json:"checks,omitempty"`
	// Review is GitHub's reviewDecision: "APPROVED", "CHANGES_REQUESTED",
	// "REVIEW_REQUIRED", or empty when the repo requires no review.
	Review string `json:"review,omitempty"`
	// Conflicts is true only when GitHub has finished computing mergeability
	// and says it conflicts. An unknown result is not a conflict.
	Conflicts bool `json:"conflicts"`
	// Reviewers is who has been asked to review and has not yet. It is what
	// "waiting on me" is decided from, so the filter can switch without a
	// second request.
	Reviewers []string  `json:"reviewers,omitempty"`
	Updated   time.Time `json:"updated"`
	URL       string    `json:"url"`
	Body      string    `json:"body,omitempty"` // detail screen only
}

// ── decoding ──────────────────────────────────────────────────────────────

// ghPR is the shape of one item from `gh pr list --json ...`.
//
// gh answers this from GraphQL, which is why statusCheckRollup,
// reviewDecision and mergeable arrive in the same call. The REST pulls
// endpoint has none of the three.
type ghPR struct {
	Number         int                    `json:"number"`
	Title          string                 `json:"title"`
	Body           string                 `json:"body"`
	URL            string                 `json:"url"`
	IsDraft        bool                   `json:"isDraft"`
	BaseRefName    string                 `json:"baseRefName"`
	HeadRefName    string                 `json:"headRefName"`
	Additions      int                    `json:"additions"`
	Deletions      int                    `json:"deletions"`
	Mergeable      string                 `json:"mergeable"`
	ReviewDecision string                 `json:"reviewDecision"`
	UpdatedAt      time.Time              `json:"updatedAt"`
	Author         struct{ Login string } `json:"author"`
	Labels         []Label                `json:"labels"`
	ReviewRequests []struct {
		Login string `json:"login"` // empty for a team request
		Name  string `json:"name"`  // team slug lands here
	} `json:"reviewRequests"`
	StatusCheckRollup []struct {
		Status     string `json:"status"`     // CheckRun: QUEUED/IN_PROGRESS/COMPLETED
		Conclusion string `json:"conclusion"` // CheckRun
		State      string `json:"state"`      // StatusContext: SUCCESS/FAILURE/PENDING/ERROR
	} `json:"statusCheckRollup"`
}

func (g ghPR) pr() PR {
	return PR{
		Number:    g.Number,
		Title:     g.Title,
		Author:    g.Author.Login,
		Draft:     g.IsDraft,
		Labels:    g.Labels,
		Base:      g.BaseRefName,
		Head:      g.HeadRefName,
		Additions: g.Additions,
		Deletions: g.Deletions,
		Checks:    g.checks(),
		Review:    g.ReviewDecision,
		Conflicts: g.Mergeable == "CONFLICTING",
		Reviewers: g.reviewers(),
		Updated:   g.UpdatedAt,
		URL:       g.URL,
		Body:      g.Body,
	}
}

// reviewers flattens the pending review requests to logins. A request aimed
// at a team has no login, and the team slug is the closest thing to a name it
// has.
func (g ghPR) reviewers() []string {
	var out []string
	for _, r := range g.ReviewRequests {
		if r.Login != "" {
			out = append(out, r.Login)
		} else if r.Name != "" {
			out = append(out, r.Name)
		}
	}
	return out
}

// checks collapses the rollup to one verdict, worst first.
//
// The two node types in the rollup report differently: a CheckRun has a status
// and, once complete, a conclusion; a StatusContext has only a state. Both are
// folded into the same three words.
//
// Neutral and skipped count as passing. A required check that was skipped
// blocks the merge, but the merge button says so far better than a red dot on
// a phone would, and calling every skipped optional check a failure would make
// the colour useless.
func (g ghPR) checks() Checks {
	pending := false
	for _, c := range g.StatusCheckRollup {
		verdict := c.Conclusion
		if verdict == "" {
			verdict = c.State
		}
		switch strings.ToUpper(verdict) {
		case "FAILURE", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE", "ERROR":
			return ChecksFail
		case "CANCELLED":
			return ChecksFail
		case "SUCCESS", "NEUTRAL", "SKIPPED":
		default:
			// QUEUED, IN_PROGRESS, PENDING, WAITING, REQUESTED, or a
			// CheckRun that has not concluded yet.
			pending = true
		}
		if c.Status != "" && strings.ToUpper(c.Status) != "COMPLETED" {
			pending = true
		}
	}
	switch {
	case pending:
		return ChecksPending
	case len(g.StatusCheckRollup) > 0:
		return ChecksPass
	}
	return ChecksNone
}
