# Build instructions for agents

You are building **remux** from scratch, unattended, overnight. Kevin is asleep. He will read the
result in the morning.

Read these two files before writing any code:

- `docs/plan.md` - the full design. It is the spec. Follow it.
- `docs/ui-mock.html` - the UI. **Port it, do not redesign it.** Open it and read the CSS and the
  markup. The palette, spacing, component structure and all 9 screens are already decided.

If the plan and this file disagree, this file wins.

---

## 1. Read this section twice: do not disturb the live workspace

Kevin's Mac is running **real work right now** - roughly 26 tmux panes across 3 sessions, several of
them live Codex and Claude Code agents doing real tasks. Killing one, sending stray keys into one,
or resizing his layout is the worst thing you can do tonight. It is worse than not finishing.

### Never run these against anything

```
tmux attach-session          tmux -CC            tmux kill-server
tmux select-window           tmux select-pane    tmux switch-client
tmux resize-pane             tmux resize-window
tmux set -g ...              tmux source-file
```

`attach-session` and `-CC` make you a tmux client, and a client's terminal size renegotiates pane
dimensions. That resizes Kevin's laptop layout. This is the single constraint the whole design is
built around. The product itself must never call these either.

### Read-only against the real workspace is fine

`list-sessions`, `list-windows`, `list-panes`, `capture-pane`, `display-message`. Use these freely
to test `/api/tree` and the status classifier against real data. That is the point.

### All writes go to a scratch session only

Create it once:

```bash
tmux new-session -d -s remux-test
tmux new-window -d -t remux-test -n scratch
```

Every `send-keys`, `kill-pane`, `kill-window`, `rename-*` and `new-*` you test must target a pane
inside `remux-test`. Before any destructive test, confirm the target belongs to it:

```bash
tmux display-message -p -t "$PANE" '#{session_name}'
```

If that is not `remux-test`, stop. Do not proceed.

Clean up when you finish: `tmux kill-session -t remux-test`.

### Prove you did no harm

`scripts/laptop-invariant.sh` snapshots the active window and every pane size outside the scratch
session, then diffs it.

```bash
./scripts/laptop-invariant.sh before
# ... run your tests ...
./scripts/laptop-invariant.sh after     # exits non-zero if anything moved
```

Run this around **every** phase that touches tmux. If it fails, you broke the core promise of the
product. Stop, find out why, fix it, and write what happened in the build log.

---

## 2. Environment

Already installed, verified:

| Tool | Version | Path |
|---|---|---|
| Go | 1.24.0 | |
| Node | v22.20.0 | |
| npm | 11.12.0 | |
| tmux | 3.5a | `/usr/local/bin/tmux` |
| gh | authenticated as `viminizer` | |

Not installed, and **you cannot install it tonight**: Tailscale. That is fine, see phase 7.

- Module path: `github.com/viminizer/remux`
- Default port: `7399`
- Dependencies, and nothing else without a reason written in the commit message:
  `tailscale.com`, `github.com/coder/websocket`, `github.com/SherClockHolmes/webpush-go`

---

## 3. Build order

Each phase has an exit test. **Do not move on until it passes.** Commit at every exit.

Phases 1-6 give Kevin a working app on his laptop. They matter most. If you run out of time, having
1-6 finished and 7-9 untouched is a much better morning than nine half-finished phases.

### Phase 0 - scaffold
`go.mod`, directory layout from the plan, `scripts/build.sh`.
**Exit:** `go build ./...` and `go vet ./...` are clean.

### Phase 1 - tmux layer and REST
`internal/tmux` (exec wrapper, ID validation, `Tree()`, previews) and `internal/api` with
`--local` serving plain HTTP on `127.0.0.1:7399`.
**Exit:** `curl localhost:7399/api/tree` returns Kevin's real 3 sessions and ~26 panes, with correct
commands and paths. Invariant script passes.

### Phase 2 - WebSocket streaming
Per-connection poller: capture, hash, push only on change. Tree diff every 2s.
**Exit:** subscribe to a scratch pane, run `echo hi` in it, get exactly one `snap` within ~400ms, and
**zero** snaps while the pane is quiet. Verify the quiet case explicitly - a poller that pushes every
tick looks fine in a browser and destroys the battery.

### Phase 3 - actions
Send text (with bracketed paste for multi-line), keys from the allowlist, interrupt, create, rename,
kill, focus.
**Exit:** in the scratch session, a multi-line prompt with `submit:false` arrives as multiple lines
and is not submitted; `keys:["Enter"]` then submits it. Invariant script passes.

