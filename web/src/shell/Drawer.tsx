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
  starred,
  onOpen,
  onMenu,
  onStar,
  onNew,
  onSettings,
}: {
  open: boolean
  panes: Pane[]
  current: string | null
  conn: Conn
  starred: string[]
  onOpen: (p: Pane) => void
  onMenu: (p: Pane) => void
  onStar: (p: Pane) => void
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

      <PaneList
        panes={panes}
        current={current}
        filter={q}
        starred={starred}
        onOpen={onOpen}
        onMenu={onMenu}
        onStar={onStar}
      />

      {/* A real button, not a div with onClick. The div was not focusable, did
          not answer Enter or Space, and was announced as plain text - while the
          ✚ New button right above it behaved correctly, for no visible reason.

          Its only affordances were cursor:pointer and a hover background, and
          both are gated on hardware this app does not run on: a touch screen
          has no cursor and no hover, and the hover rule sits behind
          @media (hover:hover). On the device remux is built for there was no
          signal at all that it did anything.

          Splitting the status and the action is what makes it readable. The
          state stays dim on the left, the destination is brighter on the right
          with a gear and a chevron. It also stops "Connected · Settings"
          parsing as one status string, which is what made tapping the
          connection state navigate somewhere. */}
      <button className="drawer-foot" onClick={onSettings}>
        <span className={`dot ${CONN_DOT[conn]}`} />
        <span className="grow">{CONN_TEXT[conn]}</span>
        <span className="foot-settings">⚙ Settings ›</span>
      </button>
    </aside>
  )
}
