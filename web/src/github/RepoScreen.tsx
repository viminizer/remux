import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../api'
import type { Pane } from '../types'
import type { Issue, PR, Repo } from './types'
import { EndNote, IssueRow, PRRow } from './rows'
import { SkeletonRows } from './Skeleton'

type Tab = 'issues' | 'prs'
type IssueFilter = 'mine' | 'all'
type PRFilter = 'mine' | 'all' | 'review' | 'drafts'

/**
 * One repo.
 *
 * This is the screen that answers "there can be hundreds of issues". Two
 * things keep it usable: it opens on Yours, which turns 107 rows into 7; and
 * All pages 30 at a time behind an explicit button rather than infinite
 * scroll, which fires requests while the thumb is still moving.
 */
export function RepoScreen({
  repo,
  meta,
  viewer,
  tab,
  panes,
  onTab,
  onBack,
  onOpenIssue,
  onOpenPR,
  onOpenPane,
  onSendToPane,
  onUnwatch,
}: {
  repo: string
  meta?: Repo
  viewer?: string
  tab: Tab
  panes: Map<string, Pane>
  onTab: (t: Tab) => void
  onBack: () => void
  onOpenIssue: (n: number) => void
  onOpenPR: (n: number) => void
  onOpenPane: (p: Pane) => void
  onSendToPane: () => void
  onUnwatch: () => Promise<void>
}) {
  const [owner, name] = repo.split('/')
  const open = (meta?.panes ?? []).map((id) => panes.get(id)).filter((p): p is Pane => !!p)
  const [menu, setMenu] = useState(false)
  const [leaving, setLeaving] = useState(false)

  return (
    <div className="screen on gh-screen">
      <div className="screen-head">
        <button className="iconbtn" onClick={onBack} aria-label="Back">
          ←
        </button>
        <h2>
          {name}
          <small>
            {owner}
            {open.length
              ? ` · ${open[0].sessionName} · win ${open[0].windowIndex}`
              : ' · no pane'}
          </small>
        </h2>
        <button className="iconbtn" onClick={() => setMenu((v) => !v)} aria-label="Menu">
          ⋮
        </button>
      </div>

      {menu && (
        <div className="repo-menu">
          <button
            onClick={() => {
              setMenu(false)
              window.open(`https://github.com/${repo}`, '_blank', 'noopener')
            }}
          >
            Open in GitHub ↗
          </button>
          {/* The menu stays open and the item goes dim until the server has
              answered, rather than snapping shut on a tap whose result only
              shows up seconds later. */}
          <button
            className="danger"
            disabled={leaving}
            onClick={async () => {
              setLeaving(true)
              try {
                await onUnwatch()
                setMenu(false)
              } finally {
                setLeaving(false)
              }
            }}
          >
            {leaving ? 'Stopping…' : 'Stop watching this repo'}
          </button>
        </div>
      )}

      <div className="gh-seg">
        <button className={tab === 'issues' ? 'on' : ''} onClick={() => onTab('issues')}>
          Issues <span className="n">{meta?.issues ?? '·'}</span>
        </button>
        <button className={tab === 'prs' ? 'on' : ''} onClick={() => onTab('prs')}>
          PRs <span className="n">{meta?.prs ?? '·'}</span>
        </button>
      </div>

      {tab === 'issues' ? (
        <IssuesTab repo={repo} total={meta?.issues ?? 0} viewer={viewer} onOpen={onOpenIssue} />
      ) : (
        <PRsTab repo={repo} viewer={viewer} onOpen={onOpenPR} />
      )}

      <div className="actions">
        {open.length ? (
          <>
            <button className="act" onClick={onSendToPane}>
              Send to pane
            </button>
            <button className="act primary" onClick={() => onOpenPane(open[0])}>
              Open pane ↗
            </button>
          </>
        ) : (
          <>
            <button
              className="act"
              onClick={() => window.open(`https://github.com/${repo}`, '_blank', 'noopener')}
            >
              Open in GitHub ↗
            </button>
            <button className="act primary" onClick={onSendToPane}>
              Checkout in a pane
            </button>
          </>
        )}
      </div>
    </div>
  )
}

