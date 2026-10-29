# Build log

Built overnight, 2026-09-08 into 2026-09-09, unattended.

**Short version:** phases 0-9 are all implemented and their exit tests pass,
except the parts that genuinely cannot be done without you - enrolling the
tailnet node and receiving a real push. `laptop-invariant.sh` passed after
every phase that touched tmux, and `remux-test` is cleaned up.

Three things below need you: the Tailscale login, two admin-console toggles,
and adding it to the home screen. They are in "What you have to do by hand".

---

## What works, and how it was verified

### Phase 0 - scaffold
`go.mod` with the `toolchain go1.26.8` line, the directory layout from the
plan, `scripts/build.sh`.

Verified: `go build ./...` and `go vet ./...` clean.

### Phase 1 - tmux layer and REST
`internal/tmux` (exec wrapper with a forbidden-command guard, ID validation,
`Tree()`, cached parallel previews) and `internal/api` with `--local`.

Verified against your real workspace, not a fixture:

```
python3 /tmp/tree.py       # via /api/tree on 127.0.0.1:7399
sessions: 4   windows: 20   panes: 29
  $1  1yegabiz     windows=5
  $2  saas         windows=10   attached
  $3  source-open  windows=3
  $30 remux-test   windows=2     <- the scratch session
```

That is your real 3 sessions / 17 windows / 27 panes plus my scratch session.
Commands and paths were correct, including Claude Code panes reporting
`pane_current_command` as a version number (`2.1.263`) and the live task in
`pane_title` (`✳ Separate worktree`).

`./scripts/laptop-invariant.sh after` -> PASS.

### Phase 2 - WebSocket streaming
Per-connection poller: capture, SHA-256, push only on change. Tree diff every
2 s, also hashed.

The quiet case is the one that matters and it is asserted explicitly in
`internal/api/ws_test.go` - a poller that pushes every tick looks identical in a
browser and only shows up as a warm phone. `TestSnapOnlyOnChange` requires:

- exactly **1** snap on subscribe,
- **0** over the next 3 seconds of silence (the poller ticks 5x/second there),
- a snap carrying the echoed marker after the pane changes,
- **0** again once it settles.

Writing that test found a real race: the capture runs outside the lock, so a
pane unsubscribed mid-capture still had its frame sent - which is the one thing
`unsub` exists to prevent, since the phone sends it when the screen goes off.
The poller now re-checks the subscription before sending, and
`TestUnsubStopsPolling` covers it.

### Phase 3 - actions
Send text with bracketed paste, allowlisted keys, interrupt, create, rename,
kill, focus.

`TestMultilineTextIsNotSubmitted` sends `echo remux_alpha\necho remux_beta`
with `submit:false` and asserts both lines are staged and neither has run, then
that `keys:["Enter"]` runs both.

**A note on the harness, because my first attempt was wrong.** I initially drove
this with `cat`, which proved nothing: bracketed paste only does anything when
the receiving application opts into it, and `cat` reading a tty in canonical
mode submits on every newline regardless. zsh does honour it, so the test drives
that. The contrast, observed directly in the scratch pane:

```
with bracketed paste:     both lines staged, then Enter -> aaa, bbb
without:                  echo ccc ran immediately, echo ddd left dangling
```

That second line is exactly the failure the design exists to prevent - one
instruction reaching an agent as several half-finished ones - so
`TestWithoutBracketedPasteItWouldSubmitEarly` pins it.

`./scripts/laptop-invariant.sh after` -> PASS.

### Phase 4 - agent status
`internal/agent/status.go`, all patterns in one file. Fixtures under
`internal/agent/testdata/` are real captures from your workspace, except
`waiting-choice.txt` and `busy-working.txt`, which I produced in the scratch
session because no live pane was in either state at the time (see Questions).

Run read-only over all 27 real panes:

```
totals: {'idle': 21, 'shell': 7, 'busy': 1}      # zero unknown
```

The one `busy` pane was genuinely mid-task. **Accuracy: 29/29 correct after one
fix.**

That fix matters. The first run classified pane `%13` as `waiting` because a
paragraph in it contained the line `6390. No collision.` - a port number
followed by a sentence starting with "No" matched the numbered-choice pattern.
`waiting` is not just a badge: it sorts a pane to the top of the drawer and
fires a push notification, so that would have woken your phone for a pane that
needed nothing. The pattern now requires a single digit and at least two
options on screen, since a real choice always offers more than one.

### Phase 5 - web UI
React + Vite + TypeScript, plain CSS, no component library, no router library.

The CSS is lifted from `docs/ui-mock.html` rather than rewritten - same
palette, same class names, same spacing. I removed only the mock's own
scaffolding (the screen switcher and the phone frame) and made the shell
full-viewport with a drawer that pins open above 900px.

Verified by driving the real UI in a browser against real panes, at a 390px
viewport (rendered in a 390px iframe, because window resizing was not available
in this environment):

| | |
|---|---|
| Horizontal overflow | none |
| Chrome heights | 63 top bar + 51 key pad + 72 composer (plan budgeted 62/50/68) |
| Drawer at 390px | overlays, translated -308px |
| Drawer at >=900px | pinned beside the content, hamburger hidden |
| ANSI rendering | 681 coloured spans from real captures, 256-colour and truecolor, no escape leakage |
| OSC 8 hyperlinks | rendered as real anchors (your captures are full of them) |
| Deep link `#/p/%2583` | routes to that pane |
| Composer | two-line prompt typed in the browser arrived in the scratch pane as one bracketed paste, both lines ran |

Two bugs the browser found that reading the code would not have:

1. The composer read **"Message 2.1.263…"**, because Claude Code reports its
   version as `pane_current_command`. `displayCommand()` maps it back to
   `claude`, matching the mock.
2. **The stale treatment did not engage at all.** The ported CSS keys the
   dimmed output and the greyed-out composer and key pad off `body.offline`,
   and nothing was setting that class - so a cached pane rendered at full
   opacity with a live-looking composer, and the banner was carrying the entire
   message on its own. That is precisely the failure the offline design exists
   to prevent. Fixed, and re-verified below.

`web/src/ansi.ts` has 20 tests (`cd web && npm test`), including that pane
content cannot inject markup, that a `javascript:` URL is not turned into a
link, and that a quote inside an OSC 8 URL cannot break out of the `href`
attribute.

### Phase 6 - single binary
`go:embed` of the staged Vite output; `scripts/build.sh` runs both.

Verified by copying **only** the binary into an empty directory and running it
there: `/`, both hashed assets, `manifest.webmanifest`, `sw.js` and `icon.svg`
all served, and an unknown deep path serves the app shell so a hard reload on
`#/p/%14` works.

**Binary: 21 MB stripped (22,323,792 bytes).** Unstripped it is 31 MB, matching
the measurement in the plan, so `build.sh` always strips.

RSS: **14.3 MB idle**, **15.7 MB** after a full tree with previews of 29 panes.
`/api/tree` 19-23 ms; `/api/tree?preview=1` ~30 ms for 29 panes captured in
parallel.

### Phase 7 - tsnet
`ListenTLS`, `WhoIs` middleware on both the REST routes and the WebSocket
upgrade, `Explain()` for the MagicDNS / HTTPS-certificates failure.

Compiles, `--local` unaffected, and the node **does** come up - it printed a
real login URL and waited. It is not enrolled, because that needs you.

### Phase 7.5 - install experience
`install`, `uninstall`, `restart`, `status`, preflight on every start, the
admin-toggle explanation, and a terminal QR of the tailnet URL.

Full round trip tested: `install` -> `status` (showing the loaded pid and the
node state) -> `uninstall`, leaving no plist, no binary and no process.

**This phase found the worst bug of the night**, and it is the one most likely
to have bitten you on a real download.

`install` originally pointed the plist at whatever binary you ran it from. Run
from this repo - which lives under `~/Desktop` - launchd reported the service as
`running` with a healthy pid, and it did absolutely nothing. Empty stdout log,
empty stderr log, no error anywhere. Sampling the process showed it hung inside
`dyld`, in `__open`, before `main()` ever ran.

The cause is macOS TCC: `~/Desktop`, `~/Documents` and `~/Downloads` are
protected, and a LaunchAgent has no consent to read them, so it waits for an
approval that a background job can never be granted. An ad-hoc code signature
made no difference. Copying the identical binary to `~/.local/bin` and changing
nothing else made it start instantly.

`install` now copies the binary to `~/.local/bin/remux` and points launchd
there. That also means deleting the download later no longer breaks the
service, and `restart` refreshes the copy from whichever binary invoked it.

I say "most likely to have bitten you" because the documented install is
"download to `~/Downloads`, `./remux install`" - which is exactly the failing
path, and it fails silently while reporting success.

Two smaller fixes in the same area:

- The plist gained `ThrottleInterval 10`. With `KeepAlive` and no throttle, a
  start that fails fast - a tailnet with HTTPS certificates off - would have
  launchd respawning remux in a tight loop instead of leaving one readable
  error.
- tsnet reports the enrolment URL through its *user* logger, which goes to
  stderr among internal chatter. Under launchd that buried the single most
  important line of the whole install in an error log, while stdout promised
  "a login URL will appear below". It is now intercepted and printed framed on
  stdout, with stderr left clean:

```
  +----------------------------------------------------------+
  |  One-time setup: this Mac needs to join your tailnet.    |
  +----------------------------------------------------------+

  Open this and approve the device:

      https://login.tailscale.com/a/...
```

### Phase 8 - push notifications
VAPID generation, subscription storage, the watcher, service-worker `push` and
`notificationclick`.

Verified without HTTPS: the keypair generates at mode 0600 and survives a
restart (a regenerated key would silently invalidate every subscription);
re-subscribing the same endpoint replaces rather than duplicates; subscriptions
persist across reopen; and the watcher fires **only** on a transition into
`waiting` - never on a pane already waiting, never when the phone has that pane
open, and never on `busy -> idle` unless that second toggle is on. A test also
pins that the payload has four fields and no pane content.

One defect fixed here: the Settings notification switches wrote to
`localStorage` and nothing else, but the watcher runs in the Go process and
reads its own config - so the switches looked like they worked and changed
nothing. They now round-trip through `GET`/`PUT /api/settings`.

**Real delivery is untested.** It needs the tailnet certificate.

### Phase 9 - offline and the rest
Service-worker shell cache (network-first, never the API), snapshot cache in
`localStorage`, stale banner, all nine screens.

Verified in the browser by stopping the server with a pane open:

```
staleBanner:            "Cached view - last reached 13 sec ago"
outputOpacity:          0.62
composerOpacity:        0.4     pointerEvents: none    disabled: true
keypadOpacity:          0.4     pointerEvents: none    disabled: true
topDot:                 "dot stale"
drawerFoot:             "Offline - Settings"
cached content:         still rendered (2242 chars)
```

Then restarting the server: banner gone, opacity back to 1, composer enabled,
footer "Connected" - reconnect via the backoff, no reload.

---

## What does not work

1. **The tailnet node is not enrolled.** It needs you to open a login URL. The
   code path runs and prints the URL; nothing beyond that could be tested.
2. **Push has never actually been delivered.** Web Push needs a service worker,
   which needs a secure context, which needs the tailnet certificate. Every
   piece around it is tested; the wire itself is not.
3. **`WhoIs` has never returned a real identity.** The auth middleware compiles
   and is wired to both the REST routes and the socket upgrade, but with no
   enrolled node there was no way to get a 200-from-you / 403-from-someone-else
   result. This is the largest untested surface in the build. Treat the first
   tailnet connection as the real test.
4. **Nothing has run on a phone.** No Android keyboard, no home-screen install,
   no real touch target. The 390px verification was a desktop Chrome iframe.

---

## Decisions the plan did not cover

- **`go:embed` cannot reach `../../web/dist`**, which is what the plan's file
  listing implies. `build.sh` stages `web/dist` into `internal/web/dist` and
  embeds from there. Only `.gitkeep` is committed, which is enough for
  `go:embed` to compile on a fresh clone without checking in a built bundle.
- **Install copies the binary to `~/.local/bin/remux`.** Forced by the TCC
  finding above.
