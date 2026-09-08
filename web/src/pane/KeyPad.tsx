/**
 * One scrollable row of key chips above the composer.
 *
 * These are the keys an agent's menus actually need, always reachable with a
 * thumb.
 *
 * A chip is one of two things. A key name goes through the keys endpoint,
 * which is allowlisted server-side, so this list and KeyAllowlist in
 * internal/tmux/actions.go have to agree. A `text` chip is a literal character
 * and goes through the text endpoint instead - the same path the composer
 * uses, so it needs no server change and does not widen the allowlist, whose
 * whole job is keeping arbitrary strings out of send-keys.
 */
const KEYS: { k: string; label: string; text?: boolean; danger?: boolean }[] = [
  { k: 'Escape', label: 'esc' },
  { k: 'Enter', label: '⏎' },
  { k: 'Tab', label: 'tab' },
  // Both are one modifier layer deep on the iOS and Android keyboards, which
  // is the cost this row exists to remove.
  { k: '$', label: '$', text: true },
  { k: '/', label: '/', text: true },
  // The only way to erase a pane's input from the phone. The composer is a
  // local draft, not a view of the agent's input line, so backspacing there
  // edits your own text and leaves the agent's alone. ^U is the one that
  // answers the common case - accept a suggestion with tab, change your mind,
  // clear the line in one tap. Neither is `danger`; they touch the input line,
  // not the process, and ^C stays the only red chip.
  { k: 'BSpace', label: '⌫' },
  { k: 'C-u', label: '^U' },
  { k: 'BTab', label: '⇧tab' },
  { k: 'Up', label: '↑' },
  { k: 'Down', label: '↓' },
  { k: 'Left', label: '←' },
  { k: 'Right', label: '→' },
  { k: '1', label: '1' },
  { k: '2', label: '2' },
  { k: '3', label: '3' },
  { k: 'y', label: 'y' },
  { k: 'n', label: 'n' },
  { k: 'C-c', label: '^C', danger: true },
]

export function KeyPad({
  onKey,
  onText,
  disabled,
}: {
  onKey: (k: string) => void
  onText: (t: string) => void
  disabled: boolean
}) {
  return (
    <div className="keypad">
      {KEYS.map((key) => (
        <button
          key={key.k}
          className={`key ${key.danger ? 'danger' : ''}`}
          disabled={disabled}
          onClick={() => (key.text ? onText(key.k) : onKey(key.k))}
        >
          {key.label}
        </button>
      ))}
    </div>
  )
}
