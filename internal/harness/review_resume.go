package harness

import (
	"context"
	"fmt"
	"strings"
)

type priorReview struct {
	Body              string `json:"body"`
	AuthorAssociation string `json:"authorAssociation"`
	Commit            struct {
		OID string `json:"oid"`
	} `json:"commit"`
}

func reusableReview(reviews []priorReview, head string) (string, bool) {
	if head == "" {
		return "", false
	}
	for i := len(reviews) - 1; i >= 0; i-- {
		r := reviews[i]
		if r.Commit.OID != head {
			continue
		}
		if r.AuthorAssociation != "OWNER" && r.AuthorAssociation != "MEMBER" && r.AuthorAssociation != "COLLABORATOR" {
			continue
		}
		body := r.Body
		if strings.HasPrefix(body, "Review incomplete; no approval.\n\n") {
			// 0.3.91 wrote this harness header even when the agent completed
			// a review using an inline status/count. Only strip that exact header.
			body = strings.TrimPrefix(body, "Review incomplete; no approval.\n\n")
		} else if !strings.HasPrefix(body, "Reviewer: ") {
			continue
		}
		return body, completedReview(body)
	}
	return "", false
}

func (l *Loop) completedReviewOnHead(ctx context.Context, pr Item) (string, bool, error) {
	var p struct {
		Reviews []priorReview `json:"reviews"`
	}
	if err := l.gh.JSON(ctx, &p, "pr", "view", fmt.Sprint(pr.Number), "--json", "reviews"); err != nil {
		return "", false, err
	}
	body, ok := reusableReview(p.Reviews, pr.HeadOid)
	return body, ok, nil
}
