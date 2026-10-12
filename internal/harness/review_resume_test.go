package harness

import "testing"

func TestInlineReviewVerdicts(t *testing.T) {
	for _, s := range []string{
		"REVIEW COMPLETE — Confirmed bugs fixed: 0.",
		"REVIEW COMPLETE - Confirmed bugs fixed: 0. Inspected abc. No unresolved findings.",
		"**REVIEW COMPLETE** — Confirmed bugs fixed: 2",
		"review complete, confirmed bugs fixed: 0",
	} {
		if !completedReview(s) {
			t.Errorf("completed review rejected: %q", s)
		}
		if reportedFixedBugs(s) == nil {
			t.Errorf("count missing: %q", s)
		}
	}
	for _, s := range []string{
		"REVIEW COMPLETE but still investigating",
		"REVIEW COMPLETE — unresolved checks",
		"REVIEW COMPLETE — Confirmed bugs fixed: 0\nREVIEW INCOMPLETE — unfinished caller",
		"```\nREVIEW COMPLETE\n```",
		"> REVIEW COMPLETE",
		"Example: REVIEW COMPLETE — Confirmed bugs fixed: 0",
	} {
		if completedReview(s) {
			t.Errorf("incomplete/example accepted: %q", s)
		}
	}
}

func TestReuseRequiresReviewedHeadAndLatestVerdict(t *testing.T) {
	complete := priorReview{Body: "Review incomplete; no approval.\n\nNo real issues found. Checks passed.\nREVIEW COMPLETE — Confirmed bugs fixed: 0.", AuthorAssociation: "OWNER"}
	complete.Commit.OID = "head-a"
	if _, ok := reusableReview([]priorReview{complete}, "head-a"); !ok {
		t.Fatal("legacy completed review not recovered")
	}
	if _, ok := reusableReview([]priorReview{complete}, "head-b"); ok {
		t.Fatal("review reused after code changed")
	}
	incomplete := complete
	incomplete.Body = "Reviewer: claude\nREVIEW INCOMPLETE — unfinished access check"
	if _, ok := reusableReview([]priorReview{complete, incomplete}, "head-a"); ok {
		t.Fatal("newer incomplete verdict ignored")
	}
	untrusted := complete
	untrusted.AuthorAssociation = "NONE"
	if _, ok := reusableReview([]priorReview{untrusted}, "head-a"); ok {
		t.Fatal("untrusted review reused")
	}
	unwrapped := complete
	unwrapped.Body = "REVIEW COMPLETE — Confirmed bugs fixed: 0"
	if _, ok := reusableReview([]priorReview{unwrapped}, "head-a"); ok {
		t.Fatal("non-harness comment reused")
	}
}
