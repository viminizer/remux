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
  // The watchlist as the server last stated it.
  //
  // Watch and unwatch both answer with the new list, and that answer is newer
  // than the `watched` prop: the prop is derived from the poller's snapshot,
  // which is only rebuilt on the next poll. Reading the prop meant a tap left
  // the row looking exactly as it did before for several seconds.
  const [stated, setStated] = useState<Set<string> | null>(null)
  const on = stated ?? watched
  // Bumped by Try again, to re-run the fetch without changing the query.
  const [nonce, setNonce] = useState(0)

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
  }, [q, open, nonce])

  // Forget the stated list on close, so the next open reads the prop again.
  // Otherwise a repo dropped from the repo screen's menu would still show a ✓
  // here for as long as the app stays running.
  useEffect(() => {
    if (!open) setStated(null)
  }, [open])

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
    // One at a time. Both handlers rewrite the whole watchlist, so two taps in
    // flight together can lose one of the two changes.
    if (pending) return
    setPending(r.full)
    try {
      const res = on.has(r.full.toLowerCase())
        ? await api.githubUnwatch(r.full)
        : await api.githubWatch(r.full)
      setStated(new Set(res.repos.map((x) => x.toLowerCase())))
      onChanged()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setPending(null)
    }
  }

  const row = (r: PickerRepo) => {
    const added = on.has(r.full.toLowerCase())
    const busy = pending === r.full
    const pane = inPane.get(r.full)
    return (
      // While the change is in flight the row is dimmed and dead. It used to
      // stay fully lit and tappable, so there was nothing to say the tap had
      // landed.
      <button
        key={r.full}
        className={`pick ${added ? 'added' : ''} ${busy ? 'busy' : ''}`}
        disabled={busy}
        onClick={() => void toggle(r)}
      >
        <span className="av">{r.name.slice(0, 2).toLowerCase()}</span>
        <span className="mid">
          <span className="nm">{r.full}</span>
          <span className="sub">
            {pane
              ? `${pane.sessionName} · win ${pane.windowIndex}`
              : r.pushed
                ? `updated ${age(r.pushed)} ago`
                : 'no pane'}
            {busy ? (added ? ' · removing…' : ' · adding…') : added ? ' · watching' : ''}
          </span>
        </span>
        <span className="add">{busy ? <span className="wait" /> : added ? '✓' : '＋'}</span>
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

        {/* A failure and an empty result used to look the same here - both
            just stopped showing rows - so the sheet says which it is. */}
        {err && (
          <div className="loaderr">
            {err}
            <button className="retry" onClick={() => setNonce((n) => n + 1)}>
              Try again
            </button>
          </div>
        )}
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
        {repos?.length === 0 && (
          <div className="loading">
            {q ? `Nothing on GitHub matches "${q}".` : 'gh returned no repositories.'}
          </div>
        )}

        <button className="go" onClick={onClose}>
          Done
        </button>
      </div>
    </div>
  )
}
