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

  // A name Kevin typed wins the title, and that used to be the end of it - the
  // task remux read off the screen never appeared, so a label like "remux1"
  // chosen once went on masking the live work for good. Showing it here costs
  // nothing and loses nothing: the name he chose still leads the row.
  //
  // Only when it is actually masked. When the task *is* the title, repeating it
  // in the subtitle is noise.
  const task = (pane.remuxTask ?? '').trim()
  const masked = task !== '' && task !== paneTitle(pane)

  // The project goes in the subtitle rather than in front of the name.
  //
  // The session heading above already narrows things down, the row is about
  // two hundred pixels wide, and a prefix there would eat the words that
  // actually tell two panes apart. The laptop's status line does prefix the
  // name, and for the opposite reason: one pane is in view there with no
  // heading over it, so the project is the only thing saying where you are.
  const project = (pane.remuxProject ?? '').trim()

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
          {project && <span className="row-project"> · {project}</span>}
          {masked && <span className="row-task"> · {task}</span>}
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