// ── issues ────────────────────────────────────────────────────────────────

function IssuesTab({
  repo,
  total,
  viewer,
  onOpen,
}: {
  repo: string
  total: number
  viewer?: string
  onOpen: (n: number) => void
}) {
  const [filter, setFilter] = useState<IssueFilter>('mine')
  const [issues, setIssues] = useState<Issue[] | null>(null)
  const [next, setNext] = useState<string | undefined>()
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  // Which request is still the one we want. Every reply reads this before it
  // touches state, and leaving a filter bumps it.
  //
  // The rest of the screen uses a `cancelled` flag scoped to its effect, but
  // that cannot work here: "Load 30 more" starts a request from a click, with
  // no effect around it. Tapping Yours, All, Yours faster than the requests
  // return is enough to land the middle reply last, on top of the list it does
  // not belong to.
  const seq = useRef(0)

  const load = useCallback(
    async (f: IssueFilter, after?: string) => {
      const mine = seq.current
      setBusy(true)
      setErr(null)
      try {
        const page = await api.githubIssues(repo, f, after)
        if (seq.current !== mine) return
        setIssues((prev) => (after ? [...(prev ?? []), ...page.issues] : page.issues))
        setNext(page.next)
      } catch (e) {
        if (seq.current !== mine) return
        setErr(e instanceof Error ? e.message : String(e))
      } finally {
        // The request that replaces this one owns the spinner from here.
        if (seq.current === mine) setBusy(false)
      }
    },
    [repo],
  )

  useEffect(() => {
    setIssues(null)
    setNext(undefined)
    void load(filter)
    return () => {
      seq.current++
    }
  }, [filter, load])

  const shown = issues?.length ?? 0

  return (
    <>
      <div className="filters">
        <button className={`f ${filter === 'mine' ? 'on' : ''}`} onClick={() => setFilter('mine')}>
          Yours
        </button>
        <button className={`f ${filter === 'all' ? 'on' : ''}`} onClick={() => setFilter('all')}>
          All <span className="n">{total}</span>
        </button>
      </div>

      <div className="screen-body">
        {err && <div className="loaderr">{err}</div>}
        {issues === null && !err && <SkeletonRows n={5} />}

        {issues?.map((i) => (
          <IssueRow key={i.number} issue={i} viewer={viewer} onOpen={() => onOpen(i.number)} />
        ))}

        {issues?.length === 0 && (
          <EndNote>
            {filter === 'mine' ? (
              <>
                Nothing here has your name on it.
                <br />
                Tap <b>All</b> above to browse the other {total}.
              </>
            ) : (
              'No open issues.'
            )}
          </EndNote>
        )}

        {next && (
          <button className="loadmore" disabled={busy} onClick={() => void load(filter, next)}>
            {busy ? 'Loading…' : 'Load 30 more'}
            <small>
              showing {shown} of {total}
            </small>
          </button>
        )}

        {issues !== null && issues.length > 0 && !next && filter === 'mine' && (
          <EndNote>
            That is the whole list.
            <br />
            Tap <b>All</b> above to browse the other {Math.max(0, total - shown)}.
          </EndNote>
        )}
      </div>
    </>
  )
}

// ── pull requests ─────────────────────────────────────────────────────────

/**
 * The last PR list fetched, so switching tabs does not buy it again.
 *
 * PRsTab is conditionally rendered, so tapping Issues unmounts it and tapping
 * PRs mounts a fresh one - and the fetch behind it is `gh pr list`, which
 * internal/github/prs.go measures at 6-15 s on a big repo. That was paid on
 * every toggle.
 *
 * One entry, not a map: you look at one repo at a time, so opening another is
 * the natural moment to drop it. The TTL matches the GitHub poller's own
 * interval - long enough to cover a person going back and forth between the two
 * tabs, short enough that a list left on screen does not go quietly stale.
 */