- **Enter does not submit on a touch device.** With a phone keyboard, Enter has
  to insert a newline or a multi-line prompt is impossible to type; the send
  button is the only submit there. On a pointer device Enter submits and
  Shift+Enter inserts a newline. There is a "Submit with Enter" switch in
  Settings either way.
- **Row titles fall back more aggressively than specified.** `pane_title` is
  ignored when it is a bare path, a `.local` hostname, or the command repeated
  back - several of your panes have those - so the window name is used instead.
- **`GET`/`PUT /api/settings`** was added; it is not in the plan's API list.
  Without it the notification toggles could not work.
- **`GET /api/naming`** was added, also not in the plan. Naming panes is the
  only thing remux spends money on and the only thing it does with nothing to
  look at: a model call that never happened, one that failed, and one that
  found every name still correct all leave the panes exactly as they were. The
  log holds failures only, so "is it actually running" had no answer short of
  reading `remux.log` over ssh. It serves a ring buffer of the last 24 runs
  from memory - tier, duration, prompt size, every name written and where it
  came from. It has a screen of its own, reached from the switch in Settings:
  it started as a block under that switch and outgrew it the same day, for the
  reason the key pad did - the sections around it are fixed and this one grows
  with every call.
- **Live Go tests create their own window** inside `remux-test` rather than
  sharing one. Go runs packages' tests in parallel and a shared scratch pane
  had two suites typing into each other.

---

## Questions for you

1. **`waiting` and `busy` fixtures are synthetic.** Every pane in your
   workspace was idle or shell all night, so I produced those two fixtures in
   the scratch session from the strings in the plan and the mock. The
   classifier's `waiting` patterns have therefore never been checked against a
   real Codex approval prompt or a real Claude Code permission dialog. **The
   most valuable thing you can do is capture one of each when you next see
   them** and drop them into `internal/agent/testdata/` - that is the
   single weakest point in the build.
2. **`AllowLogin` self-configures to the first tailnet identity that connects.**
   That is what the plan specifies and it is convenient, but it does mean the
   first connection wins. If you would rather pin it, set `allowLogin` in
   `~/.config/remux/config.json` before the first connection.
3. **The done-notification default.** `busy -> idle` is off, per the plan. I
   suspect you will want it on for long agent runs, but I left the documented
   default alone.
4. I did not name a release process or set up CI - out of scope, and
   `gh` auth was present but unused.

---

## What you have to do by hand

```bash
cd ~/Desktop/github/workspaces/saas/remux
./scripts/build.sh
./bin/remux install
tail -f ~/.config/remux/remux.log
```

1. **Open the login URL** from that log and approve the device.
2. **Turn on two toggles** at <https://login.tailscale.com/admin/dns>:
   **MagicDNS** and **HTTPS Certificates**. If they are off, `ListenTLS` fails;
   remux catches that and names both, but it cannot flip them for you.
3. **On the phone**: install Tailscale and sign in, scan the QR code remux
   prints, then Chrome menu -> **Add to Home screen**.
4. Then the two checks that could not be done here: confirm a request from
   your identity gets through and the socket stays up, and confirm a push
   actually lands on the lock screen.

I deliberately left it **uninstalled** - the round trip was tested and then
undone, so there is no LaunchAgent on your machine right now and no background
process of mine running. `~/.config/remux/` does contain a VAPID keypair and
tsnet node state from the install test; delete that directory for a clean
slate.

---

## The laptop

`./scripts/laptop-invariant.sh after` passed after every phase that touched
tmux - phases 1, 2, 3, 4, 5, 7.5 and the final run:

```
PASS: laptop untouched (27 panes unchanged)
```

Never used, at any point: `attach-session`, `-CC`, `kill-server`,
`select-window`, `select-pane`, `switch-client`, `resize-pane`,
`resize-window`. The tmux layer refuses them in code, and
`TestForbiddenCommandsAreRefused` proves it.

Every write went to `remux-test`. Both the Go test helpers ask tmux
`display-message -p -t <pane> '#{session_name}'` and refuse to send a single
key if the answer is not `remux-test`.

`remux-test` is killed. `tmux ls` should show your 3 sessions and nothing else.

---

# Addendum - 2026-09-09, first real run on the tailnet

Kevin enrolled the node, renamed the tailnet to `ocicat-hen.ts.net`, and opened
`https://remux.ocicat-hen.ts.net` on his phone. Three things that the overnight
build could not test are now tested, and two real bugs surfaced immediately.

## The big one: tmux mangles format output without a controlling terminal

**Symptom.** The app loaded fine over the tailnet, with a valid certificate,
and reported **"No tmux server running"** against a workspace of 28 live panes.

**Cause.** tmux passes `-F` format output through its vis-escaper, and what
survives depends on whether the tmux command has a controlling terminal:

| separator | from a shell | under launchd |
|---|---|---|
| `\t` (tab) | kept | **becomes `_`** |
| `\x1f`, `\x1e`, `\x01` | `\037` literal | `\037` literal |
| `\|`, `\|~\|` | kept | kept |

`treeFormat` used tabs. Every test written overnight ran from a shell, so every
one of them passed, and the format collapsed into a single field the moment
remux ran as a LaunchAgent - which is the only way it actually ships. Each
record failed the field-count check and was skipped, leaving an empty tree.

**Why it hid so well.** `Tree()` treated "no rows parsed" as an empty
workspace, so the server returned `200 {"sessions":[]}` and the UI correctly
rendered its empty state. Every layer reported success. Nothing logged a
warning. The only wrong thing in the entire chain was the answer.

**Fix.** The separator is now `|~|` - printable so it survives escaping, and
three characters because a bare `|` appears in Claude Code's own status line
(`Opus 5 (1M context) | remux | ...`). `pane_title` moved to the end of the
record and parsing uses `SplitN`, so an agent writing the separator into its
own title cannot shift the columns. `Tree()` now returns an **error** when tmux
printed records but none parsed, because that is a broken format contract
rather than an empty workspace.

**Second effect of the same escaping.** tmux decides UTF-8 support from
`LC_ALL`, `LC_CTYPE` and `LANG`. launchd sets none of them, so tmux concluded
the client was ASCII-only and replaced every non-ASCII character in format
output with `_` - Claude Code's `✳ Separate worktree` arrived as
`_ Separate worktree`. tmux commands now run with a UTF-8 locale guaranteed,
set in Go rather than in the plist so it holds however remux was started.

`capture-pane` was never affected: byte-identical in both contexts, all 285
escape sequences intact. Only `-F` output goes through the escaper.

**Method, for next time.** Guessing got nowhere. What worked was building a
throwaway diagnostic against `internal/tmux`, running it under a real
LaunchAgent, and printing the raw bytes - which showed
`"$1_1yegabiz_0_@1_..."` immediately. Anything that only runs under launchd
needs to be tested under launchd; a shell is not a substitute.

## The pinned identity did not survive a restart

`AllowLogin` was set in memory on first connect and never written anywhere, so
every restart re-opened the claim to whoever connected next. Spotted in a
restart log still reading "allowed identity: the first tailnet user to
connect" after it had already pinned. It now persists to `config.json`.

## Now verified, and previously not

- **`WhoIs` returns a real identity.** `auth: pinned allowed login to
  ruxcodes@gmail.com`, persisted, and `remux status` shows it. This was the
  largest untested surface in the overnight build.
- **`ListenTLS` issues a real certificate.** Chrome loaded the site over HTTPS
  with no warning, which is also what makes the home-screen install possible.
- **A tailnet rename is survivable.** Renaming to `ocicat-hen.ts.net` needed
  only `remux restart`; the node identity persisted and a new certificate was
  issued. Worth doing before the PWA install, not after.
- **The whole stack works end to end from a phone**: 28 real panes, grouped by
  session, over the tailnet.

## Push delivery, verified end to end

Triggered deliberately in a `remux-test` scratch pane. The sequence matters,
because a `zsh` pane is classified `shell` unconditionally and can never reach
`waiting`:

1. Scratch pane created; the watcher recorded it as `shell`.
2. `python3` started, so `pane_current_command` became `Python` - not a shell,
   so the classifier reads the screen.
3. The screen showed a three-option approval prompt, matching the tightened
   numbered-menu rule (single digit, at least two options).
4. `shell -> waiting` is a real transition, so it fired exactly once.

The notification arrived on the phone, and tapping it opened that pane
directly - so `notificationclick` and the `#/p/%131` deep link both work.

That closes the last of the four items the overnight build could not test.

## Still untested

- **The 403 branch.** A *different* tailnet identity being refused has never
  executed, because there is only one identity on this tailnet. The allow path
  is proven; the deny path is not.
- **`busy -> idle` notifications**, which are off by default.
- **Long-run behaviour**: nothing has run for days, so nothing is known about
  the watcher's memory over time or how the subscription behaves when FCM
  rotates the endpoint.

## Corrections to the report above

The overnight report's phase 1 and phase 4 results were true for a shell and wrong
for production: those pane counts and status verdicts were all gathered through
`--local` from a terminal. They were not evidence that the LaunchAgent could
see tmux at all. The claim "verified against your real workspace, not a
fixture" was true; the unstated assumption that a shell and a LaunchAgent
behave alike was not.

---

# The GitHub screen (#38)

Built 2026-09-10/11. `docs/gh-mock.html` is the design; this is the port.

## What it does

A GitHub screen at `#/gh`, reached from one pinned row at the top of the
drawer. Two tabs: an Inbox of what is waiting for you, and a watchlist of
repos. From a repo you get its issues and pull requests; from a row you get the
item, its checks, its thread, and the tmux pane already open in that repo.

## The four decisions worth knowing

**gh is the transport.** The server shells out to the `gh` CLI already logged
in on this Mac, the same way `internal/tmux` shells out to `tmux`. No OAuth
flow, no token stored by remux, nothing on the phone. Losing the phone leaks
nothing. The cost is that `gh auth login` is a laptop-only fix, so that failure
gets a screen of its own rather than a banner.

**GraphQL, not REST, for issues.** The REST issues endpoint returns pull
requests in the same list. On `apache/shardingsphere` a page of 30 records came
back as 14 issues and 16 pull requests - half an empty screen. The GraphQL
issues connection holds issues and nothing else. It also fixes the counts:
`open_issues_count` counts pull requests as issues, while GraphQL gives the two
totals separately, and one aliased document covers the whole watchlist for one
API point instead of a request per repo.

**One poller for the whole server.** Three `gh` calls a minute cover the entire
screen: repo counts, the inbox searches, notifications. Per-repo pull request
lists are *not* polled - shardingsphere alone means 22 pull requests carrying
76 status checks each, six seconds of JSON to render one word - so a repo card's
"2 of yours, 1 red" is derived from the inbox instead of fetched.

**The inbox is not scoped to the watchlist.** The watchlist answers "what am I
keeping an eye on"; the inbox answers "what is waiting for me". An issue
assigned to Kevin in a repo he never added is exactly what he would want to
find there.

## Verified against the real workspace

| Check | Result |
|---|---|
| Repo counts | 8 repos, true numbers (186/22 shardingsphere, 107/2 seoul-wedding-client) |
| Issue paging | page 1 and 2 both a full 30 rows, 60 distinct, no overlap |
| PR rows | checks, review decision, conflicts, branch, +/− all present on 22 PRs |
| PR filters | Yours 2, All 22, Needs your review 0, Drafts 3 - all true |
| Detail | 77 named checks, failures sorted first, green ones folded away |
| Pane matching | 22 of 24 real panes into 7 repos; both misses are `~/.config` |
| Add repo | opens on "Open in a pane right now · 6", watched ones ticked |
| Send to pane | `gh issue develop 251 … --checkout` **typed and not submitted** |
| Layout | 390px wide, zero horizontal overflow on every screen |
| `laptop-invariant.sh` | passed; the only write was one scratch window, killed after |

## Bugs this found in existing code

**A CLI flag was persisting into the config file.** `--port`, `--lines`,
`--poll` and `--hostname` all overwrite the in-memory `Config`, and every
settings save wrote that copy back. So running a development server once with
`--port 7401` moved the installed service to 7401. It was always true; adding a
repo made it easy to hit, because every add saves. Every runtime write now goes
through `config.Update`, which reads the file first. Covered by
`TestSettingsSaveDoesNotPersistFlags`.

