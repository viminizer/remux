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

`scripts/laptop-invariant.sh before` / `after` proves it: it snapshots the
active window and every pane's geometry, and fails if anything moved.

The one thing remux writes to panes you are using is three user options -
`@remux_title`, `@remux_task` and `@remux_state` (see **Pane names on the
laptop**). A user option is inert: nothing renders it unless your tmux config
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

remux writes two user options onto each agent pane, so the laptop can read the
same thing the phone does:

| option | what | cost |
|---|---|---|
| `@remux_state` | one glyph: `!` blocked on an answer, `✳` working, `✓` idle | free, from the classifier that already runs |
| `@remux_task` | 3-5 words naming the work, read off the screen by a cheap model | a fraction of a cent, and only when the work changes |

Neither is rendered by tmux on its own. Put them in your status line to see
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
