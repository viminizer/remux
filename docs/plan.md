# remux - supervise your Mac's tmux + AI agents from your phone

## Context

Most of Kevin's work runs inside tmux on his Mac: projects split into sessions and windows, with
Codex and Claude Code agents living in individual panes. Right now supervising that work means
sitting at the laptop. Android terminal apps make it worse - tiny text, keyboard chords, pane
navigation.

Verified against the real setup on this machine:

- 3 sessions (`1yegabiz`, `saas`, `source-open`), 17 windows, 26 panes.
- Panes running `codex` and Claude Code (which reports `pane_current_command` as its version,
  e.g. `2.1.263`, and puts the live task name in `pane_title`, e.g. `✳ Separate worktree`).
- `tmux capture-pane -p -e` returns the fully rendered screen with truecolor SGR, 256-color,
  OSC 8 hyperlinks and box-drawing characters. A 300-line capture takes **11 ms**.

The goal: read progress, answer an agent's question, send the next instruction, and move between
projects - from a phone, without disturbing the laptop.

### "Server stack" - what that meant

Two programs are involved:

1. **The service on the Mac** ("server") - talks to tmux, exposes an HTTP + WebSocket API.
2. **The phone UI** ("client") - a web app you add to the Android home screen.

Kevin's answer: if it's an installable file, use Go. **Decision: Go.** The web UI is compiled into
the binary with `go:embed`, so the whole product ships as **one file** with no runtime dependency -
copy it anywhere, run it, done. Go 1.24 is already installed.

Kevin then asked whether Tailscale could be bundled in too. It can - `tsnet` embeds a full Tailscale
node as a Go library. That is now the design, and it removes the token auth, the QR pairing, the
`tailscale serve` step, and the Tailscale install on the Mac. See Architecture below.

---

## Architecture

Tailscale is **embedded in the binary** via `tsnet` (`tailscale.com/tsnet`, v1.102.3 - verified).
The process becomes its own node on the tailnet using a userspace gVisor TCP/IP stack. No
`tailscaled`, no root, no system extension, no Tailscale app on the Mac, no `tailscale serve`.

```
Phone (Chrome PWA)          Your Mac
  Tailscale app        ┌──────────────────────────────────┐
       │               │  remux  (one Go binary)          │
       └──WireGuard────┼─▶ tsnet node "remux"             │
   https://remux       │     ListenTLS(":443")            │
   .<tailnet>.ts.net   │     ├─ embedded React UI         │
                       │     └─ HTTP+WS API ──exec──▶ tmux server
                       └──────────────────────────────────┘
```

Key consequences:

- **Nothing listens on a real network interface.** `srv.ListenTLS("tcp", ":443")` announces only on
  the tailnet. There is no LAN port, no firewall rule, no port forward. Unreachable from anywhere
  except your tailnet - by construction, not by configuration.
- **Real HTTPS, free.** Tailscale issues and renews the cert for `remux.<tailnet>.ts.net`.
  That gives a secure context, which is what makes the Android home-screen install and the service
  worker work. This was the one setup step most likely to trip Kevin up; it now disappears.
- **Auth comes from Tailscale, not a token.** `srv.LocalClient().WhoIs(ctx, r.RemoteAddr)` returns
  the caller's Tailscale identity. Middleware allows only Kevin's login and rejects everything else
  with 403. No token to paste, no QR pairing, nothing to leak from localStorage.
- **The Mac itself stays off the tailnet.** Only this one service joins, as its own device. Least
  privilege: a compromised tailnet doesn't hand over SSH to the Mac.
- `Server.Dir` holds node state (`~/.config/remux/tsnet/`). First run prints a login URL to
  stdout; after that the node is enrolled and starts silently. `TS_AUTHKEY` can automate it.
- One WebSocket per phone client, carrying both the workspace tree and the focused pane's screen.

**`--local` flag** for development: skips tsnet entirely and serves plain HTTP on `127.0.0.1:7399`,
so the UI can be iterated on at the laptop without touching the tailnet.

**Funnel is deliberately not used.** `ListenFunnel` would put this on the public internet. It is a
remote-control plane for a machine running coding agents with filesystem access - it stays on the
tailnet.

### Cost of embedding

`tsnet` links wireguard-go, gVisor netstack, DNS and the control-plane client. Expect the binary to
grow from ~10 MB to roughly **40-60 MB**, and RSS by ~30 MB. For a personal tool on a Mac this is a
fair trade for "one file, no dependencies, real TLS, identity-based auth". Measured at build time
and recorded in the README.