**A slow response was reported as "offline".** A command timeout was
classified as `ErrOffline`, so a slow GitHub told Kevin his phone had no
network. `ErrTimeout` is now separate, and the budget went from 15s to 45s
because `gh pr list` on shardingsphere measures six seconds on a good day and
has been seen past fifteen.

**A missing `gh` given as a path was not detected.** `exec` reports that two
ways and only one was handled.

## Known gaps

- **Push delivery is untested**, as with phase 8: it needs HTTPS, so it cannot
  fire under `--local`. The transition rule itself is covered by tests
  (`internal/push/github_test.go`), including the two that matter - the first
  snapshot after a restart is a baseline, and a failed poll is never treated as
  fresh.
- **State 4c in the mock still has the layout problems Kevin flagged.** The
  port was built from 4d, which is the properly designed one, so the shipped
  screen does not inherit them - but the mock file itself is unfixed.
- **The repo card avatar** is the first two letters of the name, so
  `shortlist` and `shardingsphere` both read `sh`. The mock had the same
  collision. Left alone.
- **Search inside a repo** is not built. The mock showed a `⌕` chip on the
  filter row; shipping a chip that does nothing is worse than not having it, so
  the filter rows carry only what works.

## What Kevin has to do

The watchlist is already in `~/.config/remux/config.json` - eight repos, added
through the API while testing. The installed LaunchAgent is still running the
old binary, so:

```bash
./scripts/build.sh && remux restart
```

Nothing else. `gh` is already logged in, and there is no new permission to
grant.

# The names were wrong, and the prompt was not why

Kevin's report was that the titler names every pane but most of the names do
not describe the job being done. Reading the twenty-two live panes against
their names, three separate things were wrong, and only one of them was the
wording.

## The screen budget was being spent on furniture

`maxScreenChars` bounds one pane's contribution to the batched prompt, and it
was spent from the bottom of the screen. What is at the bottom of these panes
is not work. It is two full-width rules around the composer, a status bar, and
a hint line.

On a 195-column pane each rule costs 195 characters. Pane 7 is 195 columns, and
its entire 1200-character allowance reached no further than its own status bar
- the model was handed a project name, a directory, an empty composer, and
nothing else, then asked what the agent was working on. It answered with the
name of the pane above it in the batch: "scout issue 190" became "implement
issue 190" on a pane in a different repo.

`tailScreen` now drops blank and rule-only lines before the budget is applied.
Whole-line only, so the status bar survives - it draws its token meter out of
the same block characters, but it has words beside it, and it is the only place
the last request appears.

This made the prompt smaller in tokens while carrying far more work. A
195-character run of U+2500 is not free; ripping it out paid for the budget
going from 1200 to 1500. Measured on the live workspace the whole prompt moved
31.0KB to 32.3KB, and its non-ASCII count fell to 652 characters.

## The current name was being read as the answer

`current name:` was printed above the screen. Ahead of the evidence it reads as
the answer, and the model copied it: on a live run every pane but the two empty
ones came back byte-identical to the name it already had, stale ones included.
It now comes after the screen, labelled as written from an older screen, and
the rules ask for it to be checked rather than kept.

## A pane with nothing on it was named anyway

The prompt said a pane with no name must be given one, and a rough name beats
none. For a screen with work on it that is right. For a freshly cleared session
it forced a guess, and the guess came from the neighbours: `%24`, showing a
banner and `/clear` and nothing else, was named "fix issue 270" - which is what
`%14` beside it was doing. Two panes reading identically is the problem this
feature exists to solve.

`NONE` is the narrow hatch for that one case, and it clears the name rather
than leaving it, because a specific name on a pane cleared an hour ago is the
kind of wrong that gets believed. It costs something: an unnamed pane is
captured on every tick and asked about every `askEvery`, where a named one is
not. That is the price of not inventing, and blank panes are few.

`Run.Cleared` counts them, so the journal shows a run that refused to guess
instead of showing a run that did nothing.

## Verified against the twenty-two live panes

One run of the real chain on the real workspace, checked pane by pane against
what was on the screens:

- **8 names corrected**, every one of them stale in the same way - they named
  work the screen showed as finished. "issues 129 215" became "start api 128",
  which is what the recap line two rows up had been saying all along. "resolve
  pr 39385 conflicts" became "get routing decision", on a screen that says the
  conflicts are fully answered and what is still open is an unanswered design
  question.
- **2 panes correctly refused.** Both were cleared sessions. Both had been
  carrying a confident wrong name.
- **The rest held.** No pane that was right was made worse.

## Idle panes keep their names, on purpose

A pane that is already named and idle is never re-captured, so it is never
re-asked, so its name freezes at whatever it was when the pane went quiet. That
is exactly when a name goes stale, because the last thing an agent does is
finish: `%7` still reads "issues 129 215" on a screen whose recap says the next
thing is api#128.

Kevin was asked and chose to leave it. A new name would cost a capture on every
tick and a model call every `askEvery` for every finished pane on the laptop,
for a name nobody is currently reading - and the glyph beside it already says
the pane is done. The new prompt applies as panes move.

Do not "fix" this by adding the unnamed panes' `always` treatment to named
ones. It is the decision, not an oversight.

# Swipe between tabs

Three screens have a two-tab segmented control: the GitHub overlay (Inbox and
Repos) and every repo screen (Issues and PRs). The control sits at the top of
the screen, which is the one place a thumb holding a phone cannot reach, so the
only way to change tab was to move your hand.

## The gesture already had an owner

`useDrawerSwipe` listens at the app root and takes every rightward drag. Tabs
cannot simply take the horizontal axis, because the drawer is how you leave
these screens.

The rule is the one that hook already uses for mirror mode, applied one level
in: **the inner gesture owns the drag only while it has somewhere to go.** On
the first tab a rightward drag has nowhere to go, so it is passed on and the
drawer opens exactly as before, the same way a rightward drag on output that is
scrolled fully left belongs to the drawer.

`useTabSwipe` listens on the screen rather than the root, so it sees the drag
first, and where it wants the gesture it calls `stopPropagation` before the
root handler ever sees that move. Both hooks commit to an axis at the same
eight pixels, so the drawer is still `undecided` at that moment and simply
never decides. A drag that starts inside something that scrolls sideways - the
filter chips - is left alone for the same reason, which is the same question
`scrollerAt` asks in the drawer hook.

`claim()` is split out as a pure function because it is the whole of the
decision and none of the DOM, and `web/test/tabSwipe.test.js` covers it. The
test that matters is the third one: a rightward drag on the first tab is not
claimed.

## The content does not follow the finger

The drawer follows the finger because it is one panel sliding over another and
the gesture is the animation. A tab is a whole screen of rows that would have
to exist twice to be dragged between, and on the repo screen that means
fetching the pull requests in order to animate past them - a GitHub call spent
on a frame. The tab changes on release and the incoming list slides in 18px
from the side it came from, which is the part the eye needs, and it costs one
keyframe. It is off under `prefers-reduced-motion`.

## Verified in the browser

Synthetic touch pointer events against the running app, because the hook
ignores anything that is not a finger:

- swipe left on Inbox goes to Repos, swipe right goes back, drawer never opens
- on the repo screen the swipe writes the route, so `?t=prs` is in the URL and
  survives a reload, exactly as tapping the control does
- a rightward drag on the first tab passes all ten of its moves up to the
  window; a leftward drag passes none. That is the drawer cooperation, measured
  rather than assumed.
- a swipe starting on the filter chips, forced to overflow, does not change tab
- a mostly-vertical drag does not change tab

## The swipe shipped dead on the phone

It worked in a wide desktop window and did nothing on the device, which is the
shape of bug worth writing down.

`useTabSwipe` asked, at pointerdown, whether the touch had started inside
something that scrolls sideways - the filter chips - and bowed out if so. That
is the same question `scrollerAt` asks for the drawer, and on these screens it
has the wrong answer far too often.

`.list` and `.screen-body` set `overflow-y:auto`. CSS forces the other axis to
compute to `auto` along with it, so **a vertical list reports `overflow-x:auto`
too**. The moment one issue title is a pixel wider than the screen the list has
horizontal overflow as well, and every swipe inside it was handed to a scroller
that exists only as a rounding artifact. On a phone that is the normal state.
On a wide window it never happens, so it tested clean.

Measured: at 390px the inbox list reports 115px of horizontal overflow next to
1036px of vertical. Reproduced on the desktop by appending one 120%-wide child
to the list - the swipe stops working, remove it and it works again.

What separates the two is room across and none down, which is `isStrip`, and it
is a pure function with a test for exactly this case. The strip is also now
asked in the direction of travel rather than at pointerdown, so chips scrolled
to their end hand the rest of the gesture to the tab - the same rule
`useDrawerSwipe` uses for mirror mode.

### A second reason it looked broken

The tab under test was serving `index-DfMZQpw4.js` while the server had moved
on. The service worker is network-first and calls `skipWaiting` and
`clients.claim`, so it is built to pick up a new shell - but a load that lands
while the Mac is unreachable, which is exactly what a `remux restart` is, falls
back to the cached shell and pins the old bundle until the next successful
load.

Nothing is wrong with the worker. Settings already compares `__BUILD_VERSION__`
against the version the server reports and offers the update, which is the
thing to look at when a shipped change appears to have done nothing.

## The arc

Second report: swiping over the list did nothing, swiping over the warning bar
worked. That split is the tell, and it is not about the list at all.

The axis test gave up the moment `dy` was bigger than `dx`, and gave up for
good:

```js
if (Math.abs(dy) > Math.abs(dx) && Math.abs(dy) > AXIS) mode = 'drop'
```

A thumb swiping across a phone travels in an arc. Reproduced: a swipe whose
first sample is 5px across and 10px down is written off as a scroll, and then
goes on to travel 180 across and 18 down. A flat, deliberate flick - which is
what you make over a bar of text you are not expecting to scroll - passes the
same test on its first sample and works. Over a list, where the hand expects to
scroll, the arc is pronounced and the first sample is vertical.

So the failure was never about being inside the list. It was about how the
gesture starts, and the list is where it starts badly.

`axis()` now answers `across`, `down` or `unsure`, and only `down` gives up -
at twice the horizontal travel, not a hair over it. While the drag is neither
it stays `unsure` and nothing is decided. The browser is deciding in parallel
and cancels the pointer if it starts scrolling, so waiting costs nothing and
stops us calling it wrong first.

Verified against the failing gesture, which now changes tab, and against a
mostly-vertical drag, which still does not.

### Still to do

`useDrawerSwipe` has the identical test and so the identical hair-trigger. It
was left alone: the drawer is pinned at this window size so the change could
not be verified here, and shipping an unverifiable fix is what produced this
entry and the one above it.

## Giving the gesture back to the browser

Three attempts at a pointer-event swipe all worked on a desktop and did nothing
on the phone, in the same place: over the list. The fourth stopped trying.

The list is a scroll container. When a finger lands on one and starts to move,
the browser decides whether it owns the gesture before any JavaScript can
claim it, and when it takes it, it fires `pointercancel` and stops sending
moves. A thumb swiping sideways across a list starts its arc downward, so the
browser took it every time. Swipes over the warning bar worked for one reason:
there is no scroller under it, so nothing cancelled the pointer.

None of that is tunable. The axis test, the travel threshold and the strip
check were all arguing about events that had stopped arriving.

**Synthetic PointerEvents cannot reproduce it.** They are injected below the
touch pipeline, so no scroll ever starts and no cancel is ever sent. Every fix
tested clean here and shipped broken, three times. That is the part worth
remembering: the test could not fail.

So the paging is the browser's now. `TabPager` is two pages in one horizontal
scroller with `scroll-snap-type: x mandatory`. Nested scrollers on two axes are
something browsers do natively and well - this one scrolls across, the list
inside each page scrolls down, and there is no arbitration to lose. Snapping
gives the finger-following the old version had explicitly given up on.

### What it costs

