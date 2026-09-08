import type { Pane } from '../types'
import { paneTitle } from '../types'
import { PaneRow } from './PaneRow'

/**
 * A flat pane list grouped by session, with "Needs you" pinned on top.
 *
 * That group only appears when something is actually waiting, and it is driven
 * by the same classifier that fires the push notification - so the app and the
 * lock screen never disagree about what needs an answer.
 */
export function PaneList({
  panes,
  current,
  filter,
  starred,
  onOpen,
  onMenu,
  onStar,
}: {
  panes: Pane[]
  current: string | null
  filter: string
  starred: string[]
  onOpen: (p: Pane) => void
  onMenu: (p: Pane) => void
  onStar: (p: Pane) => void
}) {
  const f = filter.trim().toLowerCase()
  const shown = f
    ? panes.filter((p) =>
        `${paneTitle(p)} ${p.sessionName} ${p.windowName} ${p.command} ${p.windowIndex}`
          .toLowerCase()
          .includes(f),
      )
    : panes

  if (!shown.length) {
    return (
      <div className="list">
        <div className="empty">
          {panes.length ? <>No pane matches “{filter}”.</> : <>No panes yet.</>}
        </div>
      </div>
    )
  }

  // Each pane belongs to exactly one group, and the tiers are exclusive going
  // down: waiting outranks a star, because urgency beats a bookmark and the
  // drawer must not disagree with the push notification about what needs an
  // answer.
  const isStarred = (p: Pane) => starred.includes(p.id)
  const waiting = shown.filter((p) => p.status === 'waiting')
  const stars = shown.filter((p) => p.status !== 'waiting' && isStarred(p))

  // Session order follows the tree, so the drawer matches the workspace.
  const sessions: string[] = []
  for (const p of shown) if (!sessions.includes(p.sessionName)) sessions.push(p.sessionName)

  return (
    <div className="list">
      {waiting.length > 0 && (
        <>
          <div className="group alert">
            <span className="dot waiting" />
            Needs you
          </div>
          {waiting.map((p) => (
            <PaneRow
              key={'w' + p.id}
              pane={p}
              alert
              active={p.id === current}
              starred={isStarred(p)}
              onOpen={() => onOpen(p)}
              onMenu={() => onMenu(p)}
              onStar={() => onStar(p)}
            />
          ))}
        </>
      )}

      {stars.length > 0 && (
        <>
          <div className="group">★ Starred</div>
          {stars.map((p) => (
            <PaneRow
              key={'s' + p.id}
              pane={p}
              alert={false}
              active={p.id === current}
              starred
              onOpen={() => onOpen(p)}
              onMenu={() => onMenu(p)}
              onStar={() => onStar(p)}
            />
          ))}
        </>
      )}

      {sessions.map((name) => {
        const rows = shown.filter(
          (p) => p.sessionName === name && p.status !== 'waiting' && !isStarred(p),
        )
        if (!rows.length) return null
        return (
          <div key={name}>
            <div className="group">{name}</div>
            {rows.map((p) => (
              <PaneRow
                key={p.id}
                pane={p}
                alert={false}
                active={p.id === current}
                starred={false}
                onOpen={() => onOpen(p)}
                onMenu={() => onMenu(p)}
                onStar={() => onStar(p)}
              />
            ))}
          </div>
        )
      })}
    </div>
  )
}