### The hard constraint: never disturb the laptop

This drives the whole tmux layer. **The service never becomes a tmux client.**

| Never used | Why |
|---|---|
| `attach-session`, `tmux -CC` control mode | Attaching adds a client whose terminal size renegotiates pane sizes. Would resize the laptop's layout. |
| `select-window`, `select-pane`, `switch-client` | Would move the laptop's cursor while Kevin is elsewhere. |

Everything is done with **targeted, clientless commands**: `capture-pane -t %14`, `send-keys -t %14`,
`list-panes -a`. New windows/sessions are created with `-d` so the laptop's active window never
jumps. The one exception is an explicit, clearly-labelled **"Focus on laptop"** button in the pane
menu, which does `select-window` + `select-pane` on purpose.

---

## Server: Go

Dependencies: `tailscale.com` (for `tsnet`) and `github.com/coder/websocket`. Routing is stdlib
`http.ServeMux` (Go 1.22 method patterns). Everything else is stdlib. The QR-code dependency is
gone - with tsnet there is no token to pair, so the phone just opens a stable URL.

### `internal/tmux` - the tmux layer

**One call for the whole tree.** `list-panes -a` can emit session, window and pane fields together,
so the entire hierarchy comes from a single exec (~10 ms) and is grouped in Go:

```
tmux list-panes -a -F '#{session_id}\t#{session_name}\t#{session_attached}\t
                       #{window_id}\t#{window_index}\t#{window_name}\t#{window_active}\t
                       #{pane_id}\t#{pane_index}\t#{pane_title}\t#{pane_current_command}\t
                       #{pane_current_path}\t#{pane_active}\t#{pane_width}\t#{pane_height}\t
                       #{pane_in_mode}\t#{alternate_on}\t#{pane_dead}\t#{history_size}'
```

Empty sessions are backfilled from `list-sessions`. "no server running" → empty tree, not an error.

**Previews** for the browse screens: last 40 lines of each pane, fetched concurrently (worker pool
of 8), cached 1.5 s. 26 panes × 11 ms serialised is ~300 ms; parallel it is ~40 ms.

**ID validation** before any exec: session `^\$\d+$`, window `^@\d+$`, pane `^%\d+$`. `os/exec`
already avoids a shell, but IDs are validated anyway so a bad ID can never become a flag.

**Sending text** (`actions.go`) - the part that matters most for agents. Multi-line prompts are
wrapped in bracketed paste so Codex and Claude Code insert a newline instead of submitting early:

```
tmux send-keys -t %14 -H 1b 5b 32 30 30 7e     # ESC [ 200 ~
tmux send-keys -t %14 -l -- "<the prompt>"
tmux send-keys -t %14 -H 1b 5b 32 30 31 7e     # ESC [ 201 ~
tmux send-keys -t %14 Enter                     # only if submit=true
```

Single-line text skips the bracketing and just uses `-l`.

**Keys** come from a fixed allowlist, never passed through raw: `Enter Escape Tab BTab Up Down Left
Right Home End PageUp PageDown BSpace Space C-c C-d C-u C-r C-l C-a C-e M-Enter` plus digits `1-9`
and `y`/`n` for menu answers.

### `internal/agent/status.go` - agent state badges

Classifies each pane from `pane_current_command` + `pane_title` + the last ~30 ANSI-stripped lines:

| State | Signal |
|---|---|
| `waiting` (needs you) | `Do you want`, `❯ 1.`, `[y/N]`, `(y/n)`, `Allow`, `Approve`, `Yes, and` |
| `busy` | `esc to interrupt`, `Working`, `Thinking`, `(12s ·`, spinner glyphs |
| `idle` | empty composer: `Ask Codex to do anything`, Claude Code's `│ >` box |
| `shell` | current command is `zsh`/`bash`/`fish` |
| `unknown` | nothing matched |

All patterns live in this one file so they are trivial to re-tune when an agent changes its UI.
`waiting` sorts to the top of every list and gets an amber dot - that is the "an agent needs an
answer" signal, and it is also what triggers a push notification.

### `internal/push` - notify when an agent blocks on you

**Decision: v1 includes Web Push.** Without it Kevin still has to open the app to discover an agent
is stuck, which defeats the point. tsnet's real HTTPS is what makes this possible - Web Push
requires a service worker, which requires a secure context.

