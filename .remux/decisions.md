# Decisions

2026-10-12, PR #62

- Serialize claims, merged-branch cleanup and recovery with a nonblocking flock in Git's common directory. Recovery completes cleanup before publishing readiness and defers on cleanup failure or active claims. This prevents deletion of work a builder has claimed, including across worktrees. Source: existing cleanup flock and claim-label patterns in `internal/harness`.
- Identify replacement PRs by the source number parsed from every open harness issue branch, across all scopes; ignore incidental PR body references. A full-scope or other-scope replacement must prevent requeueing the same source issue. Source: existing `branchWork` ownership rule and `GH.PRs` lookup.
