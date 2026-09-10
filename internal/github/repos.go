package github

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Viewer returns the login gh is authenticated as.
//
// Everything that means "mine" - assigned to me, opened by me, review
// requested from me - needs this, and it never changes while the process
// runs, so the caller is expected to fetch it once at startup.
func (c *Client) Viewer(ctx context.Context) (string, error) {
	var u struct {
		Login string `json:"login"`
	}
	if err := c.api(ctx, "user", &u); err != nil {
		return "", err
	}
	if u.Login == "" {
		return "", ErrNoAuth
	}
	return u.Login, nil
}

// repoBatch is how many repositories go into one GraphQL document. Well below
// anything GitHub complains about; it exists so a long watchlist cannot build
// an unbounded query string.
const repoBatch = 40

// Repos fetches the whole Repos screen in one request per 40 repos.
//
// REST cannot answer this. Its open_issues_count counts pull requests as
// issues, so the only accurate split needs a separate count of open PRs, and
// doing that per repo over REST is one request each. GraphQL aliases every
// watched repo into a single document, gives issues and pullRequests as
// separate totals, and costs one point.
//
// A repo that no longer exists comes back as a null alias with an entry in
// errors. That is reported as Missing rather than failing the whole call: one
// renamed repo must not blank the screen.
func (c *Client) Repos(ctx context.Context, fulls []string) ([]Repo, error) {
	out := make([]Repo, 0, len(fulls))
	for start := 0; start < len(fulls); start += repoBatch {
		end := min(start+repoBatch, len(fulls))
		batch, err := c.reposBatch(ctx, fulls[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

type gqlRepo struct {
	NameWithOwner string                   `json:"nameWithOwner"`
	Description   string                   `json:"description"`
	IsPrivate     bool                     `json:"isPrivate"`
	IsFork        bool                     `json:"isFork"`
	PushedAt      time.Time                `json:"pushedAt"`
	Issues        struct{ TotalCount int } `json:"issues"`
	PullRequests  struct{ TotalCount int } `json:"pullRequests"`
}

func (c *Client) reposBatch(ctx context.Context, fulls []string) ([]Repo, error) {
	var params, body strings.Builder
	vars := map[string]string{}
	owners := make([][2]string, len(fulls))

	for i, full := range fulls {
		owner, name, err := SplitRepo(full)
		if err != nil {
			return nil, err
		}
		owners[i] = [2]string{owner, name}
		fmt.Fprintf(&params, ", $o%d: String!, $n%d: String!", i, i)
		fmt.Fprintf(&body, "  r%d: repository(owner: $o%d, name: $n%d) { ...f }\n", i, i, i)
		vars[fmt.Sprintf("o%d", i)] = owner
		vars[fmt.Sprintf("n%d", i)] = name
	}

	query := "query(" + strings.TrimPrefix(params.String(), ", ") + ") {\n" + body.String() + "}\n" + repoFragment

	var data map[string]*gqlRepo
	if err := c.graphql(ctx, query, vars, &data); err != nil {
		return nil, err
	}

	repos := make([]Repo, len(fulls))
	for i, full := range fulls {
		r := Repo{Full: full, Owner: owners[i][0], Name: owners[i][1]}
		if g := data[fmt.Sprintf("r%d", i)]; g != nil {
			r.Full = g.NameWithOwner // picks up a rename
			r.Owner, r.Name, _ = strings.Cut(g.NameWithOwner, "/")
			r.Description = g.Description
			r.Private = g.IsPrivate
			r.Fork = g.IsFork
			r.Pushed = g.PushedAt
			r.Issues = g.Issues.TotalCount
			r.PRs = g.PullRequests.TotalCount
		} else {
			r.Missing = true
		}
		repos[i] = r
	}
	return repos, nil
}

const repoFragment = `fragment f on Repository {
  nameWithOwner
  description
  isPrivate
  isFork
  pushedAt
  issues(states: OPEN) { totalCount }
  pullRequests(states: OPEN) { totalCount }
}
`

// ── the Add repo picker ───────────────────────────────────────────────────

// Mine lists repositories Kevin can already see, most recently pushed first.
//
// This is what the Add repo sheet opens on, so it has to be the right list
// without him typing anything. affiliation covers the three ways a repo shows
// up in his world: his own, ones he was added to, and ones that belong to an
// org he is in.
func (c *Client) Mine(ctx context.Context, limit int) ([]Repo, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	path := fmt.Sprintf(
		"user/repos?sort=pushed&direction=desc&per_page=%d&affiliation=owner,collaborator,organization_member",
		limit)

	var raw []struct {
		FullName    string    `json:"full_name"`
		Description string    `json:"description"`
		Private     bool      `json:"private"`
		Fork        bool      `json:"fork"`
		PushedAt    time.Time `json:"pushed_at"`
	}
	if err := c.api(ctx, path, &raw); err != nil {
		return nil, err
	}

	out := make([]Repo, 0, len(raw))
	for _, r := range raw {
		owner, name, err := SplitRepo(r.FullName)
		if err != nil {
			continue
		}
		out = append(out, Repo{
			Full: r.FullName, Owner: owner, Name: name,
			Description: r.Description, Private: r.Private,
			Fork: r.Fork, Pushed: r.PushedAt,
		})
	}
	return out, nil
}

// SearchRepos backs the picker's search box.
//
// Mine cannot find apache/shardingsphere - he is not a member of apache - and
// a repo he only watches is exactly the case the search box exists for. This
// is the one call in the package on GitHub's search quota (30/minute), which
// is fine for something a person types into and wrong for anything polled.
func (c *Client) SearchRepos(ctx context.Context, query string, limit int) ([]Repo, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	path := fmt.Sprintf("search/repositories?per_page=%d&q=%s", limit, urlQuery(query))

	var resp struct {
		Items []struct {
			FullName    string    `json:"full_name"`
			Description string    `json:"description"`
			Private     bool      `json:"private"`
			Fork        bool      `json:"fork"`
			PushedAt    time.Time `json:"pushed_at"`
			Stars       int       `json:"stargazers_count"`
		} `json:"items"`
	}
	if err := c.api(ctx, path, &resp); err != nil {
		return nil, err
	}

	out := make([]Repo, 0, len(resp.Items))
	for _, r := range resp.Items {
		owner, name, err := SplitRepo(r.FullName)
		if err != nil {
			continue
		}
		out = append(out, Repo{
			Full: r.FullName, Owner: owner, Name: name,
			Description: r.Description, Private: r.Private,
			Fork: r.Fork, Pushed: r.PushedAt,
		})
	}
	return out, nil
}