- **Watcher.** A background loop captures the last ~15 lines of every pane every 5 s (26 panes in
  parallel ≈ 40 ms of work, well under 1 % of a core) and runs the same status classifier. It only
  runs while at least one push subscription is registered, so the idle-cost story holds when Kevin
  isn't using it.
- **Trigger.** Only on a *transition* into `waiting` - never on a pane that is already waiting.
  Per-pane cooldown of 5 min. Suppressed if the phone currently has that pane open (the client tells
  the server which pane is focused). A second, default-off toggle notifies on `busy → idle` so Kevin
  can be told when a long task finishes.
- **Payload carries no pane content.** Push traffic passes through Google's FCM servers, so the
  message contains only session name, window name and pane id - e.g. `saas / woment-react - codex
  needs an answer`. The app fetches the actual screen after Kevin taps it.
- **Tap target.** The service worker's `notificationclick` handler opens `/#/p/%14` directly, so one
  tap goes from lock screen to the pane that needs him.
- **Keys.** A VAPID keypair is generated on first run into `~/.config/remux/vapid.json`.
  Subscriptions are stored alongside it so they survive restarts. Library:
  `github.com/SherClockHolmes/webpush-go`.
- **Caveat to state in the README:** this is the one part that leaves the tailnet. The phone needs
  ordinary internet (not just Tailscale) to receive pushes, and the Mac needs outbound access to
  FCM. Everything else in the app is tailnet-only.

### `internal/api/auth.go` - identity, not tokens

```go
who, err := lc.WhoIs(r.Context(), r.RemoteAddr)   // lc = srv.LocalClient()
if err != nil || who.UserProfile.LoginName != cfg.AllowLogin {
    http.Error(w, "forbidden", http.StatusForbidden)
    return
}
```

`AllowLogin` defaults to the login that enrolled the node, so it self-configures on first run.
`WhoIs` is also applied to the WebSocket upgrade, so a socket cannot outlive the check. Under
`--local` the middleware is skipped (the listener is loopback-only).

Every mutating request is additionally logged with the caller's login and the tmux target, to
`~/.config/remux/audit.log` - a plain record of what was sent to which pane.

### HTTP API

```
GET    /api/health                          server, hostname, tmux version
GET    /api/tree?preview=1                  full hierarchy + previews + agent status
GET    /api/panes/{id}/capture?lines=N      one-shot screen read
POST   /api/panes/{id}/text                 {text, submit}    - bracketed paste
POST   /api/panes/{id}/keys                 {keys:["Escape","Enter"]}
POST   /api/panes/{id}/interrupt            C-c
POST   /api/panes/{id}/focus                explicit select-window + select-pane
DELETE /api/panes/{id}                      kill-pane
POST   /api/sessions                        {name, path}   new-session -d
POST   /api/windows                         {sessionId, name, path}   new-window -d
PATCH  /api/sessions/{id} | /api/windows/{id}   {name}   rename
DELETE /api/sessions/{id} | /api/windows/{id}
GET    /ws
```

No `token` query parameter anywhere - identity comes from the tailnet connection itself.

### WebSocket protocol

```
client → {t:"sub",   pane:"%14", lines:400}
client → {t:"unsub"} | {t:"resume"} | {t:"ping"}

server → {t:"snap", pane, rev, lines:[...], meta:{cmd,title,w,h,inMode,alt,status}}
server → {t:"tree", rev, sessions:[...]}
server → {t:"gone", pane}
```

Server-side poller, per connection:

- Subscribed pane every **400 ms**: `capture-pane -p -e -S -<lines>`, SHA-256 the result, push only
  when the hash changes. At 11 ms per capture this is under 3 % of one core.
- Tree every **2 s**, same hash-and-diff, so the browse screens stay live without polling from the
  phone.
- Client sends `unsub` on `visibilitychange` → polling stops entirely when the phone screen is off.

---

## Phone UI: React + Vite + TypeScript

Plain CSS (mobile-first, dark), no component library, no router library.

> Clickable mock: `docs/ui-mock.html` - real layout, fake data, no server.

**Why not React Native.** Considered and deferred. The design depends on the UI being served *by*
the Go binary: that is what removes the connection screen, the pairing step and the second install.
A React Native app is installed separately, so it must be told the server address - which resurrects
exactly the setup tsnet deleted - and it splits one artefact into a binary plus a separately
versioned APK, breaking the single-file property that picked Go in the first place. React Native
only wins on true background execution, notifications without a push service, and hardware keyboards;
none of those are v1 problems. The Go API is identical either way, so if Android keyboard or gesture
behaviour turns out to be painful in daily use, the UI can be ported later with nothing wasted.

