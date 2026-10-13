package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// graph uses the GraphQL budget directly; dependency polling never spills into REST.
func (g GH) graph(ctx context.Context, query string) (json.RawMessage, error) {
	if until := g.budget().Graph; until.After(time.Now()) {
		return nil, &githubWait{until, "GraphQL"}
	}
	out, err := g.raw(ctx, "api", "graphql", "--include", "-f", "query="+query)
	body, headers := apiResponse(out)
	if secondaryRateError(err) {
		return nil, g.pauseSecondary(headers)
	}
	if rateError(err) || err != nil && headers["x-ratelimit-remaining"] == "0" {
		until := retryAt(headers, time.Now())
		g.setBudget("GraphQL", until)
		return nil, &githubWait{until, "GraphQL"}
	}
	if err != nil {
		return nil, err
	}
	var response struct {
		Data   json.RawMessage
		Errors []struct{ Message string }
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if len(response.Errors) > 0 {
		return nil, fmt.Errorf("GitHub GraphQL: %s", response.Errors[0].Message)
	}
	if len(response.Data) == 0 || string(response.Data) == "null" {
		return nil, fmt.Errorf("GitHub GraphQL returned no data")
	}
	return response.Data, nil
}

// Blockers reads dependencies in batches instead of one REST request per blocked issue.
// Every connection is paginated; a missing issue or partial response fails closed.
func (g GH) Blockers(ctx context.Context, numbers []int) (map[int][]Item, error) {
	owner, repo, ok := strings.Cut(g.Slug, "/")
	if !ok || owner == "" || repo == "" {
		return nil, fmt.Errorf("invalid repository slug")
	}
	result := make(map[int][]Item)
	for start := 0; start < len(numbers); start += 25 {
		pending := make(map[int]string)
		for _, n := range numbers[start:min(start+25, len(numbers))] {
			if n <= 0 {
				return nil, fmt.Errorf("invalid issue number")
			}
			pending[n] = ""
			result[n] = []Item{}
		}
		for len(pending) > 0 {
			var query strings.Builder
			fmt.Fprintf(&query, "query { repository(owner:%s,name:%s) {", strconv.Quote(owner), strconv.Quote(repo))
			for n, cursor := range pending {
				after := ""
				if cursor != "" {
					after = ",after:" + strconv.Quote(cursor)
				}
				fmt.Fprintf(&query, "i%d: issue(number:%d) { blockedBy(first:100%s) { nodes { number state } pageInfo { hasNextPage endCursor } } }", n, n, after)
			}
			query.WriteString("} }")
			data, err := g.graph(ctx, query.String())
			if err != nil {
				return nil, err
			}
			type connection struct {
				BlockedBy struct {
					Nodes    []Item
					PageInfo struct {
						HasNextPage bool
						EndCursor   string
					}
				}
			}
			var response struct{ Repository map[string]*connection }
			if err := json.Unmarshal(data, &response); err != nil {
				return nil, err
			}
			for n, cursor := range pending {
				issue := response.Repository[fmt.Sprintf("i%d", n)]
				if issue == nil || issue.BlockedBy.Nodes == nil {
					return nil, fmt.Errorf("missing dependencies for issue #%d", n)
				}
				result[n] = append(result[n], issue.BlockedBy.Nodes...)
				page := issue.BlockedBy.PageInfo
				if !page.HasNextPage {
					delete(pending, n)
				} else if page.EndCursor == "" || page.EndCursor == cursor {
					return nil, fmt.Errorf("invalid dependency cursor for #%d", n)
				} else {
					pending[n] = page.EndCursor
				}
			}
		}
	}
	return result, nil
}
