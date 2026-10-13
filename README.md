# remux

Supervise your Mac's tmux sessions - and the coding agents living in them -
from your phone.

Read progress, answer an agent's question, send the next instruction, and move
between projects, without sitting at the laptop.

One binary. The phone UI is compiled in with `go:embed`, and Tailscale is
embedded with `tsnet`, so there is nothing else to install: no `tailscaled`, no
root, no Tailscale app on the Mac, no `tailscale serve`, no port forward.

```
Phone (Chrome PWA)          Your Mac
  Tailscale app        +----------------------------------+
       |               |  remux  (one Go binary)          |
       +---WireGuard---+-> tsnet node "remux"             |
   https://remux       |     ListenTLS(":443")            |
   .<tailnet>.ts.net   |     |- embedded React UI         |
                       |     +- HTTP+WS API --exec--> tmux server
                       +----------------------------------+
```

## It never touches your laptop's layout

remux never becomes a tmux client. Attaching - `attach-session` or `-CC`
control mode - adds a client whose terminal size renegotiates pane dimensions,
which would resize the layout you are sitting in front of. Everything is done
with targeted, clientless commands (`capture-pane -t %14`, `send-keys -t %14`),
and new sessions and windows are created with `-d` so the active window never
jumps.

The tmux layer refuses `attach-session`, `switch-client`, `kill-server`,
`select-window`, `select-pane`, `resize-pane`, `resize-window` and
`source-file` outright. The one deliberate exception is the pane menu's
**Focus on laptop**, which you have to ask for by name.

`scripts/laptop-invariant.sh before` / `after` proves it: it snapshots every
pane's geometry and fails if one vanished, changed size, or gained a client.

The one thing remux writes to panes you are using is four user options -
`@remux_title`, `@remux_task`, `@remux_state` and `@remux_project` (see **Pane
names on the laptop**). A user option is inert: nothing renders it unless your tmux config
asks for it, and no running program can be disturbed by it. The invariant
script reports every one of those writes and fails if a value is outside what
remux is allowed to write.

## Install

Download the release binary, make it executable, then:

```bash
./remux install
```

`install` copies the binary to `~/.local/bin/remux` and writes a **LaunchAgent**
at `~/Library/LaunchAgents/com.viminizer.remux.plist`.

Two details worth knowing, both learned the hard way:

- It has to be a LaunchAgent, not a system daemon. A daemon runs as root, and
  your tmux server belongs to your own login session - root cannot see it.
- The binary is copied out of wherever you ran it from. macOS guards
  `~/Desktop`, `~/Documents` and `~/Downloads` with TCC, and a LaunchAgent has
  no consent to read them: pointing launchd at a binary under one of those
  produces a process that hangs inside `dyld` before `main()` runs, reporting a
  healthy pid and writing nothing to any log.

Then, once:

1. **Open the login URL** printed on first start
   (`tail -f ~/.config/remux/remux.log`) and approve the device.
2. **Turn on MagicDNS and HTTPS Certificates** at
   <https://login.tailscale.com/admin/dns>. Without them `ListenTLS` fails;
   remux catches that and tells you which toggle to flip rather than showing a
   raw TLS error.
3. **On the phone**: install the Tailscale app and sign in, scan the QR code
   remux prints, then Chrome menu -> Add to Home screen.

Keeping the Mac awake is still your job: `caffeinate -dims`, or Amphetamine.

### Commands

```
remux                 run on your tailnet
remux --local         run on 127.0.0.1:7399, no tailnet, no auth - for UI work
remux install         write and load the launchd agent; starts at login
remux uninstall       unload and remove it
remux restart         reload after replacing the binary
remux status          preflight, launch agent, node state, identity
```

Updating is: replace the file, `remux restart`.

## Auth

There is no token anywhere. `WhoIs` resolves the caller's Tailscale identity
from the connection itself, and only the enrolled login gets through -
everything else gets a 403 that never reaches tmux. The check gates the
WebSocket upgrade as well as the REST routes, so a socket cannot outlive it.

`AllowLogin` defaults to the first identity to connect, so it self-configures.
Every mutating request is written to `~/.config/remux/audit.log` with the
caller and the tmux target.

