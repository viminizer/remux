import { useRef } from 'react'
import type { Pane } from '../types'
import { displayCommand, paneTitle, statusLabel } from '../types'
import { StatusGlyph } from '../components/StatusGlyph'

/**
 * One row per pane.
 *
 * The pane is the unit and the session is the grouping; the window level is
 * not shown at all. See "Which levels the UI exposes" in docs/plan.md.
 *
 * So the subtitle is what the pane is running and how it is doing, and the
 * session comes from the group heading above the row. It used to read
 * "win 3 · zsh · shell", but a window index is an internal coordinate - not
 * something anyone named, and not a level this UI lets you act on.
 */
export function PaneRow({
  pane,
  active,
  alert,
  starred,
  onOpen,
  onMenu,
  onStar,
}: {
  pane: Pane
  active: boolean
  alert: boolean
  starred: boolean
  onOpen: () => void
  onMenu: () => void
  onStar: () => void
}) {
  const hold = useRef<{ timer?: number; x: number; y: number }>({ x: 0, y: 0 })

  const cancel = () => {
    clearTimeout(hold.current.timer)
    hold.current.timer = undefined
  }

  // The star is a sibling of the row, not a child. The row is itself a button,
  // and a button inside a button is invalid HTML that behaves differently in
  // every browser. Keeping them siblings also leaves the long-press handling
  // below untouched, which is fiddly and already tuned.
  return (
    <div className={`row-wrap ${active ? 'active' : ''}`}>
      <button
        className={`row ${alert ? 'alert' : ''} ${active ? 'active' : ''}`}
        onClick={onOpen}
        onContextMenu={(e) => {
          e.preventDefault()
          onMenu()
        }}
        onPointerDown={(e) => {
          hold.current = { x: e.clientX, y: e.clientY, timer: window.setTimeout(onMenu, 550) }
        }}
        // A flick down the pane list is a press that lasts well over 550ms. The
        // menu must only open if the finger stayed put.
        onPointerMove={(e) => {
          const h = hold.current
          if (h.timer && Math.hypot(e.clientX - h.x, e.clientY - h.y) > 10) cancel()
        }}
        onPointerUp={cancel}
        onPointerCancel={cancel}
        onPointerLeave={cancel}
      >
        <div className="row-t">
          <StatusGlyph pane={pane} />
          <span>{paneTitle(pane)}</span>
        </div>
        <div className="row-s">
          {displayCommand(pane.command)} · {statusLabel(pane.status)}
        </div>
      </button>
      <button
        className={`star ${starred ? 'on' : ''}`}
        aria-label={starred ? `Unstar ${paneTitle(pane)}` : `Star ${paneTitle(pane)}`}
        aria-pressed={starred}
        onClick={onStar}
      >
        {starred ? '★' : '☆'}
      </button>
    </div>
  )
}