### Phase 4 - agent status
`internal/agent/status.go`. Classify `waiting` / `busy` / `idle` / `shell` / `unknown`.
**Exit:** run it read-only over the real workspace and print a table of pane id, command, and
verdict. Sanity-check it yourself: panes running `codex` and Claude Code should not all come back
`unknown`. Note the accuracy in the build log.

### Phase 5 - web UI
React + Vite + TypeScript, plain CSS. Port `docs/ui-mock.html`: same palette, same components, same
9 screens. Wire it to the real API and WebSocket.
**Exit:** `--local` in a browser at 390px width behaves like the mock, against real panes.

### Phase 6 - single binary
`go:embed` the Vite build. `scripts/build.sh` runs both.
**Exit:** `./bin/remux --local` serves the UI with no `web/dist` on disk beside it. Record the binary
size in the build log.

### Phase 7 - tsnet
`ListenTLS`, `WhoIs` auth middleware on both REST and the WebSocket upgrade.
**You cannot finish this tonight** - enrolling the node needs Kevin to open a login URL in a browser.
Write the code, make it compile, keep `--local` working, and leave the login to him.
**Exit:** `go build` clean, `--local` unaffected, and a clear note in the build log about what Kevin
must do.

### Phase 8 - push notifications
VAPID keypair generation, subscription storage, the watcher, service worker `push` and
`notificationclick`.
**Exit:** keys generate, a subscription round-trips, the watcher fires on a simulated transition into
`waiting`. Real delivery needs HTTPS, so it cannot be tested until phase 7 is enrolled.

### Phase 9 - offline and the rest
Service-worker snapshot cache, stale banner, diagnostics, remaining screens.
**Exit:** with the server stopped, a previously opened pane still renders, dimmed, age-stamped, input
disabled.

---

## 4. Testing rules

- **Write Go tests for logic that does not need tmux**: ID validation, `list-panes` output parsing,
  ANSI handling, the status classifier (feed it captured fixtures), bracketed-paste construction.
  Capture real fixtures with `capture-pane` and commit them under `internal/agent/testdata/`.
- **Never assert a phase works because it compiles.** Run it.
- **Validate any HTML or JS you write before declaring it done.** The mock in this repo shipped
  broken once because a quote ended a string early and the page rendered blank with no error:
  ```bash
  node -e "new Function(require('fs').readFileSync('file.js','utf8')); console.log('OK')"
  ```
- Keep `main` building. If a phase leaves the tree broken, fix it before you commit.

## 5. Committing

Work directly on `main`. It is a private solo repo, and Kevin wants to wake up to progress on it.

- Commit when a phase exits, or when a self-contained fix is done. Use your judgment - somewhere
  around 6 to 15 commits for a night is healthy. One giant commit at 6am is not.
- Push after every commit. If you crash at 3am, the work should still be on GitHub.
- Subject line imperative and specific. Body explains **why**, not what the diff already shows.
- Add the trailer: `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`
- Never force-push. Never rewrite pushed history. Never commit `bin/`, `web/dist/`,
  `web/node_modules/`, tsnet node keys, or the VAPID private key. Those keys are real credentials -
  the node key would let someone impersonate the Mac on Kevin's tailnet.

## 6. When you get stuck

- **Do not stall.** If a phase is blocked, write down why, skip it, and start the next one. A blocked
  phase 4 must not cost you phases 5 and 6.
- **Do not invent facts about Kevin, his employer, or his setup.** If you need one, note the question
  in the build log and pick a documented default.
- **Do not do anything that needs him.** No Tailscale login, no browser permission prompts, no
  installs that need a password, no `sudo`.
- **Do not expand scope.** No extra features, no refactors of things that already work, no swapping
  the stack. If you think something in the plan is wrong, build it as specified and write your
  objection in the build log.

## 7. Leave a morning report

Write `docs/build-log.md` as you go, not at the end. Kevin reads this first.

Cover:

1. What works, phase by phase, and how you verified it. Name the commands you actually ran.
2. What does not work, and why.
3. Anything you had to decide that the plan did not cover.
4. Questions for Kevin, and where you used a default instead of guessing.
5. Exactly what he has to do by hand - the Tailscale login, the two admin-console toggles, adding
   it to the home screen.
6. Confirmation that `laptop-invariant.sh` passed, and that `remux-test` is cleaned up.

Be honest in it. If a phase is half-done, say half-done. A truthful report of six finished phases is
worth far more than a cheerful one that overstates nine.
