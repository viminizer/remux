package github

import (
	"encoding/json"
	"testing"
)

func TestIgnoredMatch(t *testing.T) {
	ig := Ignored{"vercel", " netlify "}
	for _, name := range []string{"Vercel", "vercel/preview", "Vercel - Preview", "netlify/deploy"} {
		if !ig.Match(name) {
			t.Errorf("%q should match", name)
		}
	}
	for _, name := range []string{"", "build", "test (ubuntu)", "verce"} {
		if ig.Match(name) {
			t.Errorf("%q should not match", name)
		}
	}
	if (Ignored{}).Match("Vercel") || (Ignored{"", "  "}).Match("Vercel") {
		t.Error("an empty list must match nothing")
	}
}

// The real case: two of Kevin's pull requests were red only because a Vercel
// preview deploy failed, which kept them in "Needs you" forever.
func TestRollupChecksSkipsIgnored(t *testing.T) {
	nodes := func(js string) []rollupNode {
		var out []rollupNode
		if err := json.Unmarshal([]byte(js), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	only := nodes(`[{"context":"Vercel","state":"FAILURE"}]`)
	mixed := nodes(`[{"context":"Vercel","state":"FAILURE"},
	                 {"name":"build","status":"COMPLETED","conclusion":"SUCCESS"}]`)
	both := nodes(`[{"context":"Vercel","state":"FAILURE"},
	                {"name":"build","status":"COMPLETED","conclusion":"FAILURE"}]`)
	waiting := nodes(`[{"context":"Vercel","state":"FAILURE"},
	                   {"name":"build","status":"IN_PROGRESS"}]`)

	ig := Ignored{"vercel"}
	cases := []struct {
		name  string
		nodes []rollupNode
		total int
		ig    Ignored
		want  Checks
	}{
		{"only the ignored one failed", mixed, 2, ig, ChecksPass},
		{"a real check failed too", both, 2, ig, ChecksFail},
		{"the rest is still running", waiting, 2, ig, ChecksPending},
		{"no ignore list", mixed, 2, nil, ChecksFail},
		// A pull request whose only check is an ignored one has, as far as
		// this list is concerned, no checks at all. Yod-All/munaddiy-admin#9
		// is exactly this and sat in "Needs you" because of it.
		{"everything is ignorable", only, 1, ig, ChecksNone},
		// contexts(last: N) is not ordered by state, so a truncated list
		// could hide the failure that matters.
		{"list was truncated", mixed, 99, ig, ChecksFail},
	}
	for _, c := range cases {
		if got := rollupChecks("FAILURE", c.nodes, c.total, c.ig); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}

	// A green rollup is never re-examined - there is nothing to forgive.
	if got := rollupChecks("SUCCESS", both, 2, ig); got != ChecksPass {
		t.Errorf("green rollup became %q", got)
	}
}

// The same rule has to hold for a repo's pull request list, which gets the
// checks by name from gh rather than from the inbox query.
func TestPRChecksSkipsIgnored(t *testing.T) {
	var g ghPR
	js := `{"statusCheckRollup":[{"context":"Vercel","state":"FAILURE"},
	                              {"name":"build","status":"COMPLETED","conclusion":"SUCCESS"}]}`
	if err := json.Unmarshal([]byte(js), &g); err != nil {
		t.Fatal(err)
	}
	if got := g.checks(Ignored{"vercel"}); got != ChecksPass {
		t.Errorf("got %q want pass", got)
	}
	if got := g.checks(nil); got != ChecksFail {
		t.Errorf("without an ignore list: got %q want fail", got)
	}

	// The detail screen still lists the red check. Hiding it would turn a
	// preference about inbox noise into a lie about the pull request.
	d := g.detail(Ignored{"vercel"})
	var seen bool
	for _, r := range d.Runs {
		if r.Name == "Vercel" && r.State == ChecksFail {
			seen = true
		}
	}
	if !seen {
		t.Errorf("the ignored check vanished from the detail runs: %+v", d.Runs)
	}
}
