#!/usr/bin/env bash
#
# Proves remux did not disturb the laptop.
#
# Snapshots the active tmux target and the geometry of every pane outside the
# remux-test scratch session, then diffs the two snapshots. If the active window
# moved, a pane was resized, or a pane vanished, this fails.
#
#   ./scripts/laptop-invariant.sh before
#   ... run tests ...
#   ./scripts/laptop-invariant.sh after
#
set -euo pipefail

SCRATCH="remux-test"
DIR="${TMPDIR:-/tmp}/remux-invariant"
TAG="${1:-}"

if [ "$TAG" != "before" ] && [ "$TAG" != "after" ]; then
  echo "usage: $0 before|after" >&2
  exit 2
fi

mkdir -p "$DIR"

# Which window/pane the laptop is looking at.
tmux display-message -p '#{session_name}:#{window_index}.#{pane_index}' > "$DIR/$TAG.active"

# Geometry of every pane that is not part of the scratch session.
tmux list-panes -a -F '#{session_name} #{pane_id} #{pane_width}x#{pane_height}' > "$DIR/$TAG.raw"
grep -v "^$SCRATCH " "$DIR/$TAG.raw" > "$DIR/$TAG.layout" || true
sort -o "$DIR/$TAG.layout" "$DIR/$TAG.layout"

if [ "$TAG" = "before" ]; then
  echo "snapshot saved  ($(wc -l < "$DIR/before.layout" | tr -d ' ') panes outside $SCRATCH)"
  exit 0
fi

if [ ! -f "$DIR/before.active" ]; then
  echo "FAIL: no 'before' snapshot. Run '$0 before' first." >&2
  exit 2
fi

fail=0

if ! diff -u "$DIR/before.active" "$DIR/after.active"; then
  echo "FAIL: the laptop's active window or pane moved." >&2
  echo "      Something called select-window, select-pane or switch-client." >&2
  fail=1
fi

if ! diff -u "$DIR/before.layout" "$DIR/after.layout"; then
  echo "FAIL: pane geometry changed outside $SCRATCH." >&2
  echo "      Either a client attached and renegotiated sizes, or a real pane was killed." >&2
  fail=1
fi

if [ "$fail" -eq 0 ]; then
  echo "PASS: laptop untouched ($(wc -l < "$DIR/after.layout" | tr -d ' ') panes unchanged)"
fi

exit "$fail"
