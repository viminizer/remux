import { useEffect, useState } from 'react'
import type { Session } from '../types'

/** Create a window in a session, or a whole new session. */
export function NewSheet({
  open,
  sessions,
  defaultSession,
  onClose,
  onCreate,
}: {
  open: boolean
  sessions: Session[]
  defaultSession: string | null
  onClose: () => void
  onCreate: (kind: 'window' | 'session', name: string, sessionId: string, path: string) => void
}) {
  const [kind, setKind] = useState<'window' | 'session'>('window')
  const [name, setName] = useState('')
  const [sessionId, setSessionId] = useState('')
  const [path, setPath] = useState('')

  useEffect(() => {
    if (open) {
      setSessionId(defaultSession ?? sessions[0]?.id ?? '')
      setName('')
      setPath('')
      if (!sessions.length) setKind('session')
    }
  }, [open, defaultSession, sessions])

  return (
    <div className={`sheet ${open ? 'on' : ''}`}>
      <div className="grip" />
      <div className="sheet-body">
        <h3>Create</h3>

        <div className="seg">
          <button className={kind === 'window' ? 'on' : ''} onClick={() => setKind('window')}>
            Window
          </button>
          <button className={kind === 'session' ? 'on' : ''} onClick={() => setKind('session')}>
            Session
          </button>
        </div>

        <div className="field">
          <label>Name</label>
          <input value={name} onChange={(e) => setName(e.target.value)} spellCheck={false} autoCapitalize="off" />
        </div>

        {kind === 'window' && (
          <div className="field">
            <label>In session</label>
            <select value={sessionId} onChange={(e) => setSessionId(e.target.value)}>
              {sessions.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </div>
        )}

        <div className="field">
          <label>Start directory</label>
          <input
            value={path}
            onChange={(e) => setPath(e.target.value)}
            placeholder="~/Desktop/github"
            spellCheck={false}
            autoCapitalize="off"
          />
        </div>

        <button className="go" onClick={() => onCreate(kind, name.trim(), sessionId, path.trim())}>
          Create
        </button>
        <button className="cancel" onClick={onClose}>
          Cancel
        </button>

        <p style={{ margin: '12px 0 0', fontSize: 11.5, color: 'var(--ov0)', lineHeight: 1.6 }}>
          Created with <code style={{ fontFamily: 'var(--mono)' }}>-d</code> so your laptop's active
          window doesn't jump.
        </p>
      </div>
    </div>
  )
}
