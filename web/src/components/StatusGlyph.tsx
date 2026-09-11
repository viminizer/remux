import type { Pane, Status } from '../types'
import { dotClass, statusGlyph, statusLabel } from '../types'

/**
 * The drawer's status mark: a character, not a dot.
 *
 * The dot is still right in the top bar, where one pane is in view and the
 * spinner on "working" is the clearest thing on screen. A list of twenty rows
 * is a different job - you are scanning for the two that are blocked - and a
 * shape is found faster down a column than a hue, especially between peach and
 * green. It is also the same vocabulary the tmux status line uses on the Mac,
 * so a pane reads the same in both places.
 *
 * The colour is kept: same classes as the dot, so the two stay in step.
 */
export function StatusGlyph({ pane, stale }: { pane: Pane; stale?: boolean }) {
  // The phone's own verdict first. It is computed per poll from a capture this
  // connection just took, where @remux_state is whatever the laptop last wrote
  // - fresher wins, and the option is the fallback for a pane this connection
  // has not classified yet.
  const status: Status | undefined = pane.status
  const glyph = statusGlyph(status) || (status ? '' : pane.remuxState) || ''

  // When the view is stale the mark goes grey rather than showing a state that
  // is exactly what can no longer be trusted.
  return (
    <span
      className={`glyph ${stale ? 'stale' : dotClass(status)}`}
      aria-label={statusLabel(status)}
    >
      {glyph}
    </span>
  )
}
