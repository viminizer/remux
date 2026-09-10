/** Mirrors internal/github. Field names match the Go json tags exactly. */

export type Checks = '' | 'pass' | 'fail' | 'pending'

export interface Label {
  name: string
  color: string // 6 hex digits, no leading #
}

export interface Repo {
  full: string
  owner: string
  name: string
  description?: string
  private: boolean
  fork: boolean
  issues: number
  prs: number
  myPrs?: number
  redPrs?: number
  pushed?: string
  missing?: boolean
  panes?: string[]
}

export interface Comment {
  author: string
  body: string
  at: string
  trimmed?: boolean
}

export interface CheckRun {
  name: string
  state: Checks
  took?: string
  url?: string
}

export interface Issue {
  number: number
  title: string
  author: string
  assignees?: string[]
  labels?: Label[]
  comments: number
  created: string
  updated: string
  url: string
  body?: string
  threadComments?: Comment[]
}

export interface PR {
  number: number
  title: string
  author: string
  draft: boolean
  labels?: Label[]
  base: string
  head: string
  additions: number
  deletions: number
  checks?: Checks
  review?: string
  conflicts: boolean
  reviewers?: string[]
  updated: string
  url: string
  body?: string
  runs?: CheckRun[]
  threadComments?: Comment[]
}

export type InboxKind = 'issue' | 'pr' | 'mention'

export interface InboxItem {
  kind: InboxKind
  repo: string
  number: number
  title: string
  url: string
  updated: string
  labels?: Label[]
  draft?: boolean
  checks?: Checks
  review?: string
  conflicts?: boolean
  panes?: string[]
}

export interface Inbox {
  needsYou: InboxItem[]
  assigned: InboxItem[]
  yourPRs: InboxItem[]
  replies: number
}

/**
 * The whole screen at one instant.
 *
 * `error` rides alongside the data rather than replacing it. A failed poll
 * keeps the last good repos and inbox and only adds a banner, which is what
 * makes the offline case a dimmed list instead of an empty screen.
 */
export interface GitHubSnapshot {
  viewer?: string
  repos: Repo[]
  inbox: Inbox
  at: string
  error?: string
  errorKind?: string
  /**
   * Every repo checked out in a pane right now, mapped to those pane ids.
   * It covers repos outside the watchlist too, because the inbox spans them.
   */
  panes?: Record<string, string[]>
  /**
   * Set when the Mac refused to say which repo a pane is in. On macOS that is
   * the privacy control: ~/Desktop, ~/Documents and ~/Downloads are protected
   * and the background service has not been granted access to them.
   */
  panesBlocked?: boolean
}

export interface IssuePage {
  issues: Issue[]
  /** Cursor for Load more. Absent at the end of the list. */
  next?: string
}

export interface PickerRepo extends Repo {
  watched: boolean
}

export const emptySnapshot: GitHubSnapshot = {
  repos: [],
  inbox: { needsYou: [], assigned: [], yourPRs: [], replies: 0 },
  at: '',
}

/** Short relative age, the way every row in the mock stamps it: 3h, 4d, 5w. */
export function age(iso: string): string {
  const then = Date.parse(iso)
  if (!Number.isFinite(then)) return ''
  const s = Math.max(0, (Date.now() - then) / 1000)
  if (s < 60) return 'now'
  if (s < 3600) return `${Math.floor(s / 60)}m`
  if (s < 86400) return `${Math.floor(s / 3600)}h`
  if (s < 86400 * 7) return `${Math.floor(s / 86400)}d`
  if (s < 86400 * 30) return `${Math.floor(s / (86400 * 7))}w`
  if (s < 86400 * 365) return `${Math.floor(s / (86400 * 30))}mo`
  return `${Math.floor(s / (86400 * 365))}y`
}

/**
 * A label's own colour, dimmed to sit on the dark background.
 *
 * GitHub label colours are chosen against white and several of Kevin's are
 * near-white themselves, which on this palette reads as a bright dot with no
 * meaning. Dropping them to a fixed lightness keeps them distinguishable from
 * each other without any of them shouting.
 */
export function labelColor(hex: string): string {
  const h = /^[0-9a-fA-F]{6}$/.test(hex) ? hex : '6c7086'
  const r = parseInt(h.slice(0, 2), 16)
  const g = parseInt(h.slice(2, 4), 16)
  const b = parseInt(h.slice(4, 6), 16)
  const mix = (c: number) => Math.round(c * 0.72 + 40)
  return `rgb(${mix(r)}, ${mix(g)}, ${mix(b)})`
}
