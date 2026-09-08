import type { Status } from '../types'
import { StatusDot } from '../components/StatusDot'

export function TopBar({
  title,
  sub,
  status,
  stale,
  onBurger,
  onKebab,
}: {
  title: string
  sub: string
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
        <p>{sub}</p>
      </div>
      <button className="iconbtn" onClick={onKebab} aria-label="Menu">
        ⋮
      </button>
    </header>
  )
}