Under `--local` the check is skipped, because the listener is loopback-only.

`ListenFunnel` is deliberately not used. This is a remote-control plane for a
machine running agents with filesystem access; it stays on the tailnet.

## Measured on this Mac

Intel i9-9880H, macOS 26.6.2, tmux 3.5a, against a real workspace of 3
sessions / 17 windows / 27 panes.

| | |
|---|---|
| Binary, stripped | **21 MB** (22,323,792 bytes) |
| RSS, idle | **14.3 MB** |
| RSS, after a full tree + previews of 29 panes | **15.7 MB** |
| `GET /api/tree` | 19-23 ms |
| `GET /api/tree?preview=1` (29 panes captured in parallel) | ~30 ms |
| One `capture-pane` | ~11 ms |

The stripping is not optional: the same binary is 31 MB unstripped, so
`scripts/build.sh` always passes `-ldflags="-s -w"`.

## Push notifications

The one part of remux that leaves the tailnet. A watcher classifies every pane
every 5 s and notifies only on a *transition* into `waiting`, with a 5-minute
per-pane cooldown, suppressed if the phone already has that pane open. It only
runs while at least one subscription exists.

The payload carries session name, window name and pane id - never pane
content, because it passes through the browser vendor's push service. Tapping
it opens `/#/p/%14` directly.

The phone needs ordinary internet to receive pushes, not just Tailscale, and
the Mac needs outbound access to the push service.

## Pane names on the laptop

remux writes three user options onto each agent pane, so the laptop can read
the same thing the phone does:

| option | what | cost |
|---|---|---|
| `@remux_state` | one glyph: `!` blocked on an answer, `✳` working, `✓` idle | free, from the classifier that already runs |
| `@remux_task` | 3-5 words naming the work, read off the screen by a cheap model | a fraction of a cent, and only when the work changes |
| `@remux_project` | the short repo name the pane is working in, at most 12 characters | free, worked out from the pane's own directory |

None of them is rendered by tmux on its own. Put them in your status line to see
them - this is the snippet the glyph exists for:

```tmux
set -g window-status-format         '#I:#W#{?#{!=:#{@remux_state},}, #{@remux_state},}'
set -g window-status-current-format '#I:#W#{?#{!=:#{@remux_state},}, #{@remux_state},}'
set -g pane-border-status top
set -g pane-border-format ' #{?#{!=:#{@remux_task},},#{@remux_task},#{pane_current_command}} #{@remux_state} '
```

`@remux_task` is the only part of remux that spends money, so it has a switch
on the Settings screen. Turning it off clears the names it wrote; the glyph
half keeps working and has no switch, because it costs nothing.

## Agent loops

Claude and Codex take GitHub issues on their own, and Codex reviews and merges
their pull requests. You only step in when an agent is stuck. The design is
[`docs/agent-harness.html`](docs/agent-harness.html).

Set a repo up once:

```bash
remux harness init ~/code/myrepo   # labels on GitHub, starter files in the repo
```

Then fill in `.remux/harness.json` and commit it with `.remux/decisions.md`:

| field | what |
|---|---|
| `mode` | `personal` (Codex merges) or `company` (nothing merges; the PR goes to your supervisor) |
| `test` | the command that must pass before anything merges. Without it nothing merges. |
| `references` | local read-only clones the agents compare against for real decisions |
| `instructions` | project default instructions, read before every run |
| `supervisor` | company mode: the reviewer when there is no CODEOWNERS and no history |

A repo can also keep a brief per scope and role under `.remux/instructions/`, so
starting a loop does not mean pasting prose into a form on a phone, and a reviewer
is never handed a builder's brief. A start reads
`<scope>-<role>.md` first and falls back to `<role>.md`:

| file | the loop it briefs |
|---|---|
| `.remux/instructions/frontend-build.md` | a build loop with scope `frontend` |
| `.remux/instructions/backend-build.md` | a build loop with scope `backend` |
| `.remux/instructions/build.md` | any build loop whose scope has no file of its own |
| `.remux/instructions/review.md` | every review loop, whatever the scope |
| `.remux/instructions/supervise.md` | the supervise loop |

