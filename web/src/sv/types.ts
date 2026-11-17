import type { HarnessRepo } from '../loops/types'

export type Phase = 'discussing' | 'planning' | 'review' | 'queued' | 'running' | 'blocked' | 'done' | 'stopped'

/** One supervisor task, as its status.json says. */
export interface SvTask {
  id: string
  repo: string
  agent: 'claude' | 'codex'
  goal: string
  phase: Phase
  summary?: string
  question?: string
  steps?: { title: string; state: 'next' | 'running' | 'done' | 'blocked'; note?: string }[]
  changes?: string[]
  evidence?: string[]
  areas?: string[]
  created: string
  updated: string
  ended?: string
}

export interface SvEvent {
  at: string
  kind: string
  text: string
  worker?: string
}

export interface SvMessage {
  at: string
  from: 'kevin' | 'supervisor'
  text: string
}

export interface SvWorker {
  id: string
  task: string
  n: number
  role: string
  agent: string
  model?: string
  readOnly: boolean
  why?: string
  state: 'running' | 'done' | 'failed' | 'limited' | 'stopped'
  started: string
  ended?: string
}

export interface SvList {
  tasks: SvTask[]
  repos: HarnessRepo[]
  limits: { PerRepo: number; Total: number; Workers: number }
}

export interface SvDetail {
  task: SvTask
  chat: SvMessage[]
  plan: string
  retro: string
  events: SvEvent[]
  workers: SvWorker[]
  /** tmux session name → pane id, for watching a session live. */
  panes: Record<string, string>
}

export const PHASE: Record<Phase, string> = {
  discussing: 'Discussing',
  planning: 'Writing the plan',
  review: 'Plan ready for you',
  queued: 'Waiting to start',
  running: 'Running',
  blocked: 'Needs you',
  done: 'Done',
  stopped: 'Stopped',
}

export function phaseDot(p: Phase): string {
  if (p === 'blocked' || p === 'review') return 'waiting'
  if (p === 'running' || p === 'planning') return 'working'
  if (p === 'stopped') return 'stale'
  return 'idle'
}

/** Whether a task waits on Kevin. */
export const needsYou = (t: SvTask) => t.phase === 'blocked' || t.phase === 'review'

export function ago(iso?: string): string {
  if (!iso) return ''
  const s = Math.max(0, (Date.now() - Date.parse(iso)) / 1000)
  if (s < 60) return 'now'
  if (s < 3600) return `${Math.floor(s / 60)}m`
  if (s < 86400) return `${Math.floor(s / 3600)}h`
  return `${Math.floor(s / 86400)}d`
}
