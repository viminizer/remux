import { useCallback, useEffect, useMemo, useState } from 'react'
import { api } from '../api'
import type { Pane } from '../types'
import type { Issue, PR, Repo } from './types'
import { EndNote, IssueRow, PRRow, ScopeNote } from './rows'

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
  onUnwatch: () => void
}) {
  const [owner, name] = repo.split('/')
  const open = (meta?.panes ?? []).map((id) => panes.get(id)).filter((p): p is Pane => !!p)
  const [menu, setMenu] = useState(false)

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
          <button
            className="danger"
            onClick={() => {
              setMenu(false)
              onUnwatch()
            }}
          >
            Stop watching this repo
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

  const load = useCallback(
    async (f: IssueFilter, after?: string) => {
      setBusy(true)
      setErr(null)
      try {
        const page = await api.githubIssues(repo, f, after)
        setIssues((prev) => (after ? [...(prev ?? []), ...page.issues] : page.issues))
        setNext(page.next)
      } catch (e) {
        setErr(e instanceof Error ? e.message : String(e))
      } finally {
        setBusy(false)
      }
    },
    [repo],
  )

  useEffect(() => {
    setIssues(null)
    setNext(undefined)
    void load(filter)
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
        {filter === 'mine' ? (
          <ScopeNote>
            Opened on <b>Yours</b> · assigned to you or opened by you.
            {total > shown && (
              <>
                {' '}
                The other <b>{Math.max(0, total - shown)}</b> open issues were never fetched.
              </>
            )}
          </ScopeNote>
        ) : (
          <ScopeNote>
            <b>All open</b> · sorted by recently updated, <b>30 per page</b>. Nothing loads
            until you ask for it.
          </ScopeNote>
        )}

        {err && <div className="loaderr">{err}</div>}
        {issues === null && !err && <div className="loading">Loading…</div>}

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
  const [prs, setPRs] = useState<PR[] | null>(null)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setPRs(null)
    setErr(null)
    api
      .githubPRs(repo)
      .then((r) => !cancelled && setPRs(r.prs))
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

  const chip = (key: PRFilter, label: string, n: number) => (
    <button className={`f ${filter === key ? 'on' : ''}`} onClick={() => setFilter(key)}>
      {label} <span className="n">{n}</span>
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
        <ScopeNote>
          A PR row answers <b>can this merge</b>, not just what it is: draft, checks, review
          decision, conflicts, branch, size.
        </ScopeNote>

        {err && <div className="loaderr">{err}</div>}
        {prs === null && !err && <div className="loading">Loading…</div>}

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

        {prs !== null && shown.length > 0 && (
          <EndNote>
            {counts.all} open PR{counts.all === 1 ? '' : 's'} in this repo, all on one page.
          </EndNote>
        )}
      </div>
    </>
  )
}