let prCache: { repo: string; prs: PR[]; at: number } | null = null
const PR_CACHE_MS = 60000

function cachedPRs(repo: string): PR[] | null {
  if (!prCache || prCache.repo !== repo) return null
  if (Date.now() - prCache.at > PR_CACHE_MS) return null
  return prCache.prs
}

/**
 * The whole PR list is fetched once and filtered here.
 *
 * A PR list is short - 22 is the largest across Kevin's repos - so holding all
 * of it makes every filter instant instead of a round trip, and the counts on
 * the chips are true rather than guesses.
 */
function PRsTab({
  repo,
  viewer,
  onOpen,
}: {
  repo: string
  viewer?: string
  onOpen: (n: number) => void
}) {
  const [filter, setFilter] = useState<PRFilter>('all')
  const [prs, setPRs] = useState<PR[] | null>(() => cachedPRs(repo))
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    const have = cachedPRs(repo)
    if (have) {
      setPRs(have)
      setErr(null)
      return
    }
    let cancelled = false
    setPRs(null)
    setErr(null)
    api
      .githubPRs(repo)
      .then((r) => {
        // Inside the guard, not beside it. A slow fetch for a repo you have
        // already navigated away from would otherwise land last and overwrite
        // the entry for the repo actually on screen, leaving that one to pay
        // the 6-15 s again on the very next toggle.
        if (cancelled) return
        prCache = { repo, prs: r.prs, at: Date.now() }
        setPRs(r.prs)
      })
      .catch((e) => !cancelled && setErr(e instanceof Error ? e.message : String(e)))
    return () => {
      cancelled = true
    }
  }, [repo])

  const counts = useMemo(() => {
    const all = prs ?? []
    return {
      all: all.length,
      mine: all.filter((p) => viewer && p.author === viewer).length,
      review: all.filter((p) => viewer && (p.reviewers ?? []).includes(viewer)).length,
      drafts: all.filter((p) => p.draft).length,
    }
  }, [prs, viewer])

  const shown = useMemo(() => {
    const all = prs ?? []
    switch (filter) {
      case 'mine':
        return all.filter((p) => viewer && p.author === viewer)
      case 'review':
        return all.filter((p) => viewer && (p.reviewers ?? []).includes(viewer))
      case 'drafts':
        return all.filter((p) => p.draft)
      default:
        return all
    }
  }, [prs, filter, viewer])

  // Every count is 0 until the list lands, and "Needs your review 0" is a
  // statement, not a placeholder. A dot says the same thing the repo header's
  // counts say while they wait.
  const chip = (key: PRFilter, label: string, n: number) => (
    <button className={`f ${filter === key ? 'on' : ''}`} onClick={() => setFilter(key)}>
      {label} <span className="n">{prs === null ? '·' : n}</span>
    </button>
  )

  return (
    <>
      <div className="filters">
        {chip('mine', 'Yours', counts.mine)}
        {chip('all', 'All', counts.all)}
        {chip('review', 'Needs your review', counts.review)}
        {chip('drafts', 'Drafts', counts.drafts)}
      </div>

      <div className="screen-body">
        {err && <div className="loaderr">{err}</div>}
        {prs === null && !err && <SkeletonRows n={4} />}

        {shown.map((p) => (
          <PRRow key={p.number} pr={p} viewer={viewer} onOpen={() => onOpen(p.number)} />
        ))}

        {prs !== null && shown.length === 0 && (
          <div className="msg">
            <div className="glyph" style={{ color: 'var(--ov1)' }}>
              ⑂
            </div>
            <h2>Nothing here</h2>
            <p>
              {filter === 'all'
                ? 'No open pull requests in this repo.'
                : filter === 'review'
                  ? 'Nobody is waiting on your review here.'
                  : filter === 'mine'
                    ? 'You have no open pull requests in this repo.'
                    : 'No drafts.'}
            </p>
          </div>
        )}

      </div>
    </>
  )
}