`.phone` is `touch-action: pan-y`, which is what hands every horizontal drag to
`useDrawerSwipe`. A descendant cannot widen that - the effective value is the
intersection down the tree - so `.phone` itself relaxes to `pan-x pan-y
pinch-zoom` while a GitHub screen is open, and the drawer swipe stands down
there. Both screens keep a back arrow.

Both tabs are mounted, because a page has to exist to be scrolled to. On the
GitHub screen that is free - both read the one snapshot already in hand. On the
repo screen it means `gh pr list` runs when the repo opens rather than when the
tab is first tapped: about half a second, measured, in exchange for the tab
being there when you arrive.

### Two self-inflicted bugs on the way

A `ResizeObserver` reset `scrollLeft` on every callback, including the initial
one and the ones the scroll itself caused, which pinned the pager to the page
it started on. It now acts only on a real width change.

The scroll handler was coalesced with `requestAnimationFrame`, which does not
run in a hidden tab - so the sync could not be tested here at all. It reads the
scroll event directly now: a divide and a compare, with an early return on
every event but the one that crosses the half way point. The control lights the
tab you are heading for while your finger is still moving, which is what a pager
should do. For the same reason the programmatic scroll is instant rather than
smooth when the document is hidden, where a smooth scroll is deferred and may
never run, leaving the control and the pager disagreeing.

### Verified

Scroll the pager and the control follows; tap the control and the pager
follows; on the repo screen the swipe writes `?t=prs`; a deep link to `?t=prs`
lands on the second page without animating in from the first; a list inside a
page still scrolls vertically.

# Why a restart renamed the whole workspace

Kevin: "sometimes it is renaming all the 22 panes, even though I'm using a few
of them". Two causes, and the second one was mine.

## Nothing survives the process

Neither the activity gate's marks nor the ninety-second cooldown is persisted.
So on the first tree after a start, every pane is both unseen by the gate
(`!had` returns it) and never asked, and the first pass sends all of them in
one batch. Eight restarts shipping the swipe fixes is eight full sweeps.

`Pass.settle` marks the panes that arrive already carrying a name, once, on the
first tree. They keep what they have until they do something - the same bargain
idle panes were already given. A pane with no name is deliberately left out of
that: naming those is the whole point of the first pass.

## A full sweep used to be silent

Before, `current name:` sat above the screen in the prompt, the model anchored
hard on it, and most panes came back `SAME`, which writes nothing. Moving the
name below the screen and asking for it to be checked is what corrected "issues
129 215" to "start api 128" - and it is also why a full sweep now re-derives
all twenty-two and rewrites every one whose wording drifted.

Measured since: on an eight-pane batch of panes that had not moved, six came
back `SAME` and one was `NONE`. So the anchoring is not gone, and a sweep of a
quiet workspace is still mostly silent. What the restarts produced was a sweep
of twenty-two panes whose names really had drifted over a day of work, all
rewritten at once.

The sweep was always happening. The prompt change is what made it visible. Both
halves are worth keeping; what was wrong is that the sweep happened at all on a
restart.

## And the batch had no ceiling

The batch exists because the per-call floor is process startup: eight panes cost
about what one does. That argument stops holding somewhere above it. Twenty-two
panes is a 32KB prompt measured at 76 seconds against a 90 second timeout, and
past the timeout the call is killed, every tier is tried in turn, and the chain
backs off - so the most expensive run is the one that returns nothing. The log
showed 22 `signal: killed` and a backoff climbing 1m30s, 3m, 6m. Worse, a
backoff makes every pane due again at once, so the next attempt is another full
sweep and fails the same way.

`maxBatch` is 8. The rest wait for the next tick two seconds later, and only the
panes actually sent are marked as asked, so nothing waits out a cooldown it
never cost.

## And the timeout was set against the wrong measurement

Ninety seconds came from a 4.6s trivial prompt. On the real workspace: 22 panes
at 76s, 8 panes at 63s, both seen past ninety. The cost is barely about the
prompt - it is `claude -p` starting up and queueing behind everything else on a
laptop where twenty agents share one account.

So `maxBatch` alone does not buy the headroom; 63s against a 90s ceiling is not
a margin. `modelTimeout` is 150s now. Holding the slot longer is close to free -
naming is on a ninety-second cooldown anyway and one call in flight only delays
the next batch by a tick - while being killed at the timeout is the most
expensive outcome there is.

## The titler was answering the wrong question

Kevin: "i want my titler have a better titling strategy. it is a little shit
right now."

He was right, and the reason turned out not to be the prompt wording. It was
the question the whole feature was built around.

The prompt's leading rule was "the newest thing wins". On `%122` the newest
thing was `❯ done, restart it`, so the pane came back named `restart remux
service` - correct by the rules and useless for scanning, because the recap
three lines above said the session was about a monthly cost card on the Naming
screen. Same cause behind `commit #185 changes`, `push pane title fix` and
`open pr to bump jq`. Every one of those is a step inside a job, not the job.

Worse on a pane that had just finished. `%1` is Codex, and its entire 1200-char
window was the completion report:

```
• PR #457 is ready for review and assigned to @JoonsungUm.
› Ask Codex to do anything
```

So it transcribed the report: `pr 457 marked ready assigned`. `%5` hit the same
screen shape and got the *identical name*. Two panes reading the same is the one
failure this feature exists to prevent, and the design was manufacturing it.

## One session, one problem

The fact that reframed it came from Kevin, not from the code: he runs one
session per problem, holds it from the first request until the work is merged
and cleaned up, then clears it or opens a new one. He does not change goal
halfway - he keeps context small on purpose, because output gets worse as it
grows, and he compacts rather than restarting.

So the goal of a pane is fixed in its first minute and does not move for hours.
Implementing, opening the PR, fixing review comments and merging are one task
wearing different clothes. And with twenty-six panes the point of a name is that
it can be *learned* - one that changes every ninety seconds never can.

Which makes the timer the wrong mechanism entirely. `askEvery` re-asked every
pane forever with no exit: $14.76 across 432 runs in thirteen days, nearly all
of it re-deciding a question with one answer, and every one of those calls
another chance to replace a correct name with the current step.

## Name once, then stop

Two ideas replace the timer, and neither is a number someone guessed.

The model can say when it is finished. `SAME` already means "the name it has is
still right". Two of those in a row is the question being answered, and a
settled pane leaves the rotation completely - `dueForNaming` returns false and
it never enters a batch again. At rest, with every pane named and confirmed, the
chain is not called at all.

A new session announces itself for free. Both agents put their own name in
`pane_title`, the tree poll already reads that field every two seconds, and it
changes exactly when the session does - `Claude Code` becomes a generated title,
Codex's title changes outright. Comparing the *normalised* form is what makes it
usable: the raw string carries an animated spinner and moves several times a
second, so `⠧ Fix GitHub issues | shortlist` and `⠹ Fix GitHub issues |
shortlist` are the same session seconds apart.

`normalizeTitle` strips the spinner, strips Codex's ` | <dir>` suffix only when
it matches the pane's directory or project, lowercases, and returns empty for a
placeholder - `Claude Code` before it has generated a title, or a bare directory
name. Six of the twenty-six live panes show nothing but a directory and give no
signal at all; those keep the old cooldown.

## The agent's own title was free evidence all along

`%1` had finished and its screen held nothing but the report - but Codex still
knew what the session was for. Its `pane_title` read `Review PRs in Parallel`.
The titler had never looked at it.

For a Codex pane that is the only surviving record of the goal, because Codex
prints no recap. Claude Code is the mirror image: it prints a recap and lets its
title go stale. The two agents have opposite strengths and remux was using
neither. The normalised title now goes into the prompt as `agent calls itself`,
fenced exactly like the screen - it is written by the same untrusted program,
one that reads issue text and web pages and puts what it finds in its own title.

`maxScreenChars` goes 1500 to 3000 at the same time. Fifteen hundred is about
seven lines of a 195-column pane; measured against the two panes above it was a
third of what was on screen, and the request that states the goal is the first
thing to scroll away.

## What the live run caught

Eight real panes through the new prompt. Two past-tense names became imperative
- `merged pr 272` to `merge stale role claim fix`, `closed issue 299` to `fix
issue 299` - four came back unchanged, and one correctly answered NONE.

And four of those eight confirmations came back as the name *repeated word for
word* rather than the keyword `SAME`. Under the design as written those panes
would never have settled and the entire saving would never have arrived. A
repeat is now read as the confirmation it is, which also makes the prompt rule
asking for `SAME` a preference rather than a requirement.

Testing that turned up one more character of difference between settled and
never settling. `wordRe` has to allow a dot inside a word - `v0.3.62`,
`config.json` - so it swallows the full stop at the end of a sentence too. A
model answering `Review PR 269.` about a pane named `review pr 269` used to
write the extra dot, which is a rename, which puts the pane back to the start.
`clean` trims trailing punctuation off each word now. That used to be cosmetic.

## What is not done

Whether `pane_title` survives a `/compact` is unverified. Weak evidence that it
does: `%81` is titled `✳ Issue 39` and that session was compacted. If it does
not, a compaction unsettles the pane and costs one extra call - the summary is
dense with the original goal, so the name that comes back should be the same
one, which is why the prompt now says a compaction summary is the same session
continuing and names the goal it describes rather than the work it lists as
done.

## 2026-09-15 - Share push watcher work with the API (#50)

The remaining duplicate pass in #50 was the push watcher: it owned a separate
tree read and activity gate even when the API had already classified the same
workspace. It now requests the shared classified tree, accepting the same age
as the normal tree poll. This explicitly requests verdicts with no WebSocket
open, while retaining the five-second notification interval, subscription and
settings gates, first-observation baseline, cooldown, and focus suppression.
Local mode still does not start the push watcher.

Reproduced with a fake tmux executable that counts actual subprocess calls:
`go test ./internal/api -run '^TestPushWatcherReusesSharedWorkspace$' -count=1 -v`
failed before the fix with two `list-panes` and two `capture-pane` calls for one
shared request followed by a subscribed watcher tick. The same test passes
after the fix with one of each, including after the preview cache expires.

Additional verification:

- `go test ./internal/push -count=1` passed. Tests cover notification gating,
  baseline, missing verdicts, failed refresh, cooldown, shell exclusion and
  removal, alongside the existing transition and focus checks.
- `go test ./internal/api -run '^(TestPushWatcherReusesSharedWorkspace|TestClassifiedWorkspace)' -count=1 -v`
  passed. Tests also verify classification without a phone, concurrent reuse,
  stale-tree refresh, quiet capture suppression and retry after a failed first
  capture.

These focused checks use temporary fake executables and stores; they do not
connect to the real tmux server or send real push notifications.

Final verification in the isolated issue worktree:

- `go test ./... -count=1`, `go vet ./...`, and `go build ./...` all passed.
  The full suite exercised the existing live tmux tests in `remux-test`.
- `go test -race ./internal/push -count=1` and
  `go test -race ./internal/api -run '^(TestPushWatcherReusesSharedWorkspace|TestClassifiedWorkspace.*)$' -count=1`
  passed.
- `TMPDIR=/tmp/remux-issue50-verification ./scripts/laptop-invariant.sh before`
  and `after` surrounded the scratch session and full suite. The final result
  was `PASS: laptop untouched (27 panes unchanged)`. The script noted allowed
  changes to existing `@remux_*` options during the run; geometry and clients
  passed. The scratch session was verified by name and removed before `after`.

No configuration defaults or dependencies changed. Real push delivery was not
tested; this fix verifies shared polling and notification decisions locally.

## 2026-09-15 - Shift+Left keypad button

Added `⇧←` beside the arrows in the expanded keypad for Codex follow-up
questions. It sends the single tmux key `S-Left`, now in the backend allowlist.
Like the arrows, it keeps the keypad open and supports repeated presses. It
also appears automatically in Settings' built-in chip controls.

Verification:

- `go test ./internal/tmux -run '^TestNormalizeKey$' -count=1` failed on
  `S-Left` before the allowlist change and passed after it.
- `go test ./... -count=1`, `go vet ./...`, `go build ./...`, `npm test` and
  `npm run build` in `web`, and `./scripts/build.sh` passed. The stripped
  binary measured 22,742,136 bytes.
