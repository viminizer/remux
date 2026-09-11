#!/usr/bin/env bash
#
# Proves remux did not disturb the laptop.
#
# Snapshots the tmux workspace outside the remux-test scratch session, then
# diffs the two snapshots. If the active window moved, a pane was resized, a
# pane vanished, a window or session was renamed, a client attached, or any
# pane option changed, this fails.
#
#   ./scripts/laptop-invariant.sh before
#   ... run tests ...
#   ./scripts/laptop-invariant.sh after
#
# The three @remux_* pane options are the one thing remux may write outside the
# scratch session: @remux_task and @remux_state on its own initiative - that is
# the pane-naming feature working, not damage - and @remux_title when Kevin
# renames a pane from the phone. Everything else about a pane must be identical.
#
# They are not simply filtered out. A check that cannot fail on the writes it
# was extended for proves less than the one it replaced, so they get their own
# snapshot instead: every change to them is printed, and a value outside what
# remux is allowed to write is a failure. The glyph vocabulary and the title
# length below are the writer's own limits (internal/titler/state.go and
# title.go); a value outside them means the writer is wrong, which is exactly
# the kind of damage this script exists to catch.
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

# Session and window names, and which window each session has active. A rename
# or a jump inside a session that nobody is attached to would not show up in
# the active target above.
tmux list-windows -a -F '#{session_name} #{window_index} #{window_name} active=#{window_active}' \
  > "$DIR/$TAG.wraw"
grep -v "^$SCRATCH " "$DIR/$TAG.wraw" > "$DIR/$TAG.windows" || true
sort -o "$DIR/$TAG.windows" "$DIR/$TAG.windows"

# Attached clients. remux must never become one: a client's terminal size is
# what renegotiates pane dimensions.
tmux list-clients -F '#{client_tty} #{client_session} #{client_width}x#{client_height}' \
  > "$DIR/$TAG.clients" || true
sort -o "$DIR/$TAG.clients" "$DIR/$TAG.clients"

# Every pane option outside the scratch session, split into the three remux is
# allowed to write and everything else.
#
# pane_title is deliberately not snapshotted: agents rewrite it on every
# render, so it changes on its own and proves nothing.
: > "$DIR/$TAG.options"
: > "$DIR/$TAG.remux"
while read -r sess pane; do
  [ "$sess" = "$SCRATCH" ] && continue
  tmux show-options -p -t "$pane" > "$DIR/.opts" || true
  grep -E '^@remux_(title|task|state)' "$DIR/.opts" \
    | sed "s|^|$pane |" >> "$DIR/$TAG.remux" || true
  grep -vE '^@remux_(title|task|state)' "$DIR/.opts" \
    | sed "s|^|$pane |" >> "$DIR/$TAG.options" || true
done < <(tmux list-panes -a -F '#{session_name} #{pane_id}')
rm -f "$DIR/.opts"
sort -o "$DIR/$TAG.options" "$DIR/$TAG.options"
sort -o "$DIR/$TAG.remux" "$DIR/$TAG.remux"

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

if ! diff -u "$DIR/before.windows" "$DIR/after.windows"; then
  echo "FAIL: a window or session outside $SCRATCH was renamed, added, or made active." >&2
  fail=1
fi

if ! diff -u "$DIR/before.clients" "$DIR/after.clients"; then
  echo "FAIL: the set of attached tmux clients changed." >&2
  echo "      Something called attach-session or -CC." >&2
  fail=1
fi

if ! diff -u "$DIR/before.options" "$DIR/after.options"; then
  echo "FAIL: a pane option changed outside $SCRATCH." >&2
  echo "      Only @remux_title, @remux_task and @remux_state may be written." >&2
  fail=1
fi

# The exempted options. A change here is the feature working, so it is reported
# rather than failed - but it is reported, because a write nobody sees is how
# this check would have become decorative.
if ! diff -q "$DIR/before.remux" "$DIR/after.remux" > /dev/null; then
  echo "note: remux wrote its own pane options (allowed):"
  diff -u "$DIR/before.remux" "$DIR/after.remux" | grep -E '^[+-]@|^[+-]%' | sed 's|^|      |' || true
fi

# What it wrote still has to be something remux is capable of writing. A glyph
# outside the vocabulary, a title past the length clean() enforces, or an
# option on a pane it has no business touching all mean the writer is wrong.
while read -r pane opt value; do
  # tmux quotes a value that contains a space, so "venue filter pagination"
  # arrives with the quotes still on it.
  value="${value#\"}"
  value="${value%\"}"
  case "$opt" in
    @remux_state)
      case "$value" in
        '!'|'✳'|'✓'|'') ;;
        *)
          echo "FAIL: $pane has @remux_state $value, which is not one of ! ✳ ✓" >&2
          fail=1
          ;;
      esac
      ;;
    @remux_task)
      # Bytes, because clean()'s cap is a Go len() and so is a byte count.
      n=$(printf '%s' "$value" | wc -c | tr -d ' ')
      if [ "$n" -gt 48 ]; then
        echo "FAIL: $pane has a $n-byte @remux_task; clean() caps it at 48" >&2
        fail=1
      fi
      ;;
  esac
done < "$DIR/after.remux"

if [ "$fail" -eq 0 ]; then
  echo "PASS: laptop untouched ($(wc -l < "$DIR/after.layout" | tr -d ' ') panes unchanged)"
fi

exit "$fail"