**Shape: a chat app, where each pane is a conversation.** Drawer of "conversations" on the left, one
focused pane filling the screen, composer pinned at the bottom - the ChatGPT / Claude layout, because
that is the interaction Kevin already has muscle memory for and it matches the real task (read the
last thing, answer it, move on).

The old three-screen drill-down (sessions → windows → panes) is **gone**. It collapses into the
drawer, so opening any pane is one tap from anywhere.

Routing is hash-based and shallow: `#/p/%14` is the only real route, plus `#/settings`. The Android
back button closes the drawer, then any open sheet, then leaves the app.

### The drawer - a flat pane list grouped by session

```
┌───────────────────────────┐
│  ⌕ Search           ✚ New │
├───────────────────────────┤
│  ● NEEDS YOU              │
│   woment-react            │
│   codex · pick an option  │
├───────────────────────────┤
│  SAAS                     │
│   ✳ Separate worktree     │
│   win 8 · claude · working│
│                           │
│   remux                   │
│   win 8 · codex · idle    │
│                           │
│   notes                   │
│   win 6 · zsh             │
├───────────────────────────┤
│  1YEGABIZ                 │
│   wedding-api             │
│   win 1 · codex · working │
└───────────────────────────┘
```

- **One row per pane.** Windows are not a navigation level any more - the window name becomes the
  row's subtitle. Three levels of tmux flatten into two of hierarchy: session header, pane row.
- **Row title** prefers `pane_title` when it is meaningful (Claude Code writes the live task there,
  e.g. `✳ Separate worktree`), falling back to the window name. This is what makes the list read
  like a list of conversations rather than a list of processes.
- **Row subtitle** is `win <index> · <command> · <status>`, so the tmux coordinates are still visible
  for anyone who wants them.
- **"Needs you" group** is pinned at the top and only appears when something is `waiting`. This is
  the same signal that fires the push notification, so the app and the lock screen agree.
- **Search** filters across session, window, pane title and command.
- **`✚ New`** opens a sheet: new window in a session, or new session. Rename and kill live in each
  row's long-press menu.
- On screens ≥ 900 px (the laptop browser) the drawer is pinned open beside the content instead of
  overlaying it.

### The main area - chat shell, terminal content

```
┌───────────────────────────┐
│ ☰  woment-react        ⋮  │
│    saas · win 9 · codex   │
├───────────────────────────┤
│                           │
│  › Ran gh issue view 235  │
│    {"state":"OPEN"...}    │
│                           │
│  Filed and verified all   │
│  three, with repro steps  │
│  and acceptance criteria: │
│                           │
│   - #233 CV uploads       │
│   - #234 salary 500       │
│                           │
│  ▸ Worked for 1m 31s      │
├───────────────────────────┤
│ esc ⏎ tab ↑ ↓ 1 2 y n  ⟩  │
├───────────────────────────┤
│ ┌───────────────────────┐ │
│ │ Message codex...    ➤ │ │
│ └───────────────────────┘ │
└───────────────────────────┘
```

The frame is a chat app. **The content is not** - it is the real tmux screen rendered through
`ansi.ts`, with its real colors. Nothing is parsed into speaker turns, so it works with any agent and
any TUI on day one and cannot break when Codex or Claude Code changes its rendering. The
Markdown/bubble view stays out of v1, as in Kevin's brief.

- **Top bar**: hamburger, pane title, status dot, and a `⋮` menu (Interrupt, Focus on laptop, Font
  size, Wrap toggle, Rename, Kill pane). Second line is the `session · window · command` breadcrumb.
- **Output**: **wrapped by default** - long lines reflow to phone width, which is the right call for
  reading agent prose, explanations and lists. A `⋮` → **Mirror** toggle switches to
  `white-space: pre` + horizontal scroll for when box-drawing, tables or diffs need exact alignment.
  The choice persists per device.
- Font size −/+ (10-20 px), persisted.
- Auto-stick to bottom; scrolling up holds position and shows a "jump to latest" pill.
- **Composer**: auto-growing textarea placeholder-labelled with the target (`Message codex…`), send
  button, and a "submit with Enter" toggle for agents that want the text staged first.
