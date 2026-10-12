package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
)

func argValue(args []string, key, otherwise string) string {
	for i, a := range args {
		if a == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return otherwise
}
func hasArg(args []string, key string) bool {
	for _, a := range args {
		if a == key {
			return true
		}
	}
	return false
}
func (g GH) remoteSlug(ctx context.Context) (string, error) {
	if g.Slug != "" {
		return g.Slug, nil
	}
	cmd := exec.CommandContext(ctx, "git", "config", "--get", "remote.origin.url")
	cmd.Dir = g.Dir
	b, err := cmd.Output()
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(b))
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		s = strings.TrimPrefix(u.Path, "/")
	} else if _, path, ok := strings.Cut(s, ":"); ok {
		s = path
	}
	s = strings.TrimSuffix(s, ".git")
	parts := strings.Split(s, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("cannot derive repository from origin")
	}
	return s, nil
}
func (g GH) restMap(ctx context.Context, path string) (map[string]any, error) {
	b, err := g.api(ctx, path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	err = json.Unmarshal(b, &m)
	return m, err
}
func (g GH) restArray(ctx context.Context, path string) ([]map[string]any, error) {
	b, err := g.api(ctx, path)
	if err != nil {
		return nil, err
	}
	var a []map[string]any
	err = json.Unmarshal(b, &a)
	return a, err
}
func restLabelsMatch(m map[string]any, want string) bool {
	if want == "" {
		return true
	}
	labels, _ := m["labels"].([]any)
	for _, l := range labels {
		if v, ok := l.(map[string]any); ok && v["name"] == want {
			return true
		}
	}
	return false
}
func projectREST(m map[string]any, fields string) map[string]any {
	r := map[string]any{}
	for _, key := range strings.Split(fields, ",") {
		switch key {
		case "url":
			r[key] = m["html_url"]
		case "state":
			s, _ := m["state"].(string)
			if m["merged_at"] != nil {
				s = "merged"
			}
			r[key] = strings.ToUpper(s)
		case "isDraft":
			r[key] = m["draft"]
		case "headRefName", "headRefOid":
			h, _ := m["head"].(map[string]any)
			name := "ref"
			if key == "headRefOid" {
				name = "sha"
			}
			r[key] = h[name]
		case "mergedBy":
			r[key] = m["merged_by"]
		case "author":
			r[key] = m["user"]
		case "authorAssociation":
			r[key] = m["author_association"]
		case "commit":
			r[key] = map[string]any{"oid": m["commit_id"]}
		default:
			r[key] = m[key]
		}
	}
	return r
}
func (g GH) restView(ctx context.Context, slug, kind, n, fields string) ([]byte, error) {
	path := "repos/" + slug + "/issues/" + n
	if kind == "pr" {
		path = "repos/" + slug + "/pulls/" + n
	}
	m, err := g.restMap(ctx, path)
	if err != nil {
		return nil, err
	}
	r := projectREST(m, fields)
	for _, spec := range []struct{ field, path, fields string }{
		{"comments", "issues/" + n + "/comments", "body,url,author"},
		{"reviews", "pulls/" + n + "/reviews", "body,url,author,authorAssociation,commit,state"},
	} {
		if !strings.Contains(","+fields+",", ","+spec.field+",") {
			continue
		}
		var rows []map[string]any
		for page := 1; ; page++ {
			a, e := g.restArray(ctx, fmt.Sprintf("repos/%s/%s?per_page=100&page=%d", slug, spec.path, page))
			if e != nil {
				return nil, e
			}
			for _, row := range a {
				rows = append(rows, projectREST(row, spec.fields))
			}
			if len(a) < 100 {
				break
			}
		}
		if rows == nil {
			rows = []map[string]any{}
		}
		r[spec.field] = rows
	}
	return json.Marshal(r)
}
func (g GH) restList(ctx context.Context, slug, kind string, args []string) ([]byte, error) {
	limit, err := strconv.Atoi(argValue(args, "--limit", "30"))
	if err != nil || limit < 1 {
		return nil, fmt.Errorf("invalid list limit")
	}
	state := argValue(args, "--state", "open")
	q := url.Values{"state": {state}, "per_page": {"100"}, "sort": {"created"}, "direction": {"desc"}}
	resource := "issues"
	if kind == "pr" {
		resource = "pulls"
	}
	if kind == "label" {
		resource = "labels"
		q.Del("state")
		q.Del("sort")
		q.Del("direction")
	}
	if state == "merged" {
		q.Set("state", "closed")
	}
	label := argValue(args, "--label", "")
	if kind == "issue" && label != "" {
		q.Set("labels", label)
	}
	if head := argValue(args, "--head", ""); head != "" {
		q.Set("head", strings.Split(slug, "/")[0]+":"+head)
	}
	fields := argValue(args, "--json", itemFields)
	rows := []map[string]any{}
	for page := 1; len(rows) < limit; page++ {
		q.Set("page", strconv.Itoa(page))
		a, e := g.restArray(ctx, "repos/"+slug+"/"+resource+"?"+q.Encode())
		if e != nil {
			return nil, e
		}
		for _, m := range a {
			if kind == "issue" && m["pull_request"] != nil {
				continue
			}
			if kind == "pr" && (state == "merged" && m["merged_at"] == nil || !restLabelsMatch(m, label)) {
				continue
			}
			if kind == "pr" && (strings.Contains(fields, "reviews") || strings.Contains(fields, "mergedBy")) {
				b, e := g.restView(ctx, slug, kind, fmt.Sprint(m["number"]), fields)
				if e != nil {
					return nil, e
				}
				var r map[string]any
				if e = json.Unmarshal(b, &r); e != nil {
					return nil, e
				}
				rows = append(rows, r)
			} else {
				rows = append(rows, projectREST(m, fields))
			}
			if len(rows) == limit {
				break
			}
		}
		if len(a) < 100 {
			break
		}
	}
	return json.Marshal(rows)
}
func (g GH) restThread(ctx context.Context, slug, kind, n string, commentsOnly bool) ([]byte, error) {
	fields := itemFields + ",body"
	if commentsOnly {
		fields += ",comments"
	}
	b, err := g.restView(ctx, slug, kind, n, fields)
	if err != nil {
		return nil, err
	}
	var p struct {
		Title, Body, URL, State string
		Comments                []struct {
			Body, URL string
			Author    struct{ Login string }
		}
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	var out strings.Builder
	if !commentsOnly {
		fmt.Fprintf(&out, "# %s #%s: %s\n%s\nState: %s\n\n%s\n", kind, n, p.Title, p.URL, p.State, p.Body)
	} else {
		for _, c := range p.Comments {
			fmt.Fprintf(&out, "\n## %s\n%s\n%s\n", c.Author.Login, c.URL, c.Body)
		}
	}
	return []byte(out.String()), nil
}

// Only explicit rate-limit failures reach this dispatcher. Network failures,
// permission errors and unknown commands never trigger alternate mutations.
func (g GH) restFallback(ctx context.Context, args []string) ([]byte, bool, error) {
	if len(args) < 2 {
		return nil, false, nil
	}
	slug, err := g.remoteSlug(ctx)
	if err != nil {
		return nil, true, err
	}
	kind, action := args[0], args[1]
	if kind == "repo" && action == "view" && argValue(args, "--json", "") == "nameWithOwner" {
		return []byte(slug + "\n"), true, nil
	}
	if (kind == "pr" || kind == "issue" || kind == "label") && action == "list" {
		b, e := g.restList(ctx, slug, kind, args)
		return b, true, e
	}
	if (kind == "pr" || kind == "issue") && action == "view" && len(args) > 2 {
		var b []byte
		var e error
		if fields := argValue(args, "--json", ""); fields != "" {
			b, e = g.restView(ctx, slug, kind, args[2], fields)
		} else {
			b, e = g.restThread(ctx, slug, kind, args[2], hasArg(args, "--comments"))
		}
		return b, true, e
	}
	if kind == "pr" && action == "diff" && len(args) > 2 {
		b, e := g.api(ctx, "repos/"+slug+"/pulls/"+args[2], "-H", "Accept: application/vnd.github.diff")
		return b, true, e
	}
	if kind == "pr" && action == "review" && len(args) > 2 && hasArg(args, "--comment") {
		b, e := g.api(ctx, "-X", "POST", "repos/"+slug+"/pulls/"+args[2]+"/reviews", "-f", "event=COMMENT", "-f", "body="+argValue(args, "--body", ""))
		return b, true, e
	}
	if kind == "pr" && action == "merge" && len(args) > 2 && hasArg(args, "--squash") && argValue(args, "--match-head-commit", "") != "" {
		b, e := g.api(ctx, "-X", "PUT", "repos/"+slug+"/pulls/"+args[2]+"/merge", "-f", "merge_method=squash", "-f", "sha="+argValue(args, "--match-head-commit", ""))
		if e == nil {
			var p struct {
				Merged  bool
				Message string
			}
			if json.Unmarshal(b, &p) != nil || !p.Merged {
				e = fmt.Errorf("GitHub did not confirm merge: %s", p.Message)
			}
		}
		return b, true, e
	}
	if kind == "pr" && action == "edit" && len(args) > 2 && hasArg(args, "--add-reviewer") {
		who := argValue(args, "--add-reviewer", "")
		field := "reviewers[]="
		if _, team, ok := strings.Cut(who, "/"); ok {
			who = team
			field = "team_reviewers[]="
		}
		b, e := g.api(ctx, "-X", "POST", "repos/"+slug+"/pulls/"+args[2]+"/requested_reviewers", "-f", field+who)
		return b, true, e
	}
	if kind == "label" && action == "create" && len(args) > 2 {
		fields := []string{"-f", "name=" + args[2], "-f", "color=" + argValue(args, "--color", ""), "-f", "description=" + argValue(args, "--description", "")}
		b, e := g.api(ctx, append([]string{"-X", "POST", "repos/" + slug + "/labels"}, fields...)...)
		if e != nil && !githubLimited(e) && strings.Contains(e.Error(), "422") && hasArg(args, "--force") {
			path := "repos/" + slug + "/labels/" + url.PathEscape(args[2])
			if _, exists := g.api(ctx, path); exists == nil {
				b, e = g.api(ctx, append([]string{"-X", "PATCH", path}, fields...)...)
			}
		}
		return b, true, e
	}
	// Draft-to-ready is GraphQL-only. Never invent a REST update that
	// silently leaves the PR a draft, or bypass the draft merge guard.
	return nil, false, nil
}
