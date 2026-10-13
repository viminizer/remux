package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGraphDependenciesBatchAndPaginate(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "gh")
	script := fmt.Sprintf(`#!/bin/sh
echo "$*" >> '%s'
case "$*" in
*'after:"next"'*) echo '{"data":{"repository":{"i1":{"blockedBy":{"nodes":[{"number":4,"state":"OPEN"}],"pageInfo":{"hasNextPage":false}}}}}}' ;;
*) echo '{"data":{"repository":{"i1":{"blockedBy":{"nodes":[{"number":3,"state":"CLOSED"}],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}},"i2":{"blockedBy":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}' ;;
esac
`, calls)
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	old := ghBin
	ghBin = bin
	defer func() { ghBin = old }()
	deps, err := (GH{Slug: "o/r", Dir: dir}).Blockers(context.Background(), []int{1, 2})
	if err != nil || len(deps[1]) != 2 || len(deps[2]) != 0 || deps[1][1].State != "OPEN" {
		t.Fatalf("deps=%v err=%v", deps, err)
	}
	b, _ := os.ReadFile(calls)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "i1:") || !strings.Contains(lines[0], "i2:") || strings.Contains(lines[1], "i2:") || strings.Contains(string(b), "repos/") {
		t.Fatalf("unexpected requests: %s", b)
	}
}

func TestGraphDependenciesFailClosed(t *testing.T) {
	for _, body := range []string{`{"data":{"repository":{"i1":null}}}`, `{"data":{"repository":{"i1":{"blockedBy":{"nodes":[]}}}},"errors":[{"message":"denied"}]}`, `{"data":{"repository":{"i1":{"blockedBy":{"nodes":[],"pageInfo":{"hasNextPage":true}}}}}}`} {
		t.Run(body, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "gh")
			if err := os.WriteFile(bin, []byte("#!/bin/sh\necho '"+body+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			old := ghBin
			ghBin = bin
			defer func() { ghBin = old }()
			if _, err := (GH{Slug: "o/r", Dir: dir}).BlockedBy(context.Background(), 1); err == nil {
				t.Fatal("incomplete dependency response accepted")
			}
		})
	}
}

func TestGraphDependenciesRespectGraphBudget(t *testing.T) {
	g := GH{Slug: "o/r", Dir: t.TempDir()}
	g.setBudget("GraphQL", time.Now().Add(time.Minute))
	if _, err := g.BlockedBy(context.Background(), 1); !githubLimited(err) {
		t.Fatalf("expected wait, got %v", err)
	}
}

func TestEnsureLabelsUsesReadOnlyGraphLookupWhenLabelsExist(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "gh")
	calls := filepath.Join(dir, "calls")
	var labels strings.Builder
	for i := range Labels {
		fmt.Fprintf(&labels, `,"l%d":{"id":"label%d"}`, i, i)
	}
	response := `{"data":{"repository":{"id":"repo"` + labels.String() + `}}}`
	script := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> '%s'\necho '%s'\n", calls, response)
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	old := ghBin
	ghBin = bin
	defer func() { ghBin = old }()
	if err := (GH{Slug: "o/r", Dir: dir}).EnsureLabels(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(calls)
	if strings.Count(strings.TrimSpace(string(b)), "\n") != 0 || !strings.HasPrefix(string(b), "api graphql ") || strings.Contains(string(b), "mutation") {
		t.Fatalf("setup rewrote existing labels: %s", b)
	}
}
