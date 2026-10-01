/** One agent loop, as its tmux session options describe it. */
export interface Loop {
  name: string
  pane: string
  role: 'build' | 'review' | ''
  agent: 'claude' | 'codex' | ''
  repo: string
  slug: string
  state: 'idle' | 'working' | 'blocked' | ''
  since: number
  issue: number
  title: string
  scope: string
  instructions: string
  instrMode: 'add' | 'replace' | ''
  stop: string
  note: string
}

export interface Preset {
  name: string
  scope: string
  instructions: string
  instrMode: 'add' | 'replace'
}

export interface LoopStart {
  repo: string
  agents: ('claude' | 'codex')[]
  review: boolean
  scope: string
  instructions: string
  instrMode: 'add' | 'replace'
}

export interface HarnessRepo {
  path: string
  slug: string
}

export interface LoopsPayload {
  loops: Loop[]
  presets: Preset[]
  last: LoopStart | null
  repos: HarnessRepo[]
}

export interface HarnessItem {
  slug: string
  number: number
  kind: 'issue' | 'pr'
  title: string
  url: string
  why: 'stuck' | 'supervisor'
  question?: string
  options?: string[]
}

/** The dot colour for a loop: problems stand out, idle is calm. */
export function loopDot(l: Loop): string {
  if (l.state === 'blocked') return 'waiting'
  if (l.state === 'working') return 'working'
  return 'idle'
}

/** "14m", "2h", "3d" - how long the loop has been in its state. */
export function since(unix: number): string {
  if (!unix) return ''
  const s = Math.max(0, Date.now() / 1000 - unix)
  if (s < 60) return 'now'
  if (s < 3600) return `${Math.floor(s / 60)}m`
  if (s < 86400) return `${Math.floor(s / 3600)}h`
  return `${Math.floor(s / 86400)}d`
}

export function project(l: { repo: string; slug?: string }): string {
  return l.repo.split('/').filter(Boolean).pop() ?? l.slug ?? ''
}

export function itemURL(slug: string, n: number, kind: 'issue' | 'pr' = 'issue'): string {
  return `https://github.com/${slug}/${kind === 'pr' ? 'pull' : 'issues'}/${n}`
}