- A temporary API test, `TestShiftLeftManualWire`, sent `S-Left` through the
  keys endpoint to a raw-input reader in a scratch pane. It received
  `1b5b313b3244` (`ESC [ 1 ; 2 D`), the Shift+Left sequence. The temporary
  test was removed after validation.
- An isolated browser harness rendered the real KeyPad component and API
  client at 390px width. Tapping `⇧←` produced a POST with
  `{"keys":["S-Left"]}`, kept the pad open, fit in the viewport, and was
  disabled offline. The request was intercepted; it reached no live pane.
- The laptop invariant passed before/after the live tests with all 27 panes
  unchanged. `remux-test` already existed, so it was preserved; the tests
  removed their own temporary windows. Browser and Vite sessions were stopped.

The installed service was not restarted during verification.

## 2026-10-12 - Per-loop Claude models and trial history

Added Sonnet, Opus and CLI-default selectors for each Claude build loop and
the supervise loop independently. The individual loop Edit screen can change
the choice for the next issue. New forms choose Opus; existing loops and older
API requests preserve their current choices. Claude keeps medium effort;
Codex review and repair commands retain their settings. The CLI accepts
`--model`; the API accepts aligned `models` and a separate `superviseModel`.

The harness appends owner-only JSONL records to
`~/.config/remux/loop-runs.jsonl`: process starts/ends, requested model,
issue/PR number, stage, elapsed milliseconds and exit status. Build outcomes
link issue numbers to PR numbers, and stuck/merged events survive session
cleanup. This is evidence for a Sonnet trial, not a usage meter: it records no
tokens, billed usage, exact backend model version, prompts or agent output.
No automatic routing or escalation was added, and no live trial was started.

Verification:

- `PATH="/tmp/remux-model-test-bin:$PATH" go test ./... -count=1` passed.
  The temporary wrapper points every tmux call at the isolated
  `remux-model-tests` server. It does not target the laptop's live server.
- The lifecycle test exercises mixed Sonnet/Opus builds, an independent Opus
  supervisor, unchanged Codex review, model edits, clearing to CLI default,
  invalid edits and preservation when older requests omit model choices.
  Command tests verify the Claude model flag and unchanged medium effort;
  run-record tests verify persistence after a failed process, owner-only
  permissions and exclusion of prompt text. Existing harness fixtures use
  temporary homes so their outcomes do not pollute real run history.
- `go vet ./...`, `go build ./...`, `npm --prefix web test`,
  `npm --prefix web run build`, `./scripts/build.sh` and `git diff --check`
  passed. The stripped binary measured 23,074,304 bytes.
- Agent-browser exercised the real StartLoop component and API client in a
  temporary harness at 390px width. Choosing Sonnet for Claude 1 and Opus for
  Claude 2 submitted `models:["sonnet","opus"]` and
  `superviseModel:"opus"`. There was no horizontal overflow or browser error.
  The request was intercepted; it launched no agent. Browser, Vite and
  temporary harness files were cleaned up.
- The first suite run exposed a lifecycle assertion that expected only one
  new session; it was corrected to account for the new supervisor. Existing
  input tests also needed a clean shell in the isolated server: the user's
  long prompt wrapped input, and plain sh lacked bracketed paste. Using
  `/bin/zsh -f` at 200 columns let the final complete suite pass.
- `laptop-invariant.sh before` and `after` surrounded tmux verification.
  Final result: `PASS: laptop untouched (20 panes unchanged)`. It noted the
  active window moving independently during the run; test writes used only
  the isolated server. Its `remux-test` session was confirmed and removed.
  The live workspace and live loops received no test writes.

The installed service was not restarted. After deploying this build, use
Loops → Start loop to set one Claude build loop to Sonnet and the remaining
Claude loops and supervisor to Opus. Evaluate 10–20 bounded issues against
subsequent Codex repair runs before expanding the trial. Issue selection and
automatic usage accounting remain manual.

## 2026-10-12 - Release and install 0.3.89

Added a visible PR comment for future Claude build contributions. It names
the selected model (Sonnet, Opus or CLI default), medium effort, source issue,
scope and head commit. A marker prevents duplicate comments for the same
contribution. It identifies a build run rather than claiming the final PR is
entirely one model's work. Codex attribution is unchanged, and earlier PRs
are not backfilled from guesses. Failure to post is logged and does not keep
a PR out of review.

Verification and deployment:

- `go test ./internal/harness` passed, including a fake-GitHub test proving
  the comment carries the Sonnet model, correct PR/issue and commit, is not
  duplicated, and is not added for Codex. No real test comment was posted.
- `go vet ./...`, `git diff --check`, and
  `VERSION=0.3.89 ./scripts/build.sh` passed. The build compiles TypeScript and
  Vite and embeds the result. The stripped binary measured 23,078,408 bytes.
- Saved the previous installed binary at `/tmp/remux-before-0.3.89`.
  `./bin/remux restart` copied the new binary to `~/.local/bin/remux` and
  restarted the existing LaunchAgent. Its `version` and `status` report
  0.3.89. `launchctl print` reports running, PID 95006, never exited.
  The service log reports 0.3.89 and its tailnet URL ready.
- The Mac cannot resolve the tailnet hostname, so direct authenticated HTTPS
  verification from this terminal was unavailable. A temporary local-mode
  server on port 17401, using the isolated tmux wrapper, returned
  `version:"0.3.89"` and `ok:true` from `/api/health`. Its service worker uses
  `remux-shell-0.3.89`. Agent-browser opened the real embedded UI at 390px and
  confirmed the separate build and supervisor model selectors. The local
  server and browser were stopped afterward.
- `laptop-invariant.sh before` and `after` surrounded deployment and local
  verification. Result: `PASS: laptop untouched (20 panes unchanged)`.
  No scratch session was needed or left behind. No loop was stopped.

Versioning uses the existing Git-tag convention, `v0.3.89`. The installed app
is updated. Existing build-loop processes retain their old code until they
finish and restart; restarting the Remux service does not replace them.
Model selection and PR notes take effect in loops launched with this version.

## 2026-10-12 — 0.3.90 queue pickup and parked work

Idle polling now waits 15 seconds instead of one minute, and queue triage can
run every five minutes instead of every thirty. Triage distinguishes the
selected scope from ready work already completed for that scope, and checks
actual merged prerequisite contracts rather than requiring all issue states
to be closed.

A confirmed merged `issue-N[-scope]` PR requeues its open, unfinished source
issue. An idle builder also recovers older partial merges stranded in
`status:review`, at most once per minute. The branch identifies the source;
incidental issue references in PR text never select work. Completed scopes,
active ownership, open replacement PRs, blocked/external, postponed, held and
post-deployment items are excluded. Requeue/park outcomes are recorded in
the existing run history.

Supervision distinguishes `RESOLVED PARKED` from retry readiness. A parked
item loses ready/review eligibility and carries `blocked:external`, so
closed dependency edges cannot prematurely release a time gate. Resumption
requires an explicit verified `RESOLVED READY`. Ordinary resolved repairs
still retry; an item closed during supervision is never requeued.

Verification: existing `go test ./internal/harness`, `go vet ./...` and
`git diff --check` passed. The full `VERSION=0.3.90 ./scripts/build.sh` build
passed with the installed Node 22 toolchain: TypeScript, Vite, embedded UI and
stripped Go binary. Application installation and loop restarts follow the
reviewed merge; active issue work must finish before its process is replaced.

### PR #62 review fixes

Merged-branch cleanup, idle recovery and claims now share a nonblocking lock
in Git's common directory, including across worktrees. Recovery runs cleanup
before publishing readiness; failed cleanup or an active source claim defers
requeueing. Active full-scope branches are recognized alongside scoped ones.
Open replacement PRs on any harness branch for the source issue prevent
requeueing, without using incidental issue references in PR text.

Verification: `go test ./internal/harness`, `go vet ./...` and
`git diff --check` passed. Regression tests exercise cleanup-before-ready,
dirty/later work preservation, active source claims, cross-worktree lock
exclusion and replacement PRs across scopes. `laptop-invariant.sh before`
and `after` passed with 20 panes unchanged. No live tmux mutations or scratch
sessions were needed; cleanup tests use a tmux stub. Version 0.3.90 is preserved.

## 2026-10-12 — balanced, evidence-based PR reviews (0.3.91)

Kevin requested Claude alongside Codex, faster reviews, and findings grounded in
normal user actions. Both providers now use one review-and-repair prompt, with
changed-code scope and a concrete evidence filter. Small/docs PRs use Sonnet at
medium; ordinary PRs alternate providers under the common Git queue lock;
finance/access/migration/destructive or large changes receive deeper review.
A failed/incomplete run hands its preserved work to the other provider once.
Completion requires an explicit complete verdict; timeouts never approve.
Project review_policy=fixed preserves an explicitly chosen reviewer.

PR comments and private run history record provider, effort, route, time and
self-reported evidence-backed repairs. Those counts are not independently
audited performance scores. Build providers and effort remain unchanged.
Verification: harness regressions for classification, removed authorization,
shared worktree rotation, completion gating and CLI effort; go vet and full
release build. Results are recorded in the PR before rollout.

### PR #63 review repairs

Inspected commit `781ab8502a033026bfab884ed142c7dbc45e8dd7` against GitHub's
base `de905508eeea086b46ad40c9c2b40b251e0af937`; no closing issue was linked.
One pass covered routing, settings, CLI effort, completion, handoff and their
direct callers. Two regression failures reproduced before repair:

- A small PR whose pre-review merge conflicts was routed to high-effort Opus
  because tip-to-tip diff included an unrelated billing change on main.
  Both classification diffs now use the merge base.
- A timed-out reviewer left child tool commands running, delaying handoff and
  allowing later edits in its shared worktree. Each runner now has a separate
  process group, killed on cancellation; pipe draining has a bounded wait.

The targeted regressions passed after repair, including a check that the child
cannot write after handoff. Tests use temporary Git repositories and fake CLIs;
no live tmux commands or workspace mutations were needed.
The timeout regression was also run against the original runner: its child
wrote `late.txt` and the fallback exited 7, confirming interference rather than
only a timing difference. Final checks: `go test ./...`, `go vet ./...` and
`git diff --check` all passed.

## 2026-10-12 — recover finished reviews and resumed PRs (0.3.92)

Four EduCenter PRs were held after completed reviews used an inline status/count.
The completion parser now accepts those explicit verdicts, while rejecting
incomplete verdicts, quoted examples and arbitrary mentions. Resumed PRs reuse
a trusted harness review of the exact current head; a changed head or a newer
incomplete verdict requires review. Required checks and conflict repair remain.
Reuse is logged separately and does not count as another provider review.

Supervisor resolution also removes status:blocked from resumed PRs. Previously
needs-review was restored but the mirrored blocked label kept selection idle.
Real external/runtime gates remain parked. No credentials or observations are
fabricated; issue backend completion stays separate from these partial PRs.

Validation: harness tests for inline verdicts, explicit incomplete/quoted input,
head-specific reuse, latest verdict, trusted authors and known harness wrappers;
Go vet and diff checks. Full embedded app build for 0.3.92 before rollout.

## 2026-10-12 - Scope-filtered triage, 0.3.92

The user's queue diagnosis was based on this checkout before it caught up
with origin. Fetched origin/main before implementing; 0.3.90 already added
locked partial-merge recovery and corrected the triage premise, and the
installed 0.3.91 retained those changes plus review routing. Live logs showed
the revised triage correcting #237's labels. Preserved all of that work.

Strengthened triage with a machine-generated shortlist and separate generic
ready/eligible-for-scope counts. It prioritizes unfinished review-state
contributions, excludes completed scopes, owned/held/deferred work and the
tracking/specification/release-review kinds the dispatcher cannot claim.
Blocked items remain available for prerequisite/time-gate verification.
The agent must refresh state, check active PRs/workers and merged scope
contracts, and name concrete blockers if it finds no work. An empty shortlist
skips the paid agent run. Session scope overrides are preserved.

Verification and installation:

- `go test ./internal/harness -run 'TestTriage|TestRequeue|TestQueueLock'
  -count=1` passed. The fake CLI regression exercises a real triage invocation
  with 95 ready issues already done for backend and one unfinished review
  issue. The agent receives zero eligible backend issues and exactly one
  continuation candidate. Tests also cover scope/ownership/deferral exclusions,
  blocked items, cooldown, empty-shortlist skipping and session overrides.
