/**
 * The key chips above the composer, in two states.
 *
 * Collapsed it is one scrolling row, which is all it used to be. That stopped
 * scaling at about eight chips on a phone: everything past the eighth needed a
 * horizontal swipe to a position you could not predict, and horizontal scroll
 * gives no hint anything is out there. There are 20 chips now.
 *
 * Expanded it is a grid of every chip. The point is seeing them all at once -
 * a longer scrolling row would only move the problem. It pushes the output up
 * rather than covering it, because covering the screen you are reading while
 * you decide what to send is the wrong trade.
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
type Chip = {
  k: string
  label: string
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

/** How many of CHIPS the collapsed row shows. */
const ROW = 6

/**
 * One flat list, ordered so the collapsed row is its first ROW entries. The
 * grid is the same list continued rather than a different arrangement you have
 * to re-learn when you expand it.
 *
 * The order is roughly by how often a chip gets tapped, but it is also the
 * grid: five columns, with the two slash commands two columns wide, so the
 * first four rows come out full. Reordering this changes the layout.
 */
const CHIPS: Chip[] = [
  { k: '/clear', label: '/clear', command: true },
  { k: '/compact', label: '/compact', command: true },
  { k: 'Escape', label: 'esc' },
  { k: 'Enter', label: '⏎' },
  { k: 'Tab', label: 'tab', repeat: true },
  { k: 'BSpace', label: '⌫', repeat: true },
  { k: 'Up', label: '↑', repeat: true },
  { k: 'Down', label: '↓', repeat: true },
  { k: 'Left', label: '←', repeat: true },
  { k: 'Right', label: '→', repeat: true },
  { k: 'y', label: 'y' },
  { k: 'n', label: 'n' },
  { k: '1', label: '1' },
  { k: '2', label: '2' },
  { k: '3', label: '3' },
  { k: 'BTab', label: '⇧tab', repeat: true },
  // ^U is the one that answers the common case - accept a suggestion with
  // tab, change your mind, clear the line in one tap. Neither it nor ⌫ is
  // `danger`; they touch the input line, not the process, and ^C stays the
  // only red chip.
  { k: 'C-u', label: '^U' },
  // Both are one modifier layer deep on the iOS and Android keyboards, which
  // is the cost these chips exist to remove.
  { k: '$', label: '$', text: true },
  { k: '/', label: '/', text: true },
  { k: 'C-c', label: '^C', danger: true },
]

export function KeyPad({
  onKey,
  onText,
  onCommand,
  expanded,
  onToggle,
  disabled,
}: {
  onKey: (k: string) => void
  onText: (t: string) => void
  onCommand: (t: string) => void
  expanded: boolean
  onToggle: () => void
  disabled: boolean
}) {
  const shown = expanded ? CHIPS : CHIPS.slice(0, ROW)

  const press = (key: Chip) => {
    if (key.command) onCommand(key.k)
    else if (key.text) onText(key.k)
    else onKey(key.k)
    if (expanded && !key.repeat) onToggle()
  }

  return (
    // The chevron is a sibling of .keypad, never a child. .keypad scrolls
    // horizontally when collapsed, so a child would either clip at the edge or
    // scroll away with the chips - the exact failure this feature fixes.
    <div className="keypad-wrap">
      <div className={`keypad ${expanded ? 'open' : ''}`}>
        {shown.map((key) => (
          <button
            key={key.k}
            className={`key ${key.command ? 'cmd' : ''} ${key.danger ? 'danger' : ''}`}
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
        <span aria-hidden="true">⌃</span>
      </button>
    </div>
  )
}
