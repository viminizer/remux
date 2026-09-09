/**
 * The key chips above the composer, in two states.
 *
 * It used to be one row that scrolled sideways. That stopped scaling at about
 * eight chips on a phone: everything past the eighth needed a horizontal swipe
 * to a position you could not predict, and horizontal scroll gives no hint
 * anything is out there. There are 20 chips now.
 *
 * So it is one six-column grid, and the two states differ only in how much of
 * it is rendered: collapsed is the first row, expanded is all of it. Nothing
 * scrolls in either. Expanded pushes the output up rather than covering it,
 * because covering the screen you are reading while you decide what to send is
 * the wrong trade.
 *
 * A chip is one of three things. A key name goes through the keys endpoint,
 * which is allowlisted server-side, so this list and KeyAllowlist in
 * internal/tmux/actions.go have to agree. A `text` chip is a literal character
 * and goes through the text endpoint instead - the same path the composer
 * uses, so it needs no server change and does not widen the allowlist, whose
 * whole job is keeping arbitrary strings out of send-keys.
 *
 * A `command` chip is a `text` chip that clears the input line first. Slash
 * commands only register at the start of an empty composer, so typing one into
 * a half-written prompt produces a line that silently does nothing.
 *
 * These are Claude Code commands, and the pad has no per-pane filtering -
 * `disabled` is only stale || !current - so they show on Codex and plain shell
 * panes too, where they are just text. Filtering chips by pane command would
 * be a larger change than this.
 */
import type { CustomChip } from '../store'
import type { PaneKind } from '../types'
import { paneKind } from '../types'

type Chip = {
  /**
   * What gets sent. For a key chip this is the tmux key name; for a text or
   * command chip it is the literal string.
   */
  k: string
  /**
   * React key. Two custom chips may legitimately send the same string, so
   * identity cannot come from `k`. Built-ins are unique and fall back to it.
   */
  id?: string
  label: string
  /** Span two grid columns, for a label that will not fit an eighth. */
  wide?: boolean
  /**
   * Pane kinds this chip is not worth a cell on. Absent means every pane.
   *
   * A hide-list rather than a show-list so that each entry is a claim someone
   * can check: `hideOn: ['shell']` says "useless at a shell prompt", which is
   * either true or not. A show-list would silently hide the chip from every
   * kind nobody thought to name.
   */
  hideOn?: PaneKind[]
  text?: boolean
  command?: boolean
  danger?: boolean
  /**
   * Whether tapping this leaves the panel open.
   *
   * Auto-collapse is a property of the chip, not of the panel. Arrows get
   * tapped three or four times to walk back through history, and ⌫ more than
   * that; collapsing after each one would make them unusable. One-shot chips
   * collapse, because after them you are going back to reading.
   */
  repeat?: boolean
}

/**
 * The grid is eight wide, and the collapsed row is its first row - the same
 * eight chips in the same places, so expanding adds to what you were looking
 * at instead of rearranging it.
 */
const COLUMNS = 8

/**
 * One flat list in grid order. The first COLUMNS entries are the collapsed
 * row, and they are the eight worth a permanent slot: the four keys a TUI
 * answers to; $ and / because both sit one modifier layer deep on the iOS and
 * Android keyboards, which is the cost these chips exist to remove; and ↑ ↓,
 * which with ⏎ are a whole answer to an agent's numbered prompt without ever
 * expanding the pad.
 *
 * The slash commands are two columns wide - `/compact` does not fit an eighth
 * of a phone - so 20 chips fill 22 cells and it comes out 8 / 6 / 6 with no
 * ragged row but the last. Reordering this changes the layout.
 */
const CHIPS: Chip[] = [
  { k: 'Escape', label: 'esc' },
  { k: 'Enter', label: '⏎' },
  { k: 'Tab', label: 'tab', repeat: true },
  { k: 'BSpace', label: '⌫', repeat: true },
  { k: '$', label: '$', text: true },
  { k: '/', label: '/', text: true },
  { k: 'Up', label: '↑', repeat: true },
  { k: 'Down', label: '↓', repeat: true },
  // Slash commands need an agent's composer to mean anything. Hidden at a
  // shell prompt, where they are literal text, and on `other` - vim, python, a
  // pager - for the same reason. Left on every agent rather than on Claude
  // Code alone: these are Claude Code's commands, but Codex advertises slash
  // commands of its own, and hiding a chip that works is worse than showing
  // one that does not.
  { k: '/clear', label: '/clear', command: true, wide: true, hideOn: ['shell', 'other'] },
  { k: '/compact', label: '/compact', command: true, wide: true, hideOn: ['shell', 'other'] },
  { k: 'Left', label: '←', repeat: true },
  { k: 'Right', label: '→', repeat: true },
  // Answers to an agent's prompt. Hidden only at a shell, where you would
  // type them; kept on `other`, because a digit is a count in vim and `y` is
  // yank, and those are real uses rather than leftovers.
  { k: 'y', label: 'y', hideOn: ['shell'] },
  { k: 'n', label: 'n', hideOn: ['shell'] },
  { k: '1', label: '1', hideOn: ['shell'] },
  { k: '2', label: '2', hideOn: ['shell'] },
  { k: '3', label: '3', hideOn: ['shell'] },
  { k: 'BTab', label: '⇧tab', repeat: true },
  // ^U is the one that answers the common case - accept a suggestion with
  // tab, change your mind, clear the line in one tap. Neither it nor ⌫ is
  // `danger`; they touch the input line, not the process, and ^C stays the
  // only red chip.
  { k: 'C-u', label: '^U' },
  { k: 'C-c', label: '^C', danger: true },
]