- The first parallel full suite hit the existing 500ms reviewer-start timing
  fixture before its fake CLI could create `started.txt`. No review code or
  test timeout was changed. `PATH="/tmp/remux-scope-triage-bin:$PATH"
  go test -p 1 ./... -count=1` passed, and final `go test ./internal/harness
  -count=1` passed after excluding non-dispatchable release-review candidates.
  The wrapper routes tmux tests to the isolated `remux-scope-triage-tests`
  server, with a clean zsh scratch session.
- Read current EduCenter issues through paginated REST. A temporary pure Go
  replay of the actual 210 issues returned 111 generic ready labels, zero
  eligible backend issues and 10 unfinished backend candidates, excluding the
  seven full-stack release reviews. No GitHub labels/comments were changed
  and no live agent was launched for this replay; the temporary test was removed.
- `go vet ./...`, `git diff --check` and
  `VERSION=0.3.92 ./scripts/build.sh` passed, including TypeScript/Vite build.
  The stripped binary measured 23,123,480 bytes.
- Saved the prior installed binary at `/tmp/remux-before-0.3.92`.
  `./bin/remux restart` updated `~/.local/bin/remux`. Its version is 0.3.92;
  launchd reports running PID 74764, never exited, and the service log reports
  0.3.92 with its tailnet URL ready. Existing loop tasks were not stopped.
- The scratch session was confirmed as `remux-test` on the isolated socket
  and removed. `laptop-invariant.sh before/after` passed with all 20 laptop
  panes unchanged. It noted an independent active-window move during the run.

The corrected scope premise and partial-merge recovery are already in running
0.3.91 loops. The new shortlist is in installed 0.3.92 and takes effect when
each loop is relaunched; restarting the service does not replace running
loop processes. Real external, resource and postponed-scope gates remain
blocked. No readiness was invented to make the loops appear busy.

## 2026-10-13 — GitHub REST fallback and shared recovery (0.3.94)

Primary GraphQL throttling now switches supported harness operations to REST.
Item/PR reads, pagination/filtering, bodies/comments, review metadata, diff, label
setup, comment reviews, reviewer requests and tested-head squash merges preserve
the existing contracts. Normal errors do not trigger alternate mutations.
Cooldowns defer exhausted budgets until reset/Retry-After; secondary throttling
pauses both interfaces. Draft-to-ready remains GraphQL-only and waits without
escalating or losing needs-review. Agent prompts include REST draft creation.

Large recovery scans were duplicated by each idle builder every minute. They
now share an atomic common-Git-directory timestamp per scope, at most once per
five minutes. Immediate merge continuations and 15-second new-work polling stay.

Harness regressions cover fallback selection/reset, REST response mapping,
pagination excluding PRs from issue work, review authors/commits, shared scan
claims, guarded merges, rate-wait queue retention and secondary/auth failures.
A temporary live REST-only read of EduCenter PR #1800 verified its labels, draft,
head, review history, full comments and diff (read only, no edits). Go tests/vet
and release build results are posted on the PR before rollout.

## 2026-10-13 — Active idle supervision and 30-second polling

The supervisor still prioritizes needs-human issues/PRs, then inspects builders
and reviewers idle or blocked for two minutes in the same repository. Healthy
empty queues cost no AI invocation. Investigations are spaced 15 minutes apart,
include worker scope/instructions, unfinished scope queues, open PRs, possible
stale claims and the preceding intervention, and verify outcomes before retrying.
The diagnosis covers label/partial-delivery drift, missing prerequisites, review
waits, stale ownership and build/runtime failures. External/time/credential gates
remain explicit. Stopped, working, triaging and usage-limited workers are excluded.

Final-message DELEGATE directives become one-shot preferred issue/PR assignments.
The harness checks live worker state, role, repository, pending preferences and
fresh item eligibility; workers revalidate before normal claim locking. Stale,
held, postponed, completed, owned and externally blocked preferences are discarded.
The supervisor can file a bounded missing prerequisite and enqueue eligible work;
it cannot alter worker instructions/models, merge PRs or kill/restart agents.
Quota failures do not execute directives; existing reset-time waiting remains.

Idle queue checks now use 30 seconds, including short successful runs. Preserved
the already-installed dependency GraphQL batching and redundant-label-write
optimizations from fix/loop-poll-minute rather than overwriting them.

Verified: go test ./... passed all packages; go vet ./... passed. Focused new
checks passed after the final pending-assignment guard: eligibility, quota/stop
exclusions, stale preferences, healthy-empty/cooldown behavior, and a stand-in
agent investigation that delegated through stand-in GitHub/tmux commands. These
checks did not start a real AI agent or change live GitHub labels/loop options.
Full UI + stripped build passed; final Go rebuild reused the matching 0.3.96 UI.
Binary size: 23,234,168 bytes. Manual localhost smoke at port 17496 returned
/api/health 200 with version 0.3.96 and served embedded index/assets; stopped that
owned smoke process afterward. Laptop invariant passed around tests, read-only
live roster inspection and the smoke phase (19 panes unchanged). No scratch
session remains from these phases.

Installed v0.3.96 atomically with the previous binary retained privately, then
restarted the launch agent; remux status confirmed the service loaded. Kevin
explicitly approved the live-loop exception: "Yes, restart loops after their
tasks finish." Queued graceful after-task stops, waited for each old session to
exit, then recreated all seven existing EduCenter build/review/supervise loops
with the installed binary. The separate chat supervisor was left untouched.
Read-only verification confirmed new PIDs and exact preservation of role, agent,
model, scope, instruction mode/text and original empty stop settings on all seven.
The two active triage runs finished before rollover. No live agent was interrupted.
The final laptop invariant passed (19 non-harness panes unchanged). Private
rollout snapshots/logs/verification are in
/Users/mac/.local/share/remux/rollout-0.3.96. Real idle-remediation decisions have
not yet been claimed as observed; the control flow was exercised with stand-ins.

## 2026-10-13 — Temporary builders/reviewers with verified usage headroom

Kevin authorized routine scope/capacity/model decisions without another approval,
up to five custom builders at once, temporary reviewers and cleanup after their
work. This coordination exception is recorded in AGENTS.md; active tasks and stop
requests remain protected. One idle existing Codex builder was moved to backend
with a backend-only brief and preferred task #1849; fresh GitHub/session state
confirmed it claimed the issue, rather than merely changing labels.

The supervisor can request one-task builders/reviewers through validated SPAWN
lines. Five custom builders and five custom reviewers are the per-repository caps.
Fresh eligibility, normal claims, pending assignments, role stop requests and
mentioned active code-file overlap are checked before starts. Custom loops never
fall back to unrelated queue work; they remove only their own sessions after
returning from the assigned run, including startup/limit failures. Draft PRs,
uncommitted/unpushed work and evidence survive. Temporary reviewers keep their
budget-selected provider; deep work receives high effort and the deep review
window. Ordinary existing workers retain their previous quota/reset behavior.

On-demand native `bunx tokscale@latest usage --json` supplies provider budgets.
Observed JSON exposed an active Codex account with 84% weekly left; Claude was
absent/unavailable and is not treated as 100% available. Selection ignores inactive
accounts and unsupported providers, checks session/weekly/model-specific windows
where available, and reserves shared headroom for concurrent custom workers
across repositories. Routine/deep floors are 25/40%, with 10/20-point task
allowances. These are conservative heuristics, not exact token predictions or a
completion guarantee. Known native limit failures hold that account until reset.
No automatic quota watcher, spending on credits or stopping existing work added.
A provider login/limit alone may be resolved by handing free work to an available
authorized provider; genuine business/runtime credential gates remain blocked.

The last supervisor result now persists while idle and is shown on the loop
card/detail; the idle polling text correctly says 30 seconds. Missing scope
capacity is investigated even when the existing workers are busy.

Verification: full go test ./... passed (harness 47.648s), go vet ./... passed,
and focused budget/cap/ownership/lifecycle-routing tests passed after final
refinements. Stand-ins exercised real spawn argv/metadata and the five-builder
cap, duplicate-task refusal, shared-budget constraints, inactive/missing/low quota
refusal, native reset holds and one-task queue isolation. Full TypeScript/Vite
build passed; final Go builds reuse the matching v0.3.97 UI. Stripped Intel binary
size: 23,283,360 bytes. Manual localhost port 17497 served health version 0.3.97,
embedded UI and the real loop response. Owned smoke server stopped afterward.

A real compiled CLI walkthrough with stand-in GitHub/tmux confirmed both builder
and reviewer closed-task exits removed their own sessions without launching AI;
a stand-in setup failure also cleaned up. Evidence is private under
/var/folders/yr/bvzjfk1n2szdcz11h57pc1tm0000gn/T/remux-custom-lifecycle-jx66wy7z.
These observations do not claim a full live task completed on the new spawner.
Laptop invariants passed around full tests (19 panes) and the final smoke phase
(20 panes); no non-harness layout changes or new database containers were made.

Deployment: v0.3.97 tag and published GitHub Release contain Intel/Apple Silicon
binaries and SHA256SUMS, with uploaded assets verified. Installed the clean release
Intel binary atomically and restarted the launch agent; remux status reports
0.3.97. The supervisor finished its existing investigation before graceful
rollover; fresh PID/argv and exact role/agent/model/scope/instruction/stop settings
were verified afterward. Existing builders/reviewers were left on their active
runs rather than interrupted.

To remove a real capacity gap immediately, a fresh native usage check found 77%
weekly Codex headroom. A one-task temporary backend worker for #1889 was started
after ready/ownership/code-overlap checks and was observed working on that issue
with custom=1. A visible diagnosis/delegation comment was accepted on GitHub.
This is a live start/claim observation, not a claim that the fix or cleanup after
that still-running task has completed. New supervisor spawn requests use the
same checked budgets and one-task lifecycle. Final laptop invariant passed with
20 non-harness panes unchanged. No additional user approval was requested for
this routine coordination, per Kevin's standing authorization.


## 2026-10-27 — bounded code-quality scope (isolated worktree)

Added a queue-independent `code-quality` builder with per-repository process
locking, durable category rotation and interrupted-cycle handoff/report storage
in the Git common directory. A fresh Sol coordinator runs from a dedicated clean
checkout refreshed to `origin/main`; source changes belong in separate fix
worktrees. Saved workflow prose requires fresh audit/implementation/review workers,
Codex CLI plus consistency reviews on the exact candidate, visible accepted PR
comments, current-base checks, authorized exact-head merge and task-only cleanup.
The scheduler does not independently verify the GitHub merge gates. Completion
receipts only control category advancement. Phone preset application selects one
Codex builder and no separate review/supervisor/cleanup loops; the API rejects
mixed scope plans and protects ordinary assignments/stops.

Verification uses stand-in git/tmux/bunx/codex executables for the new scheduler;
no paid agents or live quality loops were launched. Focused cases cover lock
contention/release, orphan coordinator lock retention after runner death,
restart/failed-run resume, dirty coordinator preservation, replace-mode isolation,
explicit Sol selection, durable evidence, rotation,
incomplete receipts, stop requests, missing instructions, prompt gates and invalid
role/queue combinations. Existing harness/API suites also passed; their detached
test sessions run stand-in commands. `scripts/laptop-invariant.sh before` and
`after` passed (22 outside panes unchanged; an existing service's allowed
`@remux_task` update was reported). `npm ci --ignore-scripts` populated only this
worktree's missing frontend dependencies; `npm run build` passed afterward. No
service was installed/restarted, configuration activated, or branch committed.

Additional checks: `go vet ./internal/harness ./internal/api`, `go build ./...`
and `git diff --check` passed. `go test ./internal/harness -run Quality -count=1`
passed with the orphan-lock subprocess regression case. No Codex/Claude CLI was
used in verification; their executable names were stand-in scripts.

