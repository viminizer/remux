import type { ReactNode } from 'react'
import type { Pane } from '../types'
import type { Checks, InboxItem, Issue, Label, PR } from './types'
import { age, labelColor } from './types'

/**
 * The chips a row carries are the whole point of the two row types.
 *
 * An issue row answers "what is this": labels, who it is assigned to, how much
 * discussion. A pull request row answers "can this merge": checks, review
 * decision, conflicts, size. Same shape, different vocabulary.
 */

export function LabelChip({ label }: { label: Label }) {
  return (
    <span className="chip">
      <span className="lbl-dot" style={{ background: labelColor(label.color) }} />
      {label.name}
    </span>
  )
}

export function ChecksChip({ checks }: { checks?: Checks }) {
  switch (checks) {
    case 'fail':
      return <span className="chip red">● checks failed</span>
    case 'pass':
      return <span className="chip green">✓ checks passed</span>
    case 'pending':
      return <span className="chip blue">● checks running</span>
    default:
      return null
  }
}

export function ReviewChip({ review }: { review?: string }) {
  switch (review) {
    case 'APPROVED':
      return <span className="chip green">approved</span>
    case 'CHANGES_REQUESTED':
      return <span className="chip peach">changes requested</span>
    case 'REVIEW_REQUIRED':
      return <span className="chip">review required</span>
    default:
      return null
  }
}

/**
 * The link back to tmux.
 *
 * A repo can have nine panes open in it - shortlist does - and nine chips
 * would bury the row. One chip names the first and counts the rest, and
 * tapping it opens that pane.
 */
export function PaneChip({
  panes,
  byId,
  onOpen,
}: {
  panes?: string[]
  byId: Map<string, Pane>
  onOpen?: (pane: Pane) => void
}) {
  const known = (panes ?? []).map((id) => byId.get(id)).filter((p): p is Pane => !!p)
  if (!known.length) return null

  const first = known[0]
  const more = known.length - 1
  return (
    <span
      className="chip pane"
      role="button"
      onClick={(e) => {
        e.stopPropagation()
        onOpen?.(first)
      }}
    >
      ↗ {first.sessionName} · win {first.windowIndex}
      {more > 0 ? ` +${more}` : ''}
    </span>
  )
}

/** The glyph at the head of a row, coloured by what the row is. */
export function Kind({ kind, red, draft }: { kind: 'issue' | 'pr' | 'mention'; red?: boolean; draft?: boolean }) {
  if (kind === 'mention') return <span className="kind men">@</span>
  if (kind === 'pr') {
    return <span className={`kind pr ${draft ? 'draft' : red ? 'red' : ''}`}>⑂</span>
  }
  return <span className="kind iss">◉</span>
}

/**
 * The row is a container, not a button, because it can hold two of them.
 *
 * A button inside a button is invalid HTML and behaves unpredictably on touch,
 * so the row itself is a div: the wide part that opens the item is one button,
 * and the mute is another beside it.
 */
function Row({
  kind,
  red,
  draft,
  top,
  title,
  chips,
  stamp,
  onClick,
  side,
}: {
  kind: 'issue' | 'pr' | 'mention'
  red?: boolean
  draft?: boolean
  top: ReactNode
  title: string
  chips: ReactNode
  stamp: string
  onClick?: () => void
  /** A trailing control - mute, or put back. Absent on most rows. */
  side?: ReactNode
}) {
  return (
    <div className="gh">
      <button className="gh-hit" onClick={onClick}>
        <Kind kind={kind} red={red} draft={draft} />
        <span className="mid">
          <span className="repo">{top}</span>
          <span className="ttl">{title}</span>
          <span className="meta">{chips}</span>
        </span>
        <span className="age">{stamp}</span>
      </button>
      {side}
    </div>
  )
}

