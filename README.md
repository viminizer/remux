# remux

Supervise your Mac's tmux sessions and the AI coding agents running inside them - from your phone.

Start an agent at the laptop, keep talking to it from the train, come back to the laptop. Your Mac
stays the machine doing the work.

> Status: **design.** No code yet. See [`docs/plan.md`](docs/plan.md).

## What it is

One Go binary on the Mac. It talks to tmux, embeds a Tailscale node, and serves a mobile web app
that installs to the Android home screen.

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

- **One file.** The React UI is compiled in with `go:embed`. No runtime, no `node_modules`, nothing
  to install beside it.
- **Tailscale is embedded**, not installed. `tsnet` joins the tailnet from inside the process - no
  `tailscaled`, no root, no `tailscale serve`. It also issues real TLS, which is what lets the PWA
  install to the home screen.
- **No tokens.** `WhoIs()` asks the tailnet who is calling; only your identity gets through.
- **Your laptop never moves.** The service never attaches as a tmux client, so it can't resize your
  panes or switch your active window. Everything is done with targeted commands
  (`capture-pane -t %14`, `send-keys -t %14`).

## Install

One file. Nothing else goes on the Mac.

```bash
curl -L -o remux <release url>
chmod +x remux
./remux install        # launchd agent, starts at boot
```

Measured: **21 MB** stripped for a tsnet + TLS + WhoIs binary, ~23 MB with the UI compiled in.

The binary handles what it can and walks you through what it cannot. It preflights `tmux`, the
config dir and the port on every start; prints a login URL once so you can approve the device; turns
the opaque TLS error you get when MagicDNS or HTTPS Certificates are off into a one-line
instruction; and prints a QR of your `https://remux.<tailnet>.ts.net` URL so you never type a
tailnet name into a phone keyboard.

What it genuinely cannot do for you, because it is account-level, not software:

- a Tailscale account (free)
- approving the device once, in a browser
- two admin-console toggles: MagicDNS and HTTPS Certificates
- the Tailscale app on the phone

## The interface

A chat app where each tmux pane is a conversation: drawer of panes grouped by session, one focused
pane filling the screen, composer pinned at the bottom, touch keys for `Esc` / arrows / `y` / `n` so
you can answer an agent's menu with a thumb.

Panes that are blocked waiting on you float to the top of the drawer - and push a notification, so
you don't have to keep opening the app to find out.

Open [`docs/ui-mock.html`](docs/ui-mock.html) in a browser for a clickable mock of every screen.

## Docs

| File | What |
|---|---|
| [`docs/plan.md`](docs/plan.md) | Full design: tmux layer, API, WebSocket protocol, UI, verification |
| [`docs/ui-mock.html`](docs/ui-mock.html) | Clickable mock - all 9 screens, fake data, no server |

## License

Private, unlicensed.