The final repeated harness/API run, vet and Go build passed. Its invariant
comparison failed: unrelated Keystone panes `%21` and `%280` in `saas:1` changed
height (17→22 and 36→31); the active pane moved from `saas:1.2` to `saas:1.1`.
The attached client was unchanged (`/dev/ttys000 saas 195x55`). Read-only inspection
found no attach/select/resize/split command in the relevant tests; those panes
were nvim/Claude in `/Users/mac/dev/github/systems/keystone`, outside these tests'
owned detached loop sessions. No geometry was restored and no further tmux-write
verification was performed. The cause is not proven; this final comparison must
not be reported as passing. The earlier comparison passed with all 22 panes
unchanged.

The parent then ran a new focused Quality-suite/invariant window: all Quality
checks passed in 2.6 seconds and all 22 outside panes were unchanged. The earlier
full-window comparison remains a recorded failure with unproven cause; this new
focused comparison does not retroactively change it.

## 2026-10-27 — code-quality review regression fixes

Saved-loop replacement now removes only the requested repository lane (ordinary
or code-quality), matching live reconciliation. The crash-restore regression
starts ordinary → quality → ordinary, checks both saved counts, then restores
both lanes with their instructions and ordinary Claude model intact.

Every coordinator invocation clears runner-ok and archives any prior receipt as
previous-complete-*.json before launch. A zero exit records success only with a
valid clean/merged receipt and nonempty report. Invalid/partial JSON resumes the
same recorded cycle; interrupted evidence remains available. Stand-in retries
cover missing/invalid receipts, blank reports, and a failed invocation that writes
new valid evidence; none advances the category before a genuinely successful run.

StartLoop reconstructs lanes separately and prefers ordinary workers when both
exist, regardless of session order. Quality-only initialization selects its one
Codex coordinator. Scope input and presets share the same transition: entering
quality retains the ordinary draft; leaving restores its counts/models/roles.
Project selection reconstructs the selected repository instead of mixing lanes.
The update label also reflects only the selected lane. Three new pure-function
Node tests use the existing esbuild/Node setup, without another test framework.

Verification: npm test --prefix web passed (25 cases); npm run build in web
passed; go test ./internal/harness -run Quality -count=1 passed; the complete API
suite passed; go vet ./... and go build ./... passed; git diff --check passed.
The full go test ./... run passed every other package, including harness and tmux,
but used the first version of the new API assertion that incorrectly expected a
saved Codex model instead of its intentional blank value (Sol is pinned at run
time). Correcting that assertion passed the focused crash test and full API
suite. The final missing-receipt regression was covered by the focused Quality
run. No additional live verification was attempted after the invariant failure.

The first before/after invariant passed with 22 outside panes unchanged. The
later comparison failed: active saas:1.1 moved to saas:1.2; pane %21 changed width
195→97 and new pane %429 appeared at 97x22. Read-only inspection identified %21
as nvim and %429 as Codex in the unrelated Keystone checkout. No test invokes
split/select/attach/resize on those panes; the cause is not independently proven.
No geometry was restored. No paid agents, live quality loops, deployment, config
activation, commit or push was performed. Detached test sessions used stand-ins
and their existing deferred cleanup; no remux-test session was created.

## Start loop v2 implementation (2026-10-27)

Implemented in isolated `feat/start-loop-ui`, based on origin/main 66449b4. The
approved `docs/mockups/start-loop-v2.html` remains a visual reference; React uses
normal application state and components, not the prototype's simulated JS.

The flow is Project → Scope → Summary → Start. Built-in scope rows resolve saved
guidance; other saved scopes live under Custom and Manage scopes. Management edits
the existing Preset fields, confirms named deletion and retains failed edits.
Code quality is one fixed Sol coordinator, enabled by the selected project's real
scope-role file. Its two internal reviews are not extra Remux loop counts. Ordinary
teams retain mixed providers/models, with advanced options collapsed and reversible
simple provider changes. Scope/project drafts stay local until Apply. No stop-all
controls were added because the current API rejects empty target teams.

Project polling remains filesystem-free. Text-only worktree suffix normalization
preserves independent clone paths. A separate form-open choices request filters
only missing historical paths without changing config; permission errors retain
choices. Selected-project metadata resolves exact role files, generic role fallback,
project defaults and canonical paths (including home expansion). Both explicit file
lookups have a three-second response deadline and share at most two outstanding
readers. A kernel-stalled macOS read cannot be forcibly cancelled by Go and can
occupy its slot until the OS releases it; further requests fail promptly. The UI
aborts stale requests, distinguishes failures from absent workflow, and never lets
late metadata replace the current project's draft. No workflow text is re-inlined
by this preview into tmux; the committed quality pointer remains the source.

The summary follows deterministic backend session names and separate lane rules,
including immediate idle/blocked stops and after-task working/triaging stops,
clone-name collisions, retained-loop updates and preserved assignments. Active
provider replacement is now rejected before reconciliation writes. Negative review
counts are rejected as well. Failed start/save requests propagate to the form.
The small header update affordance reuses the existing build version/health
comparison and pullNewBuild; only explicit reload stores the current form in tab
session storage with a five-minute expiry. Restoration is read-only during
render, so React StrictMode can initialize twice without losing the draft; a
mount effect consumes it after commit. Pending mutations disable both the
header reload affordance and its dialog action; applying changes is disabled
while a reload is in progress. No automatic reload or new updater was added.

Verification performed:

- `npm ci --prefix web` installed the pinned worktree dependencies. npm reported
  two existing high-severity dependency advisories; manifests/lockfile were not
  changed in this task.
- `npm test --prefix web`: 33 Node tests passed, including repeated reload-draft
  initialization, consumption and expiry, plus reconstruction of mixed
  lanes, lowered counts, exact stop timing, historical quality preservation,
  queued-stop wording, clone collisions and provider replacement previews.
- `npm run build --prefix web`: TypeScript and production Vite build passed.
- `go test ./...`: all packages passed, using existing scratch sessions/stand-ins,
  never a real Codex/Claude task. The full run preceded the final on-demand choices
  filtering addition; the final metadata/choices changes were covered separately
  by `go test ./internal/api -run 'Test(ProjectPath|LoopProject)' -count=1`.
- `go vet ./...`, `go build ./...` and `git diff --check`: passed.
- Parent's read-only 390px browser pass observed the project/scope/summary flow
  without horizontal overflow. It caught and prompted fixes for body padding,
  duplicate scope buttons, missing historical paths and unavailable quality
  selection. Parent's final browser verification observed explicit Check and
  reload preserving the selected Remux project, frontend scope and typed
  instructions, with sessionStorage consumed after mount. The 390px and 1440px
  views had no horizontal overflow. Using the parent's read-only preview,
  failed Start retained the draft and displayed its error; failed Manage scopes
  Save retained the edited name and modal with the 503 error. No live writes
  were involved in those error-path checks.

Workspace invariant: `./scripts/laptop-invariant.sh before` recorded 23 panes
outside remux-test. The comparison after the full suite FAILED: active
`saas:1.3 → saas:1.1`; `%21 107x12 → 107x27`, `%280 195x41 → 195x26`,
`%429 87x12 → 87x27`. Kevin is actively using Keystone. No command in this work
attaches, selects or resizes panes, and the exact cause of concurrent geometry
changes is not independently proven. No geometry was restored. The parent was
informed immediately. No more tmux verification ran after that comparison.
No live loop/preset/service edits, commit, push, release/tag or deployment were
performed by this implementation worker. Parent owns release v0.4.2 and final
review/publication. Temporary `.start-preview/` belongs to the parent and is not
part of this change.


### Start-loop independent review follow-up (2026-10-27)

Fixed two confirmed P2 findings from review of `0392365`:

- Saved guidance selection now identifies presets by unique saved name, rather
  than shared scope. Accessibility and Performance can both use `custom` and
  retain their own instructions/mode and edited drafts. The Custom dropdown also
  includes named presets using built-in scopes, without adding duplicate chips.
  Direct built-in choices retain their existing default selection behavior.
  Project drafts and explicit reload drafts retain the selected name; scope and
  previous-team draft identities distinguish named presets from direct choices.
- Project discovery collapses nested pane working directories under observed or
  configured roots of the same GitHub slug. Separate clone roots, path-prefix
  siblings and different slugs remain distinct. This remains filesystem-free
  during background polling.

Verification: `npm test --prefix web` passed all 35 tests, including distinct
same-scope named guidance, edited drafts, project separation and direct built-in
selection. `npm run build --prefix web` passed TypeScript and production Vite.
`go test ./internal/api -run 'Test(PaneProjectRoots|ProjectPath|LoopProject)' -count=1`,
`go vet ./...`, `go build ./...` and `git diff --check` passed. API coverage includes
nested pane paths, configured roots, clone preservation and path boundaries.
No tmux commands, live writes, commits, pushes or preview helpers were used.

Parent's isolated browser fixture also verified Accessibility (`custom`/add)
and Performance (`custom`/replace) select distinct instructions. A named Frontend
variation (`frontend`/replace) remained reachable under Custom with the correct
mode, and its edited draft survived switching to Performance and back. The
parent used a temporary stand-in blocking starts, with no live backend writes.

### Start loop dropdown and spacing polish — 2026-10-27

Removed the Start loop version button and update dialog, its session-storage reload
restoration, and the now-unused server-version props from App through LoopsRoutes.
Settings retains its existing updater. Removed the obsolete reload-draft regression
case; ordinary per-project and per-scope drafts remain in memory.

Added one dependency-free custom Select shared by Start loop and ModelSelect,
including saved-scope editing in the modal dialog. Controls have explicit labels,
selected-value indicators, arrow/Home/End navigation, typeahead, Enter/Space
selection, Escape/Tab dismissal and outside-click dismissal. Menus use the browser
popover top layer where available, with fixed positioning as fallback, viewport
bounds, upward placement near the bottom, and scrolling for long lists. External
scroll/viewport changes dismiss the menu rather than leave it detached.

Kept the existing Remux palette. Removed the negative Manage scopes margin and
separated the heading action from the scope border; added space around custom
scope/provider controls and advanced/dialog fields. Shared model labels now sit
above their controls instead of nesting the control inside the label.

Checks actually run in the isolated worktree:
- `npm ci --prefix web` completed; npm reported two existing high-severity audit
  advisories. No dependency versions were changed.
- `npm test --prefix web`: all 34 retained Node tests passed. The first run exposed
  an accidentally removed adjacent scope-helper import while deleting the obsolete
  reload test; restoring that import resolved the two failures.
- `npm run build --prefix web`: TypeScript and production Vite build passed.
- `git diff --check`: passed.

Parent browser checks against a read-only preview observed the project menu,
12px separation below Manage scopes, keyboard ArrowDown/Enter project selection,
Home/ArrowDown/Enter supervisor-model selection, viewport-bounded modal dropdowns,
and Escape dismissing the dropdown while retaining the modal. The pass found
oversized helper copy and an unthemed dialog primary action; these were corrected
with scoped helper text and reuse of existing primary/cancel/disabled/danger
button styles. Final screenshots at 320px, 390px and 1440px show the full page,
summary, advanced controls and dialogs with no horizontal overflow or browser
errors. A temporary browser fetch fixture supplied 26 project choices: the custom
menu stayed 280px high, scrolled to its final item with End, and wrapped long clone
paths inside its width. Failed scope saves retained the draft and selected mode.
Screenshots are saved locally under
`/Users/mac/.agent/diagrams/remux-start-loop-polish/` (not published to GitHub).
No live loop or scope mutations were submitted; the preview rejected writes.

The parent preview used only GET/HEAD API calls and read-only tmux observation,
with no namer/watchers. Its broader invariant comparison FAILED after concurrent
Keystone geometry changes: active saas:1.3→saas:1.1; pane %21 147x12→147x37,
%280 195x41→195x16 and %429 47x12→47x37. Read-only inspection identified these
as nvim, Claude and Codex panes in Keystone. No attach/select/resize or other pane
writes were executed here, and the exact cause is not independently proven. No
geometry was restored. The temporary preview helper was stopped and removed.


## 2026-10-29 — approved Settings v2 implementation

Ported `docs/mockups/settings-v2.html` into the existing React Settings screen:
compact connection/details including denied access, grouped Reading, Notifications,
Input & keypad, Agent naming and App & updates, one column on phones and two on
wide screens. Reused the existing palette, cards, switches and route editors; no
new dependencies or native dropdowns. Device versus Mac scope is explicit.

