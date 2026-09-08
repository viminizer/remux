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
