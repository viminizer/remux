import { useEffect, useState } from 'react'

/**
 * Names one pane.
 *
 * The field holds only the name the user has given it, never the program's own
 * title - so pre-filling does not silently pin whatever an agent happened to be
 * printing at the time. The placeholder shows what the row falls back to, and
 * submitting an empty field returns it to that.
 */
export function RenameSheet({
  open,
  current,
  placeholder,
  what,
  onClose,
  onRename,
}: {
  open: boolean
  current: string
  placeholder?: string
  what: string
  onClose: () => void
  onRename: (name: string) => void
}) {
  const [name, setName] = useState(current)
  useEffect(() => {
    if (open) setName(current)
  }, [open, current])

  const typed = name.trim()

  return (
    <div className={`sheet ${open ? 'on' : ''}`}>
      <div className="grip" />
      <div className="sheet-body">
        <h3>Rename {what}</h3>
        <div className="field">
          <label>Name</label>
          <input
            value={name}
            placeholder={placeholder}
            onChange={(e) => setName(e.target.value)}
            spellCheck={false}
            autoCapitalize="off"
          />
        </div>
        <button className="go" onClick={() => onRename(typed)}>
          {typed ? 'Rename' : 'Use the default name'}
        </button>
        <button className="cancel" onClick={onClose}>
          Cancel
        </button>
      </div>
    </div>
  )
}