Font controls preview the terminal sample and enforce 10–20px; scrollback enforces
100–2000 lines. Permission messaging covers granted/default/denied/unsupported;
Mac event choices remain editable independently of browser permission. Ignored
checks are collapsed, preserve failed-add drafts, and offer retry. Pending Mac
writes disable other Mac controls and the App write guard prevents overlapping
full-payload requests. Values apply locally only after server success. Backend
settings now persist before updating runtime values under the server mutex, so a
failed disk write also leaves watcher/namer behavior unchanged.

Update checks only refresh health. The shown version is the loaded bundle; a
separate explicit reload uses the existing service-worker update path when the
Mac serves a different version. No installer/server upgrade is implied. Mockup
preview state controls are absent from production.

Observed checks in the isolated implementation worktree:
- `npm ci --prefix web` completed (existing audit report: two high vulnerabilities;
  dependency versions were not changed).
- `npm test --prefix web`: all 34 existing pure frontend tests passed.
- `npm run build --prefix web`: TypeScript and Vite production build passed.
- `go test ./internal/api -run '^TestSettings' -count=1`: passed, including a new
  persistence-failure regression using a temporary HOME and no tmux commands.
- `go vet ./...` and `go build ./...`: passed.
- No tmux commands, paid model calls, live setting changes or service reloads were
  run by the implementation worker. Browser verification/review/release is handled
  separately; this entry does not claim that those steps are complete.

Settings follow-up: the parent’s read-only browser pass observed a successful
Check followed by a window offline event: the heading correctly changed to
“Your Mac is offline” while the inline message still claimed “Remux is reachable.”
The inline success now says “Remux was reachable at the last check.” This records
the historical health request without contradicting the current connection;
check failures, update state and loaded-bundle version handling are unchanged.
The Send with Enter hint now says “When off, use the send key to submit.” Source
inspection of `web/src/pane/Composer.tsx` confirmed Enter does not stage text when
that preference is off; it follows the textarea’s normal newline behavior.

Follow-up checks actually run: `npm test --prefix web` passed all 34 tests;
`npm run build --prefix web` passed TypeScript and Vite; `git diff --check` passed.
The follow-up worker used no tmux commands, live writes or preview-helper changes.

Parent verification before review: real Chromium screenshots at 320px, 390px and
1440px match the approved single/two-column layout, including the bottom update
card and intermediate notification/input/naming sections. No horizontal overflow
or browser errors. Removed a duplicate divider before ignored checks. Verified
font-size 10/20px and scrollback 100/2000 limits and the live terminal sample;
keypad/naming links reach their existing routes. Connection-check failure is
shown inline; a synthetic different server version offers explicit reload and
retains the separately displayed loaded bundle version. The historical-success
copy remains accurate after an offline transition. Device controls remain usable
while offline.

A parent-only local preview intercepted settings GET/PUT in memory: default PUT
failed with 503, then successful retry was enabled for verification. Pending saves
disabled all Mac switches; failures retained the previous naming value and ignored
check draft, and successful retries applied the intended value and cleared the
matching draft. No live settings, subscriptions or loops were changed. The preview
helper was stopped/removed. `laptop-invariant.sh after` PASSED: all 23 laptop panes
unchanged during the entire preview phase. Screenshots are local under
`/Users/mac/.agent/diagrams/remux-settings-implementation/`.

Settings review fix: a failed save previously retained the original patch callback,
which built a full Mac settings payload from an old snapshot. If delayed bootstrap
settings arrived before Retry, retrying one field could overwrite unrelated refreshed
preferences. Settings now reads the latest patch callback through a ref while keeping
the captured intended field change, draft completion callback and pending guards.
The granted-permission label now says “Permission allowed on this device” and explains
that permission alone does not confirm push registration or delivery. Existing missing
registration retry remains a limitation; this change adds no subscription behavior.

Fix-worker checks: `npm test --prefix web` passed all 34 existing frontend tests;
`npm run build --prefix web` passed TypeScript and Vite; `git diff --check` passed.
No focused automated regression was added because the existing tests cover pure
modules and there is no React/browser test harness; delayed-bootstrap browser
verification remains with the parent reviewer. No tmux commands, live setting writes,
Go changes, preview-helper changes, commits or pushes were performed by this worker.

Parent reproduced the review race in a separate standalone HTTP fixture (no tmux
or real settings): initial local `notifyDone/namePanes=false`; delayed bootstrap
GET refreshed both to true; the first PUT failed; Retry then sent the latest true
values while preserving the intended `notifyWaiting=false` change. Both request
bodies were captured and asserted. This confirms Retry does not replay unrelated
stale flags. The granted permission hint uses plain scope language, without a
claim that permission alone proves delivery. Fixture and browser were stopped.

Ignored-check retry follow-up: Add and Remove now retain the intended check name
and compute their list change from the latest settings on every save attempt.
A delayed bootstrap list arriving after a failed write therefore remains intact
apart from the requested Add or Remove. Add deduplicates and Remove matches names
without case sensitivity, consistent with the server's normalized set. Direct
switch changes still retain their captured target value on Retry; successful Add
alone clears the matching draft. The App write guard and backend are unchanged.

Follow-up worker verification: `npm test --prefix web` passed all 34 existing
tests; `npm run build --prefix web` passed TypeScript and Vite; `git diff --check`
passed. No new harness or dependencies were added; the existing pure-module tests
do not exercise Settings rendering, and the parent will verify both delayed-list
Add and Remove races in its browser fixture. This worker used no tmux commands,
live writes, preview-fixture edits, commits or pushes.

Parent browser verification of the list follow-up passed both delayed-bootstrap
races with captured requests. Add first failed with `[Vercel,new]`, then after
bootstrap Retry sent `[required-check,new]`; Remove first failed with `[Vercel]`,
then after bootstrap Retry sent `[required-check]`. Both retries retained refreshed
`notifyDone/namePanes=true` values. Add cleared only the submitted draft. The
fixture returns 404 for unsupported routes, matching absence rather than sending
invalid successful GitHub snapshots. No real settings or tmux were involved.

Settings switch wrappers are now associated labels, extending the clickable area
to 44px while retaining the existing pill appearance. A browser check confirmed
label/control association and that clicking the outer label toggles the switch.

## 2026-10-29 — Quality-loop budget reserve

The quality coordinator reused the ordinary Codex worker's hard-coded30% gate,
so the loop remained blocked at27% despite Kevin's20% instruction. Quality now
uses a20% gate while ordinary worker policy remains30%. A budget-blocked quality
loop saves an after-task stop instead of polling indefinitely. Account identity,
missing quota data and native-limit holds still fail closed.

Verification: full internal/harness Go tests passed (52.7s), focused quality/spawn
tests passed, go vet internal/harness and stripped Go build passed. Tests cover
20% rejection,21%/27% admission, unchanged ordinary30% policy, native holds and
scheduler stopping without a paid launch at20%. No UI/server restart required
for the initial loop-only binary deployment; only the blocked loop is restarted.

## 2026-10-29 — Choose the quality-loop provider

The Start loop quality scope now exposes the shared Loop agent selector and
Claude model selector. Requests preserve the selected provider/model; the API and
coordinator accept Claude rather than rejecting it or appending the Sol model.
The existing deterministic quality lane name and evidence directory are retained
across provider switches, and a working coordinator cannot be replaced. Selected
Claude coordinates Claude phase workers; the independent Codex CLI review stays
required and is disclosed in the screen.

Verification: full internal/harness and internal/api tests passed, go vet passed,
36 web tests passed and TypeScript/Vite production build passed. Stand-in tests
exercise Claude/sonnet coordinator completion and review instructions; API tests
exercise idle provider replacement and refusal during active work. Manual local
browser selected Claude/Sonnet and captured the submitted agents/models/scope
without sending the request or changing the running loop. Shared controls were
also checked at390px. No paid Claude task or mutation to the live loop was used.
Laptop invariant passed with23 existing panes unchanged. Local read-only API
preview omits watchers; preview frontend/API/browser are stopped after checking.

## 2026-10-29 — on-demand Model usage

Added a separate Settings → Model usage screen (`#/usage`) with the existing
palette/cards, remaining percentages, reset timestamps in the device timezone,
last successful reading time and manual Refresh. No interval, spend history,
Copilot or extra accounts were added. Configured account emails live only in the
local `model-usage.json` file, avoiding main-config updates by older loop binaries.
The API filters native JSON to configured Claude/Codex emails, validates metrics,
keeps failures per provider, binds cached snapshots to configured accounts and
the verified Claude credential, and marks retained snapshots stale on failed refresh.
Collection uses its own synchronization, not the workspace mutex, and has bounded
process/pipe waits. Claude credential selection mirrors the native file/Keychain
precedence; a read-only OAuth profile lookup verifies its email, and credential
fingerprints must match before and after native collection. No token or fingerprint
is exposed or persisted by Remux. No harness spawn gates were changed.

Observed checks: `npm ci --prefix web`, `npm test --prefix web` (34 passing),
`npm run build --prefix web`, `go test ./internal/api -run '^TestUsage' -count=1`,
`go vet ./...` and `go build ./...` passed. Added parser/privacy/partial failure
coverage and a fake CLI concurrency/cache/stale snapshot check. npm reported two
high advisories in the existing dependency tree; dependencies were not changed.
No tmux commands, real workspace writes, CLI account switches or paid model calls
were used during implementation. Phone screenshots and installed-service
verification are recorded separately after browser review and release.

Follow-up checks also passed: focused usage tests under `-race`, including native
credential precedence, fake OAuth profile transport, duplicate Codex identity
selection, singleflight/cache/stale behavior and bounded reader cancellation.
The screen uses the shared typed API helper. File reads have a one-second caller
bound and four shared reader slots, so stalled kernel reads cannot hold the
usage cache forever or accumulate unbounded goroutines. Ambiguous duplicate
Codex email rows are unavailable unless exactly one matching row is active.

Parent verification: actual credential-bound Claude profile matched the requested
account, and a read-only preview API returned exactly the configured Claude/Codex
rows with current native metrics and no credential/account-ID or other-provider
fields. The local account config was created outside the repository with mode
0600. No account login or CLI active-account selection was changed.

Screenshots at 320px/390px and 1440px showed one-column phone cards and two-column
desktop cards without horizontal overflow. Settings navigation and Back worked;
reset/checked timestamps rendered in the device timezone. A simulated HTTP refresh
failure retained both previous readings with stale labels; a real Refresh recovered.
A timed idle check observed zero background usage requests. No browser errors.
The preview helper was stopped/removed, and the full preview laptop invariant
PASSED with all 23 panes unchanged. Local screenshots are under
`/Users/mac/.agent/diagrams/remux-model-usage/`.

The screen's native reader is pinned to tokscale 4.20.1 because credential binding
mirrors that version's selection rules; updating them together prevents a future
reader change from silently assigning another Claude token's usage to this account.
Existing harness native-budget commands/gates are untouched. Final focused race
checks and production build passed after the source/identity refinements.

## 2026-10-29 — Keep the Codex reserve specific to Codex loops

A Claude coordinator stopped a saved fix before its reviews because historical
handoff instructions treated the20% Codex reserve as a cross-provider rule.
The dispatcher now explicitly scopes that reserve to Codex-led loops. A Claude
loop and its phase workers may continue while the required Codex CLI review is
actually available, even at20% remaining. Real provider limits/holds still block
work, and no failed or exhausted review counts as approval. Claude admission
already filters to the selected provider; its native-unavailable message no
longer labels that failure as the20% Codex reserve.

Focused quality/spawn Go tests and go vet passed. The stand-in coordinator runs
Claude/sonnet with Codex at20%, retains the independent review gate, and receives
the explicit provider policy. A native Claude spend-control stop still blocks a
second launch even with Codex at100%, with a provider-specific error. No paid
worker or real tmux write is used by these tests. The live Claude/Opus loop was
resumed from its clean saved PR2679/head66ac83ab; saved instructions and the private
handoff record Kevin's corrected policy. No application source or other loop was
changed by that recovery.
