#!/usr/bin/env bash
#
# Proves remux did not disturb the laptop.
#
# Snapshots the tmux workspace outside the remux-test scratch session, then
# diffs the two snapshots. If a pane was resized, a pane vanished, a client
# attached, or any pane option outside the four below changed, this fails.
#
#   ./scripts/laptop-invariant.sh before
#   ... run tests ...
#   ./scripts/laptop-invariant.sh after
#
# The four @remux_* pane options are the one thing remux may write outside the
# scratch session: @remux_task, @remux_state and @remux_project on its own
# initiative - that is the pane-naming feature working, not damage - and
# @remux_title when Kevin renames a pane from the phone. Everything else about
# a pane must be identical.
#
# They are not simply filtered out. A check that cannot fail on the writes it
# was extended for proves less than the one it replaced, so they get their own
# snapshot instead: every change to them is printed, and a value outside what
# remux is allowed to write is a failure. The glyph vocabulary, the title
# length and the project length below are the writer's own limits
# (internal/titler/state.go, title.go and project.go); a value outside them
# means the writer is wrong, which is exactly the kind of damage this script
# exists to catch.
#
# That same argument is why the rest of the workspace is now split by what a
# change means rather than by whether it changed at all. This machine is never
# idle: Kevin opens a window and looks at another one, and a Claude Code pane
# renames its own window to its version string with nobody at the keyboard.
# None of that is remux, and failing on it taught the reader to scroll past
# the gate - which is when a real violation goes through unread.
#
# So: a pane that vanished, a pane that changed size, a client that appeared,
# a pane option outside the four - that is what remux damaging the laptop
# looks like, and it still fails. A window renamed or added, a pane opened,
# the cursor moved, a client leaving - printed as notes, because the laptop
# does all of those to itself and none of them destroys anyone's work.
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

# Every pane option outside the scratch session, split into the four remux is
# allowed to write and everything else.
#
# pane_title is deliberately not snapshotted: agents rewrite it on every
# render, so it changes on its own and proves nothing.
: > "$DIR/$TAG.options"
: > "$DIR/$TAG.remux"
while read -r sess pane; do
  [ "$sess" = "$SCRATCH" ] && continue
  tmux show-options -p -t "$pane" > "$DIR/.opts" || true
  grep -E '^@remux_(title|task|state|project)' "$DIR/.opts" \
    | sed "s|^|$pane |" >> "$DIR/$TAG.remux" || true
  grep -vE '^@remux_(title|task|state|project)' "$DIR/.opts" \
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

# The cursor. Kevin moves it by looking at something, and remux moving it is
# the Focus feature doing what the phone asked. Neither costs anyone work, so
# it is reported and not failed - but it is reported, because a switch-client
# nobody asked for is still worth seeing next to whatever else this run did.
if ! diff -q "$DIR/before.active" "$DIR/after.active" > /dev/null; then
  echo "note: the active window moved: $(cat "$DIR/before.active") -> $(cat "$DIR/after.active")"
fi

# Panes. A pane cannot vanish or change size without someone losing something:
# a killed pane takes its scrollback and its running agent with it, and a size
# change means a client attached and renegotiated the whole session's geometry.
# A pane that appeared is Kevin splitting a window.
panes=$(awk 'FILENAME == ARGV[1] { b[$2] = $1 " " $3; next }
             { a[$2] = $1 " " $3 }
             END {
               for (p in b) if (!(p in a)) print "FAIL", p, "vanished  (" b[p] ")"
               for (p in a) if ((p in b) && a[p] != b[p]) print "FAIL", p, "changed   (" b[p] " -> " a[p] ")"
               for (p in a) if (!(p in b)) print "note", p, "appeared  (" a[p] ")"
             }' "$DIR/before.layout" "$DIR/after.layout" | sort)
if printf '%s\n' "$panes" | grep -q '^FAIL'; then
  echo "FAIL: a pane outside $SCRATCH vanished or changed size." >&2
  echo "      Either a client attached and renegotiated sizes, or a real pane was killed." >&2
  printf '%s\n' "$panes" | sed -n 's|^FAIL |      |p' >&2
  fail=1
fi
printf '%s\n' "$panes" | sed -n 's|^note |note: pane |p'

# Windows. A window cannot outlive its last pane, so a window that was killed
# already failed above as a vanished pane. What is left here is renaming,
# opening and which window a session has active - and the renaming is not even
# done by a person: Claude Code panes rename their window to their version
# string on their own.
if ! diff -q "$DIR/before.windows" "$DIR/after.windows" > /dev/null; then
  echo "note: windows outside $SCRATCH were renamed, opened or made active:"
  diff -u "$DIR/before.windows" "$DIR/after.windows" | grep -E '^[+-][^+-]' | sed 's|^|      |' || true
fi

# Clients. An attach is the one direction that stays a hard failure, because
# remux becoming a client is the single worst thing it could do - a client's
# terminal size is what renegotiates every pane in the session. Kevin opening
# a terminal mid-run trips this too, and that false alarm is worth keeping:
# it is rare, and the thing it guards is not recoverable.
#
# FILENAME rather than the usual NR==FNR: an empty 'before' is the ordinary
# state here - nobody has to be attached - and NR==FNR reads the second file
# as the first when the first has no lines at all.
clients=$(awk 'FILENAME == ARGV[1] { b[$1] = $2 " " $3; next }
               { a[$1] = $2 " " $3 }
               END {
                 for (c in a) if (!(c in b)) print "FAIL", c, "attached  (" a[c] ")"
                 for (c in b) if (!(c in a)) print "note", c, "detached  (" b[c] ")"
                 for (c in a) if ((c in b) && a[c] != b[c]) print "note", c, "moved     (" b[c] " -> " a[c] ")"
               }' "$DIR/before.clients" "$DIR/after.clients" | sort)
if printf '%s\n' "$clients" | grep -q '^FAIL'; then
  echo "FAIL: a tmux client attached outside $SCRATCH." >&2
  echo "      Something called attach-session or -CC." >&2
  printf '%s\n' "$clients" | sed -n 's|^FAIL |      |p' >&2
  fail=1
fi
printf '%s\n' "$clients" | sed -n 's|^note |note: client |p'

if ! diff -u "$DIR/before.options" "$DIR/after.options"; then
  echo "FAIL: a pane option changed outside $SCRATCH." >&2
  echo "      Only @remux_title, @remux_task, @remux_state and @remux_project" >&2
  echo "      may be written." >&2
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
    @remux_project)
      # Same reasoning, against clamp()'s maxProject in project.go.
      n=$(printf '%s' "$value" | wc -c | tr -d ' ')
      if [ "$n" -gt 12 ]; then
        echo "FAIL: $pane has a $n-byte @remux_project; clamp() caps it at 12" >&2
        fail=1
      fi
      ;;
  esac
done < "$DIR/after.remux"

if [ "$fail" -eq 0 ]; then
  echo "PASS: laptop untouched ($(wc -l < "$DIR/after.layout" | tr -d ' ') panes unchanged)"
fi

exit "$fail"