/**
 * A custom chip states where it belongs; the pad filters on where a chip does
 * not. This is the one place the two meet.
 *
 * `agent` and `shell` both exclude `other`, unlike the built-ins: a person who
 * picked "agents only" in Settings said what they meant, and guessing past
 * that would be second-guessing them.
 */
const HIDE_FOR: Record<'all' | 'agent' | 'shell', PaneKind[]> = {
  all: [],
  agent: ['shell', 'other'],
  shell: ['claude', 'agent', 'other'],
}

export function KeyPad({
  onKey,
  onText,
  onCommand,
  expanded,
  onToggle,
  disabled,
  custom,
  command,
}: {
  onKey: (k: string) => void
  onText: (t: string) => void
  onCommand: (t: string) => void
  expanded: boolean
  onToggle: () => void
  disabled: boolean
  custom: CustomChip[]
  /** pane_current_command of the pane on screen, for choosing chips. */
  command: string
}) {
  // Custom chips lead the expanded area rather than joining the collapsed row.
  // The collapsed row is the same eight chips in the same eight places on
  // every pane and every session, and that predictability is most of what
  // makes it usable without looking; a list that grows and shrinks from
  // Settings would take it away. Leading the second row is the next best
  // position - first thing under the thumb when the pad opens.
  //
  // A half-filled chip is dropped rather than rendered. Settings adds a blank
  // row for you to fill in, so an incomplete one is a normal intermediate
  // state, not an error worth reporting.
  const extra: Chip[] = custom
    .filter((c) => c.label.trim() !== '' && c.text !== '')
    .map((c) => ({
      k: c.text,
      id: c.id,
      label: c.label,
      wide: c.wide,
      text: !c.command,
      command: c.command,
      hideOn: HIDE_FOR[c.on ?? 'all'],
    }))

  // Filtering stops at the collapsed row. Those eight are the same chips in
  // the same eight places on every pane, which is what makes them usable
  // without looking; letting a pane switch reflow them would cost more than
  // the cells it saved. They are also the eight that are useful everywhere -
  // esc, enter, tab, backspace, $, /, and the two arrows - so nothing is
  // given up by exempting them.
  const kind = paneKind(command)
  const visible = (c: Chip) => !c.hideOn?.includes(kind)

  const shown = expanded
    ? [...CHIPS.slice(0, COLUMNS), ...extra.filter(visible), ...CHIPS.slice(COLUMNS).filter(visible)]
    : CHIPS.slice(0, COLUMNS)

  const press = (key: Chip) => {
    if (key.command) onCommand(key.k)
    else if (key.text) onText(key.k)
    else onKey(key.k)
    if (expanded && !key.repeat) onToggle()
  }

  return (
    // The chevron is a sibling of .keypad, never a child: it floats above the
    // pad's top edge, which a grid item cannot do.
    <div className="keypad-wrap">
      <div className={`keypad ${expanded ? 'open' : ''}`}>
        {shown.map((key) => (
          <button
            key={key.id ?? key.k}
            className={`key ${key.wide ? 'wide' : ''} ${key.danger ? 'danger' : ''}`}
            disabled={disabled}
            onClick={() => press(key)}
          >
            {key.label}
          </button>
        ))}
      </div>
      {/* Not disabled with the chips. Expanding sends nothing to the pane, so
          there is no reason a stale connection should stop you looking at what
          you could send once it comes back. */}
      <button
        className={`kp-toggle ${expanded ? 'open' : ''}`}
        onClick={onToggle}
        aria-expanded={expanded}
        aria-label={expanded ? 'Collapse the key pad' : 'Show all keys'}
      >
        {/* Two chevrons, not one: a single one reads as "scroll up". Drawn
            rather than typed because ⌃ is the only caret with usable metrics
            and there is no double of it. */}
        <svg
          viewBox="0 0 24 24"
          width="14"
          height="14"
          fill="none"
          stroke="currentColor"
          strokeWidth="2.6"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <path d="M5 11 12 5l7 6" />
          <path d="M5 19 12 13l7 6" />
        </svg>
      </button>
    </div>
  )
}
