# Settings v2 mockup

Standalone interactive reference: [settings-v2.html](settings-v2.html).

The proposed layout groups connection, reading, notifications, input/keypad,
agent naming and app updates. It labels device-local versus Mac-wide settings,
keeps growing keypad/naming editors on their existing screens, and keeps the
updater in Settings. This is a proposal awaiting visual approval, not a shipped
application change.

Open Preview states to simulate an offline or denied connection, notification
permission states, a failed Mac setting save, or an available interface update.
Changing font size updates the terminal sample. Failed saves retain the previous
setting and offer Retry. All interactions are in memory; no API, notifications,
model calls, real settings saves or actual reloads are invoked.

Verification (2026-10-29): inline JavaScript parse check; real-browser screenshots
at 390px and 1200px; 320px horizontal-overflow check; offline/blocked/update states;
failed-save retention and successful retry; no browser errors. Screenshots and
interactive export are local under `/Users/mac/.agent/diagrams/`. Phone and desktop
screenshots were sent through the user's configured Telegram bot at their request.
The private interactive preview is served on Tailscale port 8445.

Application implementation, reviews and release remain pending design approval.
