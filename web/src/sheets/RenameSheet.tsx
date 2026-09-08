import { useEffect, useState } from 'react'

export function RenameSheet({
  open,
  current,
  what,
  onClose,
  onRename,
}: {
  open: boolean
  current: string
  what: string
  onClose: () => void
  onRename: (name: string) => void
}) {
  const [name, setName] = useState(current)
  useEffect(() => {
    if (open) setName(current)
  }, [open, current])

  return (
    <div className={`sheet ${open ? 'on' : ''}`}>
      <div className="grip" />
      <div className="sheet-body">
        <h3>Rename {what}</h3>
        <div className="field">
          <label>Name</label>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            spellCheck={false}
            autoCapitalize="off"
          />
        </div>
        <button className="go" onClick={() => onRename(name.trim())} disabled={!name.trim()}>
          Rename
        </button>
        <button className="cancel" onClick={onClose}>
          Cancel
        </button>
      </div>
    </div>
  )
}
