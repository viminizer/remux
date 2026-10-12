# Decisions

2026-10-12, PR #62

- Serialize claims, merged-branch cleanup and recovery with a nonblocking flock in Git's common directory. Recovery completes cleanup before publishing readiness and defers on cleanup failure or active claims. This prevents deletion of work a builder has claimed, including across worktrees. Source: existing cleanup flock and claim-label patterns in `internal/harness`.
- Identify replacement PRs by the source number parsed from every open harness issue branch, across all scopes; ignore incidental PR body references. A full-scope or other-scope replacement must prevent requeueing the same source issue. Source: existing `branchWork` ownership rule and `GH.PRs` lookup.

2026-10-12, PR #63 review repairs

- Classify review changes from the base/HEAD merge base, matching a PR diff even when the pre-review merge conflicts. Comparing tips includes unrelated base changes and misroutes small PRs. Source: `reviewOnce` merge/abort flow and the conflicting-branch regression.
- Give each agent runner its own process group and kill that group on timeout/cancellation before handoff; bound pipe draining with `WaitDelay`. This stops child tool commands from holding up or mutating the next review's worktree without touching other sessions. Source: existing `spawn` runner and the timed-out fake-CLI child regression.

2026-10-13, PR #65 review repairs

- Apply secondary-limit pauses to both existing transport budgets, honor REST Retry-After with a one-minute minimum, and retain any later primary reset. This prevents queue polling through the other API during a shared throttle without shortening an exhausted budget. Source: existing GraphQL secondary-limit policy and GitHub's REST rate-limit documentation (https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api).