These are session instructions, layer 2: `harness.json`'s `instructions` are still
the project defaults underneath, unless the start asks to replace them. Whatever the
start submits itself stands for any role the repo keeps no file for, so a repo with
no `.remux/instructions/` behaves exactly as before. The start's reply names the file
each loop was given, under `instructions`, so nobody has to trust that the right brief
loaded. Editing a file and starting again reloads it.

Start loops from the phone: **drawer → Loops → Start loop**. The Start screen takes
a count for each agent, up to 5: two Claude loops are `loop-claude-<repo>` and
`loop-claude2-<repo>`. The counts are the whole set for that repo: the screen
shows what runs there now, and Start (Update loops, once some run) starts the
missing loops, stops the extra ones after their current issue, and gives running
ones the new scope and instructions. Each loop claims an issue with its own `by:<session>`
label first, so two loops waking together never work the same one. Each loop is a
detached `loop-*` tmux session running `remux loop`. A build loop takes one
`ready` issue per run (blockers first) in its own worktree under
`.claude/worktrees/`, and opens a draft PR. The review loop runs `codex exec review` on
each `needs-review` PR and posts its findings as inline review comments. A
second Codex run fixes them on the PR's branch, only when there are findings or
the branch conflicts. A crashed run is started again, up to 3 times, before the
PR goes to `needs-human`. Then the loop runs your `test` command itself, merges
or marks the PR ready for the supervisor, and removes the review's worktree. A
conflict with the default branch, failing tests or a refused merge go back to a
fix run, and the loop tries again. The PR goes to `needs-human` after 2 fix runs
that did not help, or 10 in all. A conflict the fix run resolved counts as help,
even when the default branch moved on again. Labels are the state:

`ready` · `wip:<scope>` · `done:<scope>` · `blocker` · `blocked` · `needs-review` · `needs-human`

A repo with its own `status:*` labels works as it is. `status:ready` counts as
`ready`, and the loops move an issue through the repo's own `status:in-progress`,
`status:review` and `status:blocked` as they work it, when those labels exist.

When no ready issue is eligible for its scope, a build loop runs a triage
pass using a shortlist of unfinished work for that scope. Generic ready
labels on completed scopes do not count as available work. The prompt shows
both counts, prioritizes partial deliveries awaiting continuation, and excludes
claimed, deferred, held and non-dispatchable tracking/release-review tickets.
Blocked work is inspected against actual merged prerequisites and time gates;
an open prerequisite for paused frontend does not by itself block backend.
If the shortlist is empty, no agent triage run is launched. Otherwise the
agent marks up to 5 issues `ready` in the repo's own format and says why in a
comment. It changes no code and runs at most every 5 minutes.

**Trying Sonnet.** Each Claude build loop has its own model selector: Sonnet,
Opus, or CLI default. The supervisor has a separate selector. New forms start
with Opus; existing loops retain their selection. Set Claude 1 to Sonnet and
the other Claude loops and supervisor to Opus for a small trial. A loop's Edit
screen also changes its model, starting with the next issue. All Claude runs
keep medium effort. Codex review and repair keep their existing settings.

For API clients, `POST /api/loops` accepts `models`, an array aligned with
`agents`, and `superviseModel`. Use an empty string for CLI default; omit the
fields to preserve running loops' choices. `PATCH /api/loops/{name}` accepts
`{"model":"sonnet"}` (or `opus` or an empty string). The CLI also accepts
`remux loop --model sonnet` with its usual repo/session arguments.

Run history is appended to `~/.config/remux/loop-runs.jsonl`, with start/end
records, the requested model alias, issue or PR number, exit code and duration.
Build outcomes link issues to PRs; review-stage records distinguish review
from agent runs, and merge/stuck events remain in the history after a loop
stops. Compare build results with subsequent Codex runs using the PR number.
The history contains no prompts or agent output. It does not measure tokens,
billed usage or the exact model version behind an alias. A start without an
end means the run was interrupted before it could record its result.
Claude builds also leave a PR comment naming the selected model, source issue,
scope and commit. The note identifies the build contribution; Codex may repair
the PR afterward. Repeating settlement for the same contribution does not add
another comment. Earlier PRs are not retroactively attributed.
After upgrading from a version without model selection, existing loop
processes must finish and restart to use it. Restarting the service updates
the phone UI but leaves those processes running their previous code.