- **Key pad**: one horizontally scrollable row of chips above the composer -
  `Esc  ⏎  Tab  ⇧Tab  ↑ ↓ ← →  1 2 3  y  n  ^C`. Always reachable with a thumb.
- **Settings sheet**: font size, wrap default, poll rate, capture depth, notification toggles.

### Every other screen

There is **no connection screen**. tsnet removes the setup it would have configured: no server URL
(the URL is the app), no token, no pairing. Connection stops being a step and becomes a status - a
dot in the drawer footer and a card in Settings.

**Full screens**

| Screen | When | Notes |
|---|---|---|
| Pane view | default | the mock above |
| Boot skeleton | first `/api/tree` in flight | ~200 ms; skeleton, never a spinner |
| Offline / cached | health probe fails | see below - this is the real "connection screen" |
| Not authorized | `WhoIs` returned a different login → 403 | must state plainly that nothing reached tmux |
| Settings | from the drawer footer | connection card, notifications, display |

**Sheets** - Create (window or session), Rename, both bottom sheets over a scrim.
**Inline states** - reconnect banner, notification opt-in bar, jump-to-latest pill, search-no-match.
**Empty states** - no tmux server running (CTA: create first session), pane gone while you were
reading it.

### Offline: cached, read-only

The service worker keeps the last snapshot of each pane Kevin has opened, so a dead connection still
shows the last thing the agent said - useful on a train, in a lift, or when the Mac has dozed off.

The hard rule: **stale content must never look live.** Enforced three ways - an amber banner naming
the age (`Cached view · last reached 4 min ago`), the output dimmed to 62 %, and the composer and key
pad disabled, not just ignored. The top-bar status dot switches to grey `stale` rather than showing
the pane's last known agent state, because that state is exactly what can no longer be trusted.

Diagnosis needs no third-party ping:

```
navigator.onLine === false        → "Your phone is offline"
probe fails, navigator online     → "Can't reach your Mac"
                                     · Is Tailscale on this phone?
                                     · Is the Mac awake and powered?
                                     · Last reached: 4 minutes ago
```

"Last reached" is the highest-value line - it separates "I just walked into a lift" from "the Mac
went to sleep an hour ago".

### Notification opt-in

`Notification.requestPermission()` needs a user gesture, and a denial is painful to reverse on
Android, so the app never asks on launch. After the first pane is opened, a single dismissible bar
appears: *"Get notified when an agent needs an answer"* → Enable / Not now. Asked once, never again;
Settings keeps the switch either way.

### `ansi.ts`

Small ANSI → HTML converter, no dependency. Handles SGR reset/bold/dim/italic/underline/inverse,
16-color, `38;5;n` 256-color, `38;2;r;g;b` truecolor, and OSC 8 hyperlinks (rendered as real `<a>`,
which the captures on this machine are full of). Every other CSI/OSC sequence is stripped. All text
is escaped before insertion - the converter builds spans, never raw HTML from pane content.

### Safety on destructive taps

Kill pane / kill window / kill session use a **hold-to-confirm** button: press and hold 800 ms, with
a filling progress ring. No modal to mis-tap, and impossible to trigger by accident while scrolling.

### Connection and reconnect

Header shows a state dot: connecting / live / offline. The WebSocket reconnects with exponential
backoff (0.5 s → 8 s). On `visibilitychange` back to visible, it reconnects immediately and refetches
the tree and the current pane - so locking the phone and coming back lands on the same work.

### PWA

`manifest.webmanifest` + a network-first service worker that caches only the app shell (never API
responses). Installable to the home screen thanks to tsnet's real TLS. The same service worker
handles `push` and `notificationclick` (see `internal/push`), so it earns its place twice.

---

## Files

```
remux/
  go.mod  main.go                  flags: --local, --hostname, --lines, --poll
  internal/config/config.go        allowed login, poll interval; ~/.config/remux/config.json
  internal/tsnode/tsnode.go        tsnet.Server setup, ListenTLS, LocalClient, --local fallback
  internal/tmux/tmux.go            exec wrapper, ID validation
  internal/tmux/model.go           Session/Window/Pane, Tree(), previews
  internal/tmux/actions.go         send text/keys, create, rename, kill, focus
  internal/agent/status.go         state heuristics (single file, easy to tune)
  internal/api/router.go  auth.go (WhoIs)  ws.go
  internal/web/embed.go            go:embed ../../web/dist
  web/                             vite + react + ts
    src/{App,router,api,ws,ansi,push}.ts(x)
    src/shell/{Drawer,PaneList,PaneRow,TopBar}.tsx      chat-style navigation
    src/pane/{PaneView,Output,Composer,KeyPad}.tsx      the focused "conversation"
    src/sheets/{NewSheet,RenameSheet,SettingsSheet}.tsx
    src/components/{HoldButton,StatusDot,ConnBar}.tsx
    src/styles.css
    public/{manifest.webmanifest,sw.js,icon.svg}
  scripts/build.sh                 vite build → go build → bin/remux
  scripts/com.viminizer.remux.plist    launchd
  README.md
```

