import { useState } from 'react'
import type { Conn, Pane } from '../types'
import { PaneList } from './PaneList'

const CONN_DOT: Record<Conn, string> = {
  connecting: 'shell',
  live: 'idle',
  offline: 'stale',
  denied: 'waiting',
}

const CONN_TEXT: Record<Conn, string> = {
  connecting: 'Connecting',
  live: 'Connected',
  offline: 'Offline',
  denied: 'Not authorized',
}

export function Drawer({
  open,
  panes,
  current,
  conn,
  onOpen,
  onMenu,
  onNew,
  onSettings,
}: {
  open: boolean
  panes: Pane[]
  current: string | null
  conn: Conn
  onOpen: (p: Pane) => void
  onMenu: (p: Pane) => void
  onNew: () => void
  onSettings: () => void
}) {
  const [q, setQ] = useState('')

  return (
    <aside className={`drawer ${open ? 'on' : ''}`}>
      <div className="drawer-head">
        <div className="search">
          <span style={{ color: 'var(--ov0)', fontSize: 13 }}>⌕</span>
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search panes"
            spellCheck={false}
            autoCorrect="off"
            autoCapitalize="off"
          />
        </div>
        <button className="newbtn" onClick={onNew}>
          ✚ New
        </button>
      </div>

      <PaneList panes={panes} current={current} filter={q} onOpen={onOpen} onMenu={onMenu} />

      <div className="drawer-foot" onClick={onSettings}>
        <span className={`dot ${CONN_DOT[conn]}`} />
        <span className="grow">{CONN_TEXT[conn]} · Settings</span>
      </div>
    </aside>
  )
}
