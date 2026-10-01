package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Labels is the state machine. wip:<scope> and done:<scope> are made on
// demand - GitHub creates a label the first time it is added to an issue.
var Labels = []struct{ Name, Color, Desc string }{
	{"ready", "0e8a16", "Waiting for an agent loop"},
	{"blocker", "d93f0b", "Blocks another issue. Taken first."},
	{"blocked", "fbca04", "Waiting for its blocker issues to close"},
	{"needs-review", "1d76db", "Draft PR waiting for the Codex review loop"},
	{"needs-human", "b60205", "An agent is stuck. One short question in a comment."},
}

// GH runs gh against one repo.
type GH struct {
	Slug string // owner/name
	Dir  string // a local clone, for commands that read the git remote
}

// gh is a variable so tests can stand in for the binary.
var ghBin = "gh"

func (g GH) run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, ghBin, args...)
	cmd.Dir = g.Dir
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
	if g.Slug != "" {
		cmd.Env = append(cmd.Env, "GH_REPO="+g.Slug)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("gh %s: %s", strings.Join(args[:min(len(args), 3)], " "), firstLine(errb.String(), err))
	}
	return out.Bytes(), nil
}

// JSON runs gh and decodes its output into v.
func (g GH) JSON(ctx context.Context, v any, args ...string) error {
	out, err := g.run(ctx, args...)
	if err != nil {
		return err
	}
	return json.Unmarshal(out, v)
}

// Item is an issue or a pull request, with only what the loops look at.
type Item struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	State   string `json:"state"`
	IsDraft bool   `json:"isDraft"`
	Head    string `json:"headRefName"`
	HeadOid string `json:"headRefOid"`
	Body    string `json:"body"`
	Labels  []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (it Item) Has(label string) bool {
	for _, l := range it.Labels {
		if l.Name == label {
			return true
		}
	}
	return false
}

// HasPrefix reports whether any label starts with p, like "wip:".
func (it Item) HasPrefix(p string) bool {
	for _, l := range it.Labels {
		if strings.HasPrefix(l.Name, p) {
			return true
		}
	}
	return false
}

const itemFields = "number,title,url,state,labels"

func (g GH) Issues(ctx context.Context, label string) ([]Item, error) {
	var items []Item
	err := g.JSON(ctx, &items, "issue", "list", "--state", "open", "--label", label,
		"--limit", "200", "--json", itemFields)
	return items, err
}

func (g GH) Issue(ctx context.Context, n int) (Item, error) {
	var it Item
	err := g.JSON(ctx, &it, "issue", "view", fmt.Sprint(n), "--json", itemFields)
	return it, err
}

// PRs lists open pull requests, optionally only those with a label.
func (g GH) PRs(ctx context.Context, label string) ([]Item, error) {
	args := []string{"pr", "list", "--state", "open", "--limit", "200",
		"--json", itemFields + ",isDraft,headRefName,headRefOid,body"}
	if label != "" {
		args = append(args, "--label", label)
	}
	var items []Item
	err := g.JSON(ctx, &items, args...)
	return items, err
}

// PRForBranch finds the open pull request whose head is branch.
func (g GH) PRForBranch(ctx context.Context, branch string) (*Item, error) {
	var items []Item
	err := g.JSON(ctx, &items, "pr", "list", "--state", "open", "--head", branch,
		"--json", itemFields+",isDraft,headRefName,headRefOid")
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0], nil
}

// Thread is an issue or pull request as text, comments included, for a prompt.
func (g GH) Thread(ctx context.Context, kind string, n int) (string, error) {
	out, err := g.run(ctx, kind, "view", fmt.Sprint(n), "--comments")
	return string(out), err
}

// AddLabels and RemoveLabel go through the issues API, which covers pull
// requests too, so one call serves both.
func (g GH) AddLabels(ctx context.Context, n int, labels ...string) error {
	args := []string{"api", "-X", "POST", fmt.Sprintf("repos/%s/issues/%d/labels", g.Slug, n)}
	for _, l := range labels {
		args = append(args, "-f", "labels[]="+l)
	}
	_, err := g.run(ctx, args...)
	return err
}

// RemoveLabel is quiet about a label that is not there: the goal is that it
// is gone, and it is.
func (g GH) RemoveLabel(ctx context.Context, n int, label string) error {
	_, err := g.run(ctx, "api", "-X", "DELETE",
		fmt.Sprintf("repos/%s/issues/%d/labels/%s", g.Slug, n, url.PathEscape(label)))
	if err != nil && strings.Contains(err.Error(), "Label does not exist") {
		return nil
	}
	return err
}

func (g GH) Comment(ctx context.Context, n int, body string) error {
	_, err := g.run(ctx, "api", "-X", "POST", fmt.Sprintf("repos/%s/issues/%d/comments", g.Slug, n),
		"-f", "body="+body)
	return err
}

// BlockedBy lists the issues GitHub records as blocking n.
func (g GH) BlockedBy(ctx context.Context, n int) ([]Item, error) {
	var items []Item
	err := g.JSON(ctx, &items, "api", fmt.Sprintf("repos/%s/issues/%d/dependencies/blocked_by", g.Slug, n))
	return items, err
}

// EnsureLabels creates the harness labels, and is safe to run again.
func (g GH) EnsureLabels(ctx context.Context) error {
	for _, l := range Labels {
		if _, err := g.run(ctx, "label", "create", l.Name, "--color", l.Color,
			"--description", l.Desc, "--force"); err != nil {
			return err
		}
	}
	return nil
}

// Slug asks gh which GitHub repo a local clone belongs to.
func Slug(ctx context.Context, dir string) (string, error) {
	out, err := GH{Dir: dir}.run(ctx, "repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner")
	return strings.TrimSpace(string(out)), err
}

func firstLine(s string, fallback error) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback.Error()
	}
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return s
}
