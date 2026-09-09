import type { Pane } from '../types'
import { displayCommand, isAgent, paneTitle } from '../types'
import { HoldButton } from '../components/HoldButton'

/**
 * The action sheet a long-press on a drawer row opens.
 *
 * Every action here takes the pane as an argument rather than closing over the
 * pane being viewed. That distinction is the whole point: the ⋮ menu acts on
 * the open pane, but a long-press targets an arbitrary row that is usually not
 * the open one, and reusing those handlers would have killed the wrong pane.
 *
 * The pane's title leads the sheet for the same reason. A sheet is detached
 * from the row that opened it, and every destructive mistake here starts with
 * acting on a pane you did not mean.
 */
export function PaneActionsSheet({
  pane,
  open,
  starred,
  onClose,
  onOpen,
  onStar,
  onRename,
  onSplit,
  onInterrupt,
  onFocus,
  onKill,
}: {
  pane: Pane | null
  open: boolean
  starred: boolean
  onClose: () => void
  onOpen: () => void
  onStar: () => void
  onRename: () => void
  onSplit: (direction: 'right' | 'below') => void
  onInterrupt: () => void
  onFocus: () => void
  onKill: () => void
}) {
  return (
    <div className={`sheet ${open ? 'on' : ''}`}>
      <div className="grip" />
      <div className="sheet-body">
        {pane && (
          <>
            <h3>{paneTitle(pane)}</h3>
            <button className="mi" onClick={onOpen}>
              Open
            </button>
            <button className="mi" onClick={onStar}>
              {starred ? 'Unstar pane' : 'Star pane'}
            </button>
            <div className="msep" />
            <button className="mi" onClick={onRename}>
              Rename pane
            </button>
            {/* A split halves the pane it targets, so an agent's screen would
                reflow under it. The server refuses that outright; saying so
                here is better than offering an action that fails. */}
            {isAgent(pane.command) ? (
              <div className="mi muted" style={{ cursor: 'default' }}>
                Split pane
                <small>not while {displayCommand(pane.command)} is running</small>
              </div>
            ) : (
              <>
                <button className="mi" onClick={() => onSplit('right')}>
                  Split right
                </button>
                <button className="mi" onClick={() => onSplit('below')}>
                  Split below
                </button>
              </>
            )}
            <button className="mi" onClick={onInterrupt}>
              Interrupt <small>^C</small>
            </button>
            {/* The one action that moves the laptop's own cursor. */}
            <button className="mi" onClick={onFocus}>
              Focus on laptop
            </button>
            <div className="msep" />
            <HoldButton label={`Kill pane ${pane.id}`} onConfirm={onKill} />
          </>
        )}
        <button className="cancel" onClick={onClose}>
          Cancel
        </button>
      </div>
    </div>
  )
}
