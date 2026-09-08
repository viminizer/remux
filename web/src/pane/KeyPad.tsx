/**
 * One scrollable row of key chips above the composer.
 *
 * These are the keys an agent's menus actually need, always reachable with a
 * thumb. The server refuses anything outside its allowlist, so this list and
 * that one have to agree.
 */
const KEYS: { k: string; label: string; danger?: boolean }[] = [
  { k: 'Escape', label: 'esc' },
  { k: 'Enter', label: '⏎' },
  { k: 'Tab', label: 'tab' },
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

export function KeyPad({ onKey, disabled }: { onKey: (k: string) => void; disabled: boolean }) {
  return (
    <div className="keypad">
      {KEYS.map((key) => (
        <button
          key={key.k}
          className={`key ${key.danger ? 'danger' : ''}`}
          disabled={disabled}
          onClick={() => onKey(key.k)}
        >
          {key.label}
        </button>
      ))}
    </div>
  )
}
