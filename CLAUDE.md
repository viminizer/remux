# remux

Read [`AGENTS.md`](AGENTS.md) first. It is the build instruction and it overrides anything else here.

Then read `docs/plan.md` (the spec) and `docs/ui-mock.html` (the UI to port, not redesign).

## The one rule that matters most

This machine is running Kevin's real work: ~26 tmux panes, several of them live Codex and Claude
Code agents. **Never** `attach-session`, `-CC`, `select-window`, `select-pane`, `switch-client`, or
resize anything. Read-only tmux commands against the real workspace are fine. Every write goes to
the `remux-test` scratch session and nowhere else.

There is exactly one exception, and it is the product, not a test: remux sets `@remux_title`,
`@remux_task`, `@remux_state` and `@remux_project` on real panes (see **Pane names on the laptop**
in the README).
A user option is inert - nothing renders it unless the tmux config asks for it - which is what
makes it the one safe thing to write. `scripts/laptop-invariant.sh` reports every such write and
fails if the value is outside what remux is allowed to write. Nothing else may be written to a
real pane, and this list does not grow without the same treatment.

The agent harness owns every session named `loop-*` (see **Agent loops** in the README). It
creates them detached, writes `@loop_*` options on them, and kills them. They are new sessions no
client is attached to, so this moves nothing of Kevin's. `internal/tmux/loops.go` refuses those
writes for any other session, and `laptop-invariant.sh` treats `loop-*` like `remux-test`.

Prove it after any phase that touches tmux:

```bash
./scripts/laptop-invariant.sh before
# ... tests ...
./scripts/laptop-invariant.sh after
```