Each loop checks the queue at most once every 30 seconds, including after short runs.
Idle and failed checks wait 30 seconds before retrying. Queue triage runs at most once
every five minutes per loop and checks readiness for the selected scope,
including unfinished work left in review after partial merges. A confirmed
merged `issue-N[-scope]` PR returns its open, unfinished source issue to ready;
completed scopes, active claims, open replacement PRs, postponed and blocked
work stay out. Idle builders also recover older merged work stuck in
`status:review`. The expensive recovery scan runs at most once every five minutes,
shared by builders of the same repository and scope. Normal merged work is requeued
immediately; new-work polling stays at once every 30 seconds per loop.

The supervisor checks `needs-human` first, then inspects same-repository builders
and reviewers idle or blocked for at least two minutes. It can investigate stale
labels/claims, merged partial work, missing prerequisites, review waits and
build/runtime failures, with at most one investigation per 15 minutes. An empty
unfinished queue costs no AI run. Stopped and usage-limited workers are excluded.
The previous intervention is included so the next investigation verifies its result.

It can delegate an eligible issue or PR to an available existing loop through a
one-shot preferred task. Workers re-read eligibility and use normal claim locking;
the preference cannot bypass scope completion, ownership, held/postponed work or
external blocks. Working loops and their instructions/models remain untouched.
Diagnosis and actions are recorded in visible issue/PR comments and local run logs,
and the last supervisor result remains visible on its loop card/detail.

When no suitable worker is available, the supervisor can request one-task custom
builders or reviewers without another approval. Up to five custom builders and
five custom reviewers may run concurrently per repository. They use normal queue
claims, preserve drafts/unfinished work and remove their own session after the run.
Known overlapping code files keep builders sequential; the supervisor also checks
written ownership before requesting parallel work. Stopped projects stay stopped.

Before starting a custom worker, `bunx tokscale@latest usage --json` supplies live
subscription headroom. Only authenticated supported providers with valid quota
data are eligible; Codex must be the active account. Routine tasks need at least
25% effective headroom, deep tasks 40%, after allowing 10/20 percentage points
for each concurrent custom task sharing that account. These are conservative
heuristics, not exact token estimates or a completion guarantee. Claude needs
both session and weekly windows; Codex may expose only a weekly window. Unknown
usage/CLI failures and observed limits fail closed. A custom review keeps its
budget-selected provider rather than silently falling back to an unchecked one.
Limit failures preserve work and defer that account until its native reset.
This is an on-demand spawn check, not an automatic quota watcher or a rule that
stops existing work. All first-release scope, privacy and financial rules remain.

Queue reads use GraphQL and wait for its reset instead of falling back to REST.
Dependency checks batch up to 25 blocked issues into one query and paginate every
dependency list. Startup reads the harness labels through GraphQL and creates only
missing labels, avoiding repeated updates to existing labels from every loop.

A supervisor can finish with `RESOLVED PARKED` to record a waiting decision
without retrying the item. `blocked:external` protects unavailable credentials,
external prerequisites and time gates from automatic dependency unblocking.
Use `RESOLVED READY` only after verifying the parked prerequisite is available.

Review loops use a balanced policy by default. Documentation and small changes
(at most three files and 80 changed lines) go to Claude Sonnet at medium effort.
Other changes alternate Claude Sonnet and Codex, sharing a rotation across the
repo's worktrees and review loops. Finance, authorization, migrations, destructive
operations, binary changes and large diffs receive high effort (Claude Opus or
Codex). Paths and changed code determine the route; this is a conservative
classification, not a claim that the diff has a bug. Add `"review_policy":"fixed"`
to `.remux/harness.json` to preserve a loop's explicitly selected provider/model.
Both policies use the same review instructions and completion checks.

