import type { Pane } from '../types'
import { displayCommand, paneTitle, statusLabel } from '../types'
import { StatusDot } from '../components/StatusDot'

/**
 * One row per pane.
 *
 * Windows stopped being a navigation level: the window name is the row's
 * subtitle instead, so tmux's three levels flatten into two of hierarchy and
 * any pane is one tap away from anywhere.
 */
export function PaneRow({
  pane,
  active,
  alert,
  onOpen,
  onMenu,
}: {
  pane: Pane
  active: boolean
  alert: boolean
  onOpen: () => void
  onMenu: () => void
}) {
  let holdTimer: number | undefined

  return (
    <button
      className={`row ${alert ? 'alert' : ''} ${active ? 'active' : ''}`}
      onClick={onOpen}
      onContextMenu={(e) => {
        e.preventDefault()
        onMenu()
      }}
      onPointerDown={() => {
        holdTimer = window.setTimeout(onMenu, 550)
      }}
      onPointerUp={() => clearTimeout(holdTimer)}
      onPointerLeave={() => clearTimeout(holdTimer)}
    >
      <div className="row-t">
        <StatusDot status={pane.status} />
        <span>{paneTitle(pane)}</span>
      </div>
      <div className="row-s">
        win {pane.windowIndex} · {displayCommand(pane.command)} · {statusLabel(pane.status)}
      </div>
    </button>
  )
}
