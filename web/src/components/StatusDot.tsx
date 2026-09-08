import type { Status } from '../types'
import { dotClass } from '../types'

export function StatusDot({ status, stale }: { status?: Status; stale?: boolean }) {
  // When the view is stale the dot goes grey rather than showing the pane's
  // last known state, because that state is exactly what can no longer be
  // trusted.
  return <span className={`dot ${stale ? 'stale' : dotClass(status)}`} />
}