Each finding must trace a reachable user action through existing validation to
a wrong result, with file/line evidence. Ignore style, imagined scale, impossible
states and redundant defensive checks. Reachable money, stored-data and privacy
failures still matter when rare. Inspect the changed code and direct callers in
one pass; expand only for a concrete failure. Ordinary inspection aims for 5–10
minutes; builds and repairs can take longer. Review runs have a 20-minute ceiling
(40 minutes for deep routes). A crash, usage limit, timeout or incomplete verdict
hands preserved work and evidence to the other provider once. Neither a timeout
nor passing checks counts as a completed review. Two incomplete runs leave the
PR unmerged for supervision.

The visible PR review records provider, model, effort, route, inspected commit,
checks and elapsed review/repair time. Run history also records handoffs and
completed review duration, with the reviewer's self-reported count of verified
bugs repaired. Missing counts stay missing. Compare matching change classes and
verify those repairs before favoring a provider; raw finding count is not a score.
No automatic performance weighting or quota polling is introduced.

Completed review statuses may put the confirmed repair count on the same line.
Quoted examples and explicit incomplete verdicts still cannot approve a review.
A resumed PR can reuse a trusted harness review of its exact current head;
changed heads and newer incomplete verdicts require inspection. Reuse still
runs the required checks and handles main-branch conflicts, without crediting a
new provider review or repair count. Supervisor resumption also clears the
mirrored `status:blocked` PR label so `needs-review` can actually be picked up.

When a GitHub CLI operation reports a primary GraphQL rate limit, the harness
uses REST for supported issue/PR listings, metadata, comments, review history,
diffs, label setup, posted reviews and guarded squash merges. It keeps the same
label, pagination, draft and tested-head rules. Permission and ordinary network
errors are not retried through another transport. A per-process cooldown stops
repeating exhausted GraphQL requests; normal commands resume after the reset.
REST failures respect reset/Retry-After headers. Secondary limits pause both
transports for at least a minute. Waiting is a queue delay, not a needs-human
review failure, and a merge wait keeps needs-review attached.

Draft-to-ready is still a GraphQL-only step and waits for that budget. Agent
prompts also explain REST creation of draft PRs when their own gh commands hit
the primary GraphQL limit; child CLI commands are not intercepted by the harness.

The agents run in yolo mode, on the Mac itself: Claude with
`--dangerously-skip-permissions` and Codex with
`--dangerously-bypass-approvals-and-sandbox`. They can run any command your user
can, so only start loops on repos whose issues you trust.

**The supervisor.** Turn on **Supervisor** when you start loops, and a
supervise loop takes every `needs-human` item before you do. It fixes what the
loops got stuck on (a failed push, a conflict, a failing test) and answers an
agent's question only when the issue, the decision log, the docs or the code
clearly decide it. Everything else it passes to you as a plain-English question
with its recommendation, and labels it `escalated`. While it runs, the "stuck"
push only fires for what it passes on; the Inbox shows the rest as "the
supervisor is checking this".

**Ask the supervisor** on the Loops screen opens a Claude session that knows
how the loops work, as an ordinary pane. Ask it anything: what a loop is doing,
why a PR did not merge, what to do about an item.

A stuck agent pushes "An agent is stuck". The notification opens the question
with its options as buttons, and **Send and resume** posts your answer and puts
the item back in the queue. The other two pushes are a merged PR and a company
PR sent to the supervisor.

## Development

```bash
./scripts/build.sh             # vite build + go build -> bin/remux
SKIP_UI=1 ./scripts/build.sh   # Go only

./bin/remux --local            # http://127.0.0.1:7399

go test ./...                  # includes live tests against a remux-test session
cd web && npm test             # the ANSI converter
```

The live tests create their own throwaway window inside a `remux-test` session
and ask tmux which session a pane belongs to before sending a single key. They
skip if that session does not exist:

```bash
tmux new-session -d -s remux-test
```

## Known limits

- Status badges are regex heuristics over terminal output, all in
  `internal/agent/status.go`. When Codex or Claude Code changes its rendering
  they degrade to `unknown`; they never break reading or sending.
- v1 mirrors the rendered terminal screen. Nothing is parsed into speaker
  turns, which is what makes it work with any agent and any TUI, and what stops
  it breaking when one of them changes.
- Scrollback is whatever `capture-pane -S -N` returns (400 lines by default).
  Panes in an alternate screen (vim, htop) have no history - only the visible
  screen.
