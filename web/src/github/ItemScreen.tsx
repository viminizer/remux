import { useEffect, useState } from 'react'
import { api } from '../api'
import type { Pane } from '../types'
import { displayCommand, dotClass, statusLabel } from '../types'
import type { Checks, Issue, PR } from './types'
import { age } from './types'
import { ChecksChip, LabelChip, ReviewChip } from './rows'

type Item = { kind: 'issue'; issue: Issue } | { kind: 'pr'; pr: PR }

/**
 * One issue or one pull request.
 *
 * Both share a head, a description and a thread; what differs is the question
 * being answered. An issue asks "what is this and is anyone on it", so it
 * leads with the pane already open in the repo. A pull request asks "can this
 * merge", so it leads with the checks.
 */
export function ItemScreen({
  repo,
  number,
  kind,
  panes,
  viewer,
  onBack,
  onOpenPane,
  onSendToPane,
}: {
  repo: string
  number: number
  kind: 'issue' | 'pr'
  panes: Pane[]
  viewer?: string
  onBack: () => void
  onOpenPane: (p: Pane) => void
  onSendToPane: () => void
}) {
  const [item, setItem] = useState<Item | null>(null)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setItem(null)
    setErr(null)
    const load =
      kind === 'pr'
        ? api.githubPR(repo, number).then((pr) => ({ kind: 'pr', pr }) as Item)
        : api.githubIssue(repo, number).then((issue) => ({ kind: 'issue', issue }) as Item)
    load
      .then((v) => !cancelled && setItem(v))
      .catch((e) => !cancelled && setErr(e instanceof Error ? e.message : String(e)))
    return () => {
      cancelled = true
    }
  }, [repo, number, kind])

  const url = `https://github.com/${repo}/${kind === 'pr' ? 'pull' : 'issues'}/${number}`
  const title = item
    ? item.kind === 'pr'
      ? item.pr.title
      : item.issue.title
    : `#${number}`
  const body = item ? (item.kind === 'pr' ? item.pr.body : item.issue.body) : ''
  const thread = item ? (item.kind === 'pr' ? item.pr.threadComments : item.issue.threadComments) : []

  return (
    <div className="screen on gh-screen">
      <div className="screen-head">
        <button className="iconbtn" onClick={onBack} aria-label="Back">
          ←
        </button>
        <h2>
          {kind === 'pr' ? 'Pull request' : 'Issue'}
          <small>{repo}</small>
        </h2>
        <button
          className="iconbtn"
          onClick={() => window.open(url, '_blank', 'noopener')}
          aria-label="Open in GitHub"
        >
          ↗
        </button>
      </div>

      <div className="screen-body">
        <div className="det-head">
          <div className="repo">{repo}</div>
          <h3>
            {title} <span className="num">#{number}</span>
          </h3>
          <div className="meta">
            {err ? (
              <span className="chip red">{err}</span>
            ) : !item ? (
              <span className="chip">loading…</span>
            ) : item.kind === 'pr' ? (
              <>
                <span className="chip green">open</span>
                {item.pr.draft && <span className="chip">draft</span>}
                <ChecksChip checks={item.pr.checks} />
                <ReviewChip review={item.pr.review} />
                {item.pr.conflicts && <span className="chip red">conflicts</span>}
                <span className="chip">
                  +{item.pr.additions} −{item.pr.deletions}
                </span>
                <span className="chip">
                  {item.pr.head} → {item.pr.base}
                </span>
              </>
            ) : (
              <>
                <span className="chip green">open</span>
                {(item.issue.labels ?? []).map((l) => (
                  <LabelChip key={l.name} label={l} />
                ))}
                {viewer && (item.issue.assignees ?? []).includes(viewer) && (
                  <span className="chip peach">assigned to you</span>
                )}
                {item.issue.comments > 0 && <span className="chip">💬 {item.issue.comments}</span>}
              </>
            )}
          </div>
        </div>

        {panes.length > 0 && <LiveInTmux panes={panes} onOpen={onOpenPane} />}

        {item?.kind === 'pr' && item.pr.runs && item.pr.runs.length > 0 && (
          <ChecksSection runs={item.pr.runs} />
        )}

        {body && (
          <div className="det-sec">
            <h4>Description</h4>
            <div className="body-md">
              {body
                .split(/\n{2,}/)
                .slice(0, 8)
                .map((p, i) => (
                  <p key={i}>{p}</p>
                ))}
            </div>
          </div>
        )}

        {thread && thread.length > 0 && (
          <div className="det-sec">
            <h4>Recent activity</h4>
            {thread.map((c, i) => (
              <div className="cmt" key={i}>
                <div className="av">{(c.author || '?').slice(0, 2)}</div>
                <div>
                  <div className="who">
                    {c.author || 'someone'} <span>{age(c.at)}</span>
                  </div>
                  <div className="txt">
                    {c.body}
                    {c.trimmed && <span className="c-dim"> …</span>}
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="actions">
        <button className="act" onClick={() => window.open(url, '_blank', 'noopener')}>
          Open in GitHub ↗
        </button>
        <button className="act primary" onClick={onSendToPane}>
          Send to pane
        </button>
      </div>
    </div>
  )
}

/**
 * The link back to the work already running.
 *
 * This is the whole reason the GitHub screen lives inside remux rather than
 * being a browser tab: the issue and the agent working on it are one tap
 * apart.
 */
function LiveInTmux({ panes, onOpen }: { panes: Pane[]; onOpen: (p: Pane) => void }) {
  const p = panes[0]
  return (
    <div className="det-sec live-tmux">
      <h4>Live in tmux</h4>
      <div className="live-row">
        <span className={`dot ${dotClass(p.status)}`} />
        <div className="live-mid">
          <div className="live-t">
            {displayCommand(p.command)} is {statusLabel(p.status)} in this repo
          </div>
          <div className="live-s">
            {p.sessionName} · win {p.windowIndex} · {p.path}
          </div>
        </div>
        <button className="chip pane" onClick={() => onOpen(p)}>
          Open ↗
        </button>
      </div>
      {panes.length > 1 && (
        <div className="live-more">
          {panes.length - 1} more pane{panes.length === 2 ? '' : 's'} in this repo
        </div>
      )}
    </div>
  )
}

/**
 * Checks, failures first and the green ones folded away.
 *
 * apache/shardingsphere runs 77 checks on a pull request. When two are red
 * they are the entire reason this screen was opened, and scrolling past 75
 * green lines to find them is not a phone interaction.
 */
function ChecksSection({ runs }: { runs: { name: string; state: Checks; took?: string; url?: string }[] }) {
  const [showAll, setShowAll] = useState(false)
  const interesting = runs.filter((r) => r.state !== 'pass')
  const passed = runs.length - interesting.length
  const shown = showAll ? runs : interesting

  return (
    <div className="det-sec">
      <h4>
        Checks · {runs.length}
      </h4>
      {shown.map((r, i) => (
        <div
          className="check"
          key={`${r.name}-${i}`}
          role={r.url ? 'button' : undefined}
          onClick={r.url ? () => window.open(r.url, '_blank', 'noopener') : undefined}
        >
          <span
            className={`dot ${r.state === 'fail' ? 'fail' : r.state === 'pending' ? 'working' : 'idle'}`}
          />
          <span className="nm">{r.name}</span>
          <span className="t">{r.took}</span>
        </div>
      ))}
      {passed > 0 && (
        <button className="check-more" onClick={() => setShowAll((v) => !v)}>
          {showAll ? 'Hide the green ones' : `${passed} passed`}
          <span className="arrow">{showAll ? '▴' : '▾'}</span>
        </button>
      )}
    </div>
  )
}
