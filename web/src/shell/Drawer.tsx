import { useState } from 'react'
import type { RefObject } from 'react'
import type { Conn, GhBadge, Pane } from '../types'
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

/** The one line under "GitHub", which has to say the most useful true thing. */
function ghSub(gh: GhBadge): string {
  if (gh.errorKind === 'auth' || gh.errorKind === 'nogh') return 'gh is not logged in'
  if (!gh.at) return 'reading…'

  const bits: string[] = []
  if (gh.assigned) bits.push(`${gh.assigned} assigned`)
  if (gh.red) bits.push(`${gh.red} check${gh.red === 1 ? '' : 's'} red`)
  if (!bits.length) bits.push(`${gh.repos} repo${gh.repos === 1 ? '' : 's'} · all clear`)
  if (gh.errorKind) bits.push('cached')
  return bits.join(' · ')
}

export function Drawer({
  open,
  panelRef,
  drag,
  panes,
  current,
  conn,
  starred,
  onOpen,
  onMenu,
  onStar,
  onNew,
  onSettings,
  gh,
  onGitHub,
}: {
  open: boolean
  panelRef: RefObject<HTMLElement>
  /** How far a swipe has pulled the drawer out, 0 to 1, or null when idle. */
  drag: number | null
  panes: Pane[]
  current: string | null
  conn: Conn
  starred: string[]
  onOpen: (p: Pane) => void
  onMenu: (p: Pane) => void
  onStar: (p: Pane) => void
  onNew: () => void
  onSettings: () => void
  /** Null until the first tick, or when the GitHub screen is switched off. */
  gh: GhBadge | null
  onGitHub: () => void
}) {
  const [q, setQ] = useState('')

  return (
    // While a finger is on it the drawer follows that finger, so the class
    // that animates it has to stand aside. On release the inline transform and
    // the class both go, and the transition runs from wherever it was left.
    <aside
      ref={panelRef}
      className={`drawer ${open ? 'on' : ''} ${drag !== null ? 'dragging' : ''}`}
      style={drag !== null ? { transform: `translateX(${(drag - 1) * 100}%)` } : undefined}
    >
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

      {/* The whole GitHub integration is this one row.
          A bottom tab bar would have cost about 56px of vertical space on
          every screen in the app, and pane output is the reason remux exists.
          A third icon in the top bar would have been cramped next to the
          burger and the kebab. A pinned row at the head of the drawer costs
          nothing anywhere else and is one tap from wherever you are. */}
      {gh && (
        <div className="pinned">
          <button className="pin-row" onClick={onGitHub}>
            <span className="glyph">◈</span>
            <span className="pin-mid">
              GitHub
              <span className="sub">{ghSub(gh)}</span>
            </span>
            {gh.count > 0 && (
              <span className={`badge ${gh.red > 0 ? 'red' : ''}`}>{gh.count}</span>
            )}
          </button>
        </div>
      )}

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