---

## Setup Kevin runs afterwards

Nothing is installed on the Mac except the binary itself.

1. `./scripts/build.sh` → `bin/remux` (single file: UI + Tailscale + server).
2. In the Tailscale admin console, enable **MagicDNS** and **HTTPS Certificates**. Two toggles, once
   per tailnet. `ListenTLS` needs them to issue the cert.
3. `./bin/remux` → prints a login URL on first run. Open it, approve the new device. State is
   written to `~/.config/remux/tsnet/`; every later start is silent.
4. Install the Tailscale app on the Android phone and sign in (this part is unavoidable - the phone
   has to be on the tailnet).
5. Open `https://remux.<tailnet>.ts.net` on the phone → Chrome menu → Add to Home screen.
   Real TLS means this actually installs.
6. `launchctl load` the plist to keep it running across reboots.
7. Keeping the Mac awake is on Kevin (`caffeinate -dims` or Amphetamine) - the README says so.

For UI work at the laptop: `./bin/remux --local` → `http://127.0.0.1:7399`, no tailnet, no
auth check.

**Verified on this machine:** Tailscale is not currently installed - no CLI, no
`/Applications/Tailscale.app`. With `tsnet` that no longer matters for the Mac. Kevin does still
need a Tailscale account and the app on the phone.

---

## Verification

**The laptop must not move.** Snapshot before and after driving the app from the phone:

```
tmux display-message -p '#{session_name}:#{window_index}.#{pane_index}'   # active target
tmux list-panes -a -F '#{pane_id} #{pane_width}x#{pane_height}'           # layout
```

Both must be byte-identical after browsing every screen, opening panes, and sending text. This is the
single most important test - it is the requirement that rules out the whole attach-based approach.

**Functional pass**, against a scratch session (`tmux new-session -d -s remote-test`) so nothing real
is at risk:

- `./bin/remux --local` then `curl localhost:7399/api/tree` returns 3 sessions, 17 windows,
  26 panes with correct commands and statuses.
- On the tailnet: request from an allowed identity → 200; from a different tailnet user → 403.
  `WhoIs` must also gate the `/ws` upgrade, not just REST.
- Binary size and RSS measured and written into the README, so the tsnet cost is a recorded fact
  rather than an estimate.
- WebSocket: subscribe to the scratch pane, `echo hi` in it from the laptop, confirm one `snap`
  arrives within ~400 ms and that no `snap` is sent while the pane is quiet.
- Send a **multi-line** prompt with `submit:false` to a real codex pane → it appears in the composer
  as multiple lines, not submitted. Then `keys:["Enter"]` submits it.
- Send `Escape`, `Up`, `1` to an agent showing a choice menu → navigates correctly.
- Interrupt a running agent → C-c lands, status flips `busy` → `idle`.
- Kill the scratch pane via hold-to-confirm → gone; a short tap does nothing.
- Create + rename a session and a window → they appear, and the laptop's active window is unchanged.

**Phone pass**: over `https://remux.<tailnet>.ts.net`, confirm Chrome shows a valid cert and
offers "Add to Home screen". Install it, open a codex pane, read it, send an instruction, lock the
phone for a minute, unlock → reconnects and shows the same pane, current. Then turn Tailscale off on
the phone → the app must fail closed, not hang.

---

## Known limits, stated up front

- Status badges are regex heuristics over terminal output. When Codex or Claude Code changes its
  rendering they degrade to `unknown` - they never break reading or sending. All patterns are in one
  file.
- v1 mirrors the rendered terminal screen. The Markdown/conversation view described in the brief
  needs per-agent integration and is deliberately out of scope here.
- Scrollback is whatever `capture-pane -S -N` returns (default 400 lines, adjustable). Panes in an
  alternate screen (vim, htop) have no history - only the visible screen.
