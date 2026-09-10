package github

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// PageSize is how many rows one screenful loads.
//
// Kevin's real repos run from 3 open issues to 186. A phone list cannot show
// 186 of anything, and infinite scroll is wrong here because it fires requests
// while the thumb is still moving. So the list pages explicitly and the screen
// opens on the filtered view, not the full one.
const PageSize = 30

// IssueFilter picks which slice of a repo's issues to load.
type IssueFilter string

const (
	// IssuesMine is assigned to me or opened by me. The repo screen opens
	// here: it turns 108 rows into 2.
	IssuesMine IssueFilter = "mine"
	// IssuesAll is every open issue, most recently updated first.
	IssuesAll IssueFilter = "all"
)

// IssuePage is one screenful, plus the cursor a Load more button would use.
type IssuePage struct {
	Issues []Issue `json:"issues"`
	// Next is empty at the end of the list. It is GitHub's own cursor, so
	// it is passed back verbatim and never parsed.
	Next string `json:"next,omitempty"`
}

// issueFields is the node selection shared by every issue query.
//
// The counts are bounded on purpose: an issue with 60 labels would otherwise
// drag 60 label objects into a payload that renders three of them.
const issueFields = `fragment issueFields on Issue {
  number
  title
  url
  createdAt
  updatedAt
  author { login }
  assignees(first: 10) { nodes { login } }
  labels(first: 10) { nodes { name color } }
  comments { totalCount }
}
`

type gqlIssue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Author is null for an issue whose account was deleted; a ghost row
	// must not take the list down with it.
	Author    *struct{ Login string } `json:"author"`
	Assignees struct {
		Nodes []struct{ Login string }
	} `json:"assignees"`
	Labels struct {
		Nodes []Label
	} `json:"labels"`
	Comments struct{ TotalCount int } `json:"comments"`
}

func (g gqlIssue) issue() Issue {
	is := Issue{
		Number:   g.Number,
		Title:    g.Title,
		Comments: g.Comments.TotalCount,
		Created:  g.CreatedAt,
		Updated:  g.UpdatedAt,
		URL:      g.URL,
		Body:     g.Body,
		Labels:   g.Labels.Nodes,
	}
	if g.Author != nil {
		is.Author = g.Author.Login
	}
	for _, a := range g.Assignees.Nodes {
		is.Assignees = append(is.Assignees, a.Login)
	}
	return is
}

type gqlIssueConn struct {
	PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
	Nodes []gqlIssue `json:"nodes"`
}

// Issues loads one page of open issues.
//
// This goes through GraphQL rather than the REST issues endpoint, and the
// reason is not style. REST returns pull requests in the same list - a PR is
// an issue there - so they have to be filtered out client side, and on
// apache/shardingsphere a page of 30 records came back as 14 issues and 16
// PRs. GraphQL's issues connection contains issues and nothing else, so a page
// of 30 is 30 rows.
//
// after is a cursor from a previous page's Next, or empty for the first page.
// viewer is the login "mine" resolves to and is ignored for IssuesAll.
func (c *Client) Issues(ctx context.Context, full string, f IssueFilter, viewer, after string) (IssuePage, error) {
	owner, name, err := SplitRepo(full)
	if err != nil {
		return IssuePage{}, err
	}
	if f == IssuesMine {
		return c.issuesMine(ctx, owner, name, viewer)
	}

	query := fmt.Sprintf(`query($owner: String!, $name: String!, $after: String) {
  repository(owner: $owner, name: $name) {
    issues(states: OPEN, first: %d, after: $after, orderBy: {field: UPDATED_AT, direction: DESC}) {
      pageInfo { hasNextPage endCursor }
      nodes { ...issueFields }
    }
  }
}
%s`, PageSize, issueFields)

	vars := map[string]string{"owner": owner, "name": name}
	if after != "" {
		vars["after"] = after
	}

	var data struct {
		Repository *struct {
			Issues gqlIssueConn `json:"issues"`
		} `json:"repository"`
	}
	if err := c.graphql(ctx, query, vars, &data); err != nil {
		return IssuePage{}, err
	}
	if data.Repository == nil {
		return IssuePage{}, ErrNotFound
	}

	conn := data.Repository.Issues
	page := IssuePage{Issues: make([]Issue, 0, len(conn.Nodes))}
	for _, n := range conn.Nodes {
		page.Issues = append(page.Issues, n.issue())
	}
	if conn.PageInfo.HasNextPage {
		page.Next = conn.PageInfo.EndCursor
	}
	return page, nil
}

// issuesMine merges "assigned to me" and "opened by me".
//
// filterBy takes one or the other, never both, so this is two connections -
// but aliased into one document, so it is still one request and one point.
//
// It does not page. On a repo where Kevin has more than 30 issues of his own,
// the answer is the All tab, not deeper paging through a list whose whole
// point is being short.
func (c *Client) issuesMine(ctx context.Context, owner, name, viewer string) (IssuePage, error) {
	if viewer == "" {
		return IssuePage{}, ErrNoAuth
	}

	conn := func(alias, filter string) string {
		return fmt.Sprintf(
			`    %s: issues(states: OPEN, first: %d, filterBy: {%s: $me}, orderBy: {field: UPDATED_AT, direction: DESC}) { pageInfo { hasNextPage endCursor } nodes { ...issueFields } }`,
			alias, PageSize, filter)
	}
	query := fmt.Sprintf(`query($owner: String!, $name: String!, $me: String!) {
  repository(owner: $owner, name: $name) {
%s
%s
  }
}
%s`, conn("assigned", "assignee"), conn("created", "createdBy"), issueFields)

	var data struct {
		Repository *struct {
			Assigned gqlIssueConn `json:"assigned"`
			Created  gqlIssueConn `json:"created"`
		} `json:"repository"`
	}
	err := c.graphql(ctx, query, map[string]string{"owner": owner, "name": name, "me": viewer}, &data)
	if err != nil {
		return IssuePage{}, err
	}
	if data.Repository == nil {
		return IssuePage{}, ErrNotFound
	}

	seen := map[int]bool{}
	var page IssuePage
	for _, conn := range []gqlIssueConn{data.Repository.Assigned, data.Repository.Created} {
		for _, n := range conn.Nodes {
			if seen[n.Number] {
				continue
			}
			seen[n.Number] = true
			page.Issues = append(page.Issues, n.issue())
		}
	}
	sort.SliceStable(page.Issues, func(i, j int) bool {
		return page.Issues[i].Updated.After(page.Issues[j].Updated)
	})
	return page, nil
}

// Issue loads one issue in full, for the detail screen.
func (c *Client) Issue(ctx context.Context, full string, number int) (Issue, error) {
	owner, name, err := SplitRepo(full)
	if err != nil {
		return Issue{}, err
	}
	if number < 1 {
		return Issue{}, fmt.Errorf("invalid issue number %d", number)
	}

	// number is a GraphQL Int, and gh's -f sends every variable as a
	// String, so it goes in the document. It has already been checked to be
	// a positive integer above.
	query := fmt.Sprintf(`query($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    issue(number: %d) { ...issueFields body }
  }
}
`, number) + issueFields

	var data struct {
		Repository *struct {
			Issue *gqlIssue `json:"issue"`
		} `json:"repository"`
	}
	vars := map[string]string{"owner": owner, "name": name}
	if err := c.graphql(ctx, query, vars, &data); err != nil {
		return Issue{}, err
	}
	if data.Repository == nil || data.Repository.Issue == nil {
		return Issue{}, ErrNotFound
	}
	return data.Repository.Issue.issue(), nil
}