/** One inbox row. The repo name is part of it, because the inbox spans repos. */
export function InboxRow({
  item,
  byId,
  onOpen,
  onOpenPane,
  onMute,
  onUnmute,
}: {
  item: InboxItem
  byId: Map<string, Pane>
  onOpen: () => void
  onOpenPane: (p: Pane) => void
  /** Dismiss this row until the item itself changes. */
  onMute?: () => void
  /** Put a dismissed row back. */
  onUnmute?: () => void
}) {
  const [owner, name] = item.repo.split('/')
  return (
    <Row
      side={
        onUnmute ? (
          <button className="gh-mute" onClick={onUnmute} aria-label="Put back">
            ↺
          </button>
        ) : onMute ? (
          <button className="gh-mute" onClick={onMute} aria-label="Mute">
            ✕
          </button>
        ) : undefined
      }
      kind={item.kind}
      red={item.checks === 'fail' || item.conflicts}
      draft={item.draft}
      top={
        <>
          {owner}/<b>{name}</b> · #{item.number}
        </>
      }
      title={item.title}
      stamp={age(item.updated)}
      onClick={onOpen}
      chips={
        <>
          {item.kind === 'mention' && <span className="chip mauve">mention</span>}
          {item.draft && <span className="chip">draft</span>}
          <ChecksChip checks={item.checks} />
          <ReviewChip review={item.review} />
          {item.conflicts && <span className="chip red">conflicts</span>}
          {(item.labels ?? []).slice(0, 2).map((l) => (
            <LabelChip key={l.name} label={l} />
          ))}
          <PaneChip panes={item.panes} byId={byId} onOpen={onOpenPane} />
        </>
      }
    />
  )
}

/**
 * One issue row inside a repo.
 *
 * The repo name is already in the header here, so the top line spends its
 * space on the number and on whether this one has your name on it - which is
 * the thing you are scanning a 108-row list for.
 */
export function IssueRow({
  issue,
  viewer,
  onOpen,
}: {
  issue: Issue
  viewer?: string
  onOpen: () => void
}) {
  const mine = !!viewer && (issue.assignees ?? []).includes(viewer)
  return (
    <Row
      kind="issue"
      top={
        <>
          #{issue.number}
          {mine ? ' · assigned to you' : ''}
        </>
      }
      title={issue.title}
      stamp={age(issue.updated)}
      onClick={onOpen}
      chips={
        <>
          {(issue.labels ?? []).slice(0, 3).map((l) => (
            <LabelChip key={l.name} label={l} />
          ))}
          {issue.comments > 0 && <span className="chip">💬 {issue.comments}</span>}
        </>
      }
    />
  )
}

/** One pull request row inside a repo: branch, checks, review, size. */
export function PRRow({ pr, viewer, onOpen }: { pr: PR; viewer?: string; onOpen: () => void }) {
  const who = viewer && pr.author === viewer ? 'you' : pr.author
  return (
    <Row
      kind="pr"
      red={pr.checks === 'fail' || pr.conflicts}
      draft={pr.draft}
      top={
        <>
          #{pr.number} · {who} · <b>{pr.head}</b> → {pr.base}
        </>
      }
      title={pr.title}
      stamp={age(pr.updated)}
      onClick={onOpen}
      chips={
        <>
          {pr.draft && <span className="chip">draft</span>}
          <ChecksChip checks={pr.checks} />
          <ReviewChip review={pr.review} />
          {pr.conflicts && <span className="chip red">conflicts</span>}
          <span className="chip">
            +{pr.additions} −{pr.deletions}
          </span>
        </>
      }
    />
  )
}

export function GhGroup({ title, n, hot }: { title: string; n?: number; hot?: boolean }) {
  return (
    <div className={`ghgroup ${hot ? 'hot' : ''}`}>
      {title}
      {n !== undefined && <span className="n">{n}</span>}
    </div>
  )
}

/** The end-of-list note every list in the mock closes with. */
export function EndNote({ children }: { children: ReactNode }) {
  return <div className="endnote">{children}</div>
}
