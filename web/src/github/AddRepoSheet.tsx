import { useEffect, useMemo, useState } from 'react'
import { api } from '../api'
import type { Pane } from '../types'
import type { PickerRepo } from './types'
import { age } from './types'
import { GhGroup } from './rows'

/**
 * Add a repo.
 *
 * It opens on repos already open in a pane, because that is almost always the
 * answer and it needs no typing. Below that come the ones Kevin owns or is a
 * member of. Typing switches to a search across all of GitHub, which is the
 * only way to add a repo he watches without belonging to - apache/shardingsphere
 * is his real case.
 */
export function AddRepoSheet({
  open,
  panes,
  watched,
  onClose,
  onChanged,
}: {
  open: boolean
  panes: Map<string, Pane>
  watched: Set<string>
  onClose: () => void
  onChanged: () => void
}) {
  const [q, setQ] = useState('')
  const [repos, setRepos] = useState<PickerRepo[] | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [pending, setPending] = useState<string | null>(null)

  // Debounced, because the search half of this runs on GitHub's search quota
  // - 30 requests a minute - and a keystroke is not a query.
  useEffect(() => {
    if (!open) return
    let cancelled = false
    const t = setTimeout(() => {
      setErr(null)
      api
        .githubPicker(q.trim())
        .then((r) => !cancelled && setRepos(r.repos))
        .catch((e) => !cancelled && setErr(e instanceof Error ? e.message : String(e)))
    }, q ? 350 : 0)
    return () => {
      cancelled = true
      clearTimeout(t)
    }
  }, [q, open])

  // Which repos have a pane open in them right now. The server matched each
  // pane's working directory to a repo, so this costs nothing extra here.
  const inPane = useMemo(() => {
    const byRepo = new Map<string, Pane>()
    for (const r of repos ?? []) {
      const p = (r.panes ?? []).map((id) => panes.get(id)).find((x): x is Pane => !!x)
      if (p) byRepo.set(r.full, p)
    }
    return byRepo
  }, [repos, panes])

  const toggle = async (r: PickerRepo) => {
    setPending(r.full)
    try {
      if (watched.has(r.full.toLowerCase())) {
        await api.githubUnwatch(r.full)
      } else {
        await api.githubWatch(r.full)
      }
      onChanged()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setPending(null)
    }
  }

  const row = (r: PickerRepo) => {
    const on = watched.has(r.full.toLowerCase())
    const pane = inPane.get(r.full)
    return (
      <button key={r.full} className={`pick ${on ? 'added' : ''}`} onClick={() => void toggle(r)}>
        <span className="av">{r.name.slice(0, 2).toLowerCase()}</span>
        <span className="mid">
          <span className="nm">{r.full}</span>
          <span className="sub">
            {pane
              ? `${pane.sessionName} · win ${pane.windowIndex}`
              : r.pushed
                ? `updated ${age(r.pushed)} ago`
                : 'no pane'}
            {on ? ' · watching' : ''}
          </span>
        </span>
        <span className="add">{pending === r.full ? '·' : on ? '✓' : '＋'}</span>
      </button>
    )
  }

  const withPane = (repos ?? []).filter((r) => inPane.has(r.full))
  const rest = (repos ?? []).filter((r) => !inPane.has(r.full))

  return (
    <div className={`sheet gh-sheet ${open ? 'on' : ''}`}>
      <div className="grip" />
      <div className="sheet-body">
        <h3>Add a repo</h3>
        <div className="search">
          <span style={{ color: 'var(--ov0)', fontSize: 13 }}>⌕</span>
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search all of GitHub"
            spellCheck={false}
            autoCorrect="off"
            autoCapitalize="off"
          />
        </div>

        {err && <div className="loaderr">{err}</div>}
        {repos === null && !err && <div className="loading">Loading…</div>}

        {withPane.length > 0 && (
          <>
            <GhGroup title="Open in a pane right now" n={withPane.length} />
            {withPane.map(row)}
          </>
        )}
        {rest.length > 0 && (
          <>
            <GhGroup title={q ? 'Search results' : 'Yours and your orgs'} n={rest.length} />
            {rest.map(row)}
          </>
        )}
        {repos?.length === 0 && <div className="loading">Nothing found.</div>}

        <button className="go" onClick={onClose}>
          Done
        </button>
      </div>
    </div>
  )
}
