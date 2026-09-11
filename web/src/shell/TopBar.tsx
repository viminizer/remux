import type { Status } from '../types'
import { StatusDot } from '../components/StatusDot'

/**
 * The bar above an open pane.
 *
 * The line under the title reads session · project · agent, narrowing from
 * left to right: which workspace, which repo inside it, which tool. The
 * project is the middle term because it is the one that actually places you -
 * the saas session holds shortlist and remux, and 1yegabiz holds four repos
 * under one word, so the session alone does not answer "where am I".
 *
 * Passed as parts rather than one joined string so the project can be picked
 * out. It is the only one of the three that changes what you are looking at
 * rather than describing it.
 */
export function TopBar({
  title,
  session,
  project,
  cmd,
  status,
  stale,
  onBurger,
  onKebab,
}: {
  title: string
  session: string
  project?: string
  cmd: string
  status?: Status
  stale?: boolean
  onBurger: () => void
  onKebab: () => void
}) {
  return (
    <header className="topbar">
      <button className="iconbtn burger" onClick={onBurger} aria-label="Open panes">
        ☰
      </button>
      <div className="tb-title">
        <h1>
          <StatusDot status={status} stale={stale} />
          <span>{title}</span>
        </h1>
        {session && (
          <p>
            {session}
            {project && (
              <>
                {' · '}
                <span className="tb-project">{project}</span>
              </>
            )}
            {cmd && ` · ${cmd}`}
          </p>
        )}
      </div>
      <button className="iconbtn" onClick={onKebab} aria-label="Menu">
        ⋮
      </button>
    </header>
  )
}
