# remux

Read [`AGENTS.md`](AGENTS.md) first. It is the build instruction and it overrides anything else here.

Then read `docs/plan.md` (the spec) and `docs/ui-mock.html` (the UI to port, not redesign).

## The one rule that matters most

This machine is running Kevin's real work: ~26 tmux panes, several of them live Codex and Claude
Code agents. **Never** `attach-session`, `-CC`, `select-window`, `select-pane`, `switch-client`, or
resize anything. Read-only tmux commands against the real workspace are fine. Every write goes to
the `remux-test` scratch session and nowhere else.

Prove it after any phase that touches tmux:

```bash
./scripts/laptop-invariant.sh before
# ... tests ...
./scripts/laptop-invariant.sh after
```
