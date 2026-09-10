package github

import (
	"fmt"
	"sort"
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
	Thread    []Comment `json:"threadComments,omitempty"` // detail screen only
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
	// Runs is the individual checks, filled in only by PR(). A list never
	// carries them: apache/shardingsphere runs 76 checks per pull request
	// and 22 open ones, which is 1600 rows of detail to render 22 words.
	Runs []CheckRun `json:"runs,omitempty"`
	// Comments is the tail of the thread, newest last, filled in only by
	// PR() and Issue().
	Comments []Comment `json:"threadComments,omitempty"`
}

// CheckRun is one line of the Checks section on the detail screen.
type CheckRun struct {
	Name  string `json:"name"`
	State Checks `json:"state"`
	Took  string `json:"took,omitempty"` // "4m 12s", or "running"
	URL   string `json:"url,omitempty"`
}

// Comment is one message in a thread, trimmed for a phone.
type Comment struct {
	Author  string    `json:"author"`
	Body    string    `json:"body"`
	At      time.Time `json:"at"`
	Trimmed bool      `json:"trimmed,omitempty"`
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
	StatusCheckRollup []rollupNode `json:"statusCheckRollup"`
	Comments          []struct {
		Author    struct{ Login string } `json:"author"`
		Body      string                 `json:"body"`
		CreatedAt time.Time              `json:"createdAt"`
	} `json:"comments"`
}

type rollupNode struct {
	Name        string    `json:"name"`
	Context     string    `json:"context"`    // StatusContext has context, not name
	Status      string    `json:"status"`     // CheckRun: QUEUED/IN_PROGRESS/COMPLETED
	Conclusion  string    `json:"conclusion"` // CheckRun
	State       string    `json:"state"`      // StatusContext: SUCCESS/FAILURE/PENDING
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt"`
	DetailsURL  string    `json:"detailsUrl"`
	TargetURL   string    `json:"targetUrl"`
}

// verdict is the one word this node contributes to the rollup.
func (n rollupNode) verdict() Checks {
	v := n.Conclusion
	if v == "" {
		v = n.State
	}
	switch strings.ToUpper(v) {
	case "FAILURE", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE", "ERROR", "CANCELLED":
		return ChecksFail
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		if n.Status != "" && strings.ToUpper(n.Status) != "COMPLETED" {
			return ChecksPending
		}
		return ChecksPass
	}
	return ChecksPending
}

// name is what the check calls itself. A CheckRun has a name, a StatusContext
// has a context, and only one of the two is ever set.
func (n rollupNode) name() string {
	if n.Name != "" {
		return n.Name
	}
	return n.Context
}

func (n rollupNode) run() CheckRun {
	name := n.name()
	url := n.DetailsURL
	if url == "" {
		url = n.TargetURL
	}
	return CheckRun{Name: name, State: n.verdict(), Took: n.took(), URL: url}
}

// took is how long the check ran, or "running" while it still is.
func (n rollupNode) took() string {
	if n.StartedAt.IsZero() {
		return ""
	}
	if n.CompletedAt.IsZero() {
		return "running"
	}
	d := n.CompletedAt.Sub(n.StartedAt).Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
}

func (g ghPR) pr(ig Ignored) PR {
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
		Checks:    g.checks(ig),
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
// Checks on the ignore list are left out of the verdict, but not out of the
// detail screen: a pull request that is only red because of a preview deploy
// should read as green in a list and still show the red line when opened.
func (g ghPR) checks(ig Ignored) Checks {
	kept := make([]rollupNode, 0, len(g.StatusCheckRollup))
	for _, c := range g.StatusCheckRollup {
		if !ig.Match(c.name()) {
			kept = append(kept, c)
		}
	}
	// A pull request whose every check is on the ignore list reports the same
	// as one with no checks at all, which is what it now effectively has.
	return foldChecks(kept)
}

// detail adds what only the single-item screen shows: every check by name,
// and the tail of the conversation.
func (g ghPR) detail(ig Ignored) PR {
	p := g.pr(ig)
	for _, n := range g.StatusCheckRollup {
		p.Runs = append(p.Runs, n.run())
	}
	// Failures first. On a 76-check pull request the two that are red are
	// the entire reason the screen was opened, and scrolling past 74 green
	// lines to find them is not a phone interaction.
	sort.SliceStable(p.Runs, func(i, j int) bool {
		return checkRank(p.Runs[i].State) < checkRank(p.Runs[j].State)
	})
	for _, c := range g.Comments {
		p.Comments = append(p.Comments, trimComment(c.Author.Login, c.Body, c.CreatedAt))
	}
	p.Comments = lastComments(p.Comments)
	return p
}

func checkRank(c Checks) int {
	switch c {
	case ChecksFail:
		return 0
	case ChecksPending:
		return 1
	}
	return 2
}

// commentBudget is how much of one comment body survives to the phone.
//
// A review comment in Kevin's repos can be three kilobytes of markdown. The
// detail screen shows the last few messages so he can tell what happened, not
// so he can read a full review on a 390px screen - GitHub itself is one tap
// away for that.
const commentBudget = 600

func trimComment(author, body string, at time.Time) Comment {
	c := Comment{Author: author, Body: strings.TrimSpace(body), At: at}
	if len(c.Body) > commentBudget {
		c.Body = strings.TrimSpace(c.Body[:commentBudget])
		c.Trimmed = true
	}
	return c
}

// lastComments keeps the newest few. The tail is what tells you where a
// thread stands; the head is history.
func lastComments(all []Comment) []Comment {
	const keep = 4
	if len(all) <= keep {
		return all
	}
	return all[len(all)-keep:]
}
