export type Status = 'waiting' | 'busy' | 'idle' | 'shell' | 'unknown'

export interface Pane {
  id: string
  index: number
  title: string
  /** A name the user gave this pane from the phone. Wins over `title`. */
  remuxTitle?: string
  /** What remux read the pane to be working on. See paneTitle. */
  remuxTask?: string
  /**
   * The one-glyph verdict remux wrote onto the pane, for the laptop's own tmux
   * status line to render. The drawer uses `status` instead - same
   * classification, richer type - so this is here to mirror the wire format
   * rather than to be displayed.
   */
  remuxState?: string
  command: string
  path: string
  active: boolean
  width: number
  height: number
  inMode: boolean
  alt: boolean
  dead: boolean
  history: number
  status?: Status
  preview?: string
  sessionId: string
  sessionName: string
  windowId: string
  windowIndex: number
  windowName: string
}

export interface Window {
  id: string
  index: number
  name: string
  active: boolean
  panes: Pane[]
}

export interface Session {
  id: string
  name: string
  attached: boolean
  windows: Window[]
}

export interface Tree {
  sessions: Session[]
}

export interface Health {
  ok: boolean
  version: string
  hostname: string
  tmux: string
  tmuxRunning: boolean
  panes: number
  uptimeSec: number
  login: string
  time: number
}

export interface SnapMeta {
  cmd: string
  title: string
  w: number
  h: number
  inMode: boolean
  alt: boolean
  status: Status
}

/**
 * The notification toggles live on the server, because the watcher that acts
 * on them runs there. Keeping them only on the phone would give a switch that
 * looks like it works and changes nothing.
 */
export interface NotifySettings {
  notifyWaiting: boolean
  notifyDone: boolean
  /** Fires when one of your pull requests turns red, or a review is asked of you. */
  notifyCi: boolean
  /** Lets a cheap model name each agent pane by what it is working on. */
  namePanes: boolean
  /** The watchlist, read-only here: the GitHub screen owns editing it. */
  repos?: string[]
  /**
   * Check names that do not count as a failure, matched case-insensitively
   * as substrings. Vercel is here by default: a failed preview deploy turns
   * the whole rollup red, which parked two of Kevin's pull requests in
   * "Needs you" with nothing he could do about it.
   */
  ignoreChecks?: string[]
}

/**
 * The GitHub line in the drawer, pushed on the same tick as the tree.
 *
 * It is a summary and not the screen: everything here fits on one row, and
 * the full snapshot is only fetched when somebody actually opens it.
 */
export interface GhBadge {
  t: 'gh'
  count: number
  assigned: number
  red: number
  repos: number
  at: number
  errorKind?: string
}

/** Connection state shown as a dot in the top bar and the drawer footer. */
export type Conn = 'connecting' | 'live' | 'offline' | 'denied'

/** Flattened pane list is what the drawer actually renders. */
export function flatten(tree: Tree): Pane[] {
  const out: Pane[] = []
  for (const s of tree.sessions) for (const w of s.windows) out.push(...w.panes)
  return out
}

/**
 * The row title prefers pane_title when it says something.
 *
 * Claude Code writes the live task there (e.g. "✳ Separate worktree"), which
 * is what makes the drawer read like a list of conversations rather than a
 * list of processes. It falls back to the window name when the title is just
 * a hostname, a path, or the command repeated back.
 */
export function paneTitle(p: Pane): string {
  // A name the user typed always wins. pane_title is the program's to rewrite,
  // and agents rewrite it constantly, so it can never hold a user's name.
  const mine = (p.remuxTitle || '').trim()
  if (mine) return mine

  // Then the task remux read off the screen. It comes second, not first,
  // because it is written by a model every ninety seconds and his own name is
  // not - and it comes before pane_title because it is the one name written to
  // the same rule for every pane, which is what makes twenty of them scannable.
  const task = (p.remuxTask || '').trim()
  if (task) return task

  const t = (p.title || '').trim()
  const meaningless =
    !t ||
    t === p.command ||
    t.startsWith('/') ||
    t.startsWith(':/') ||
    /^[\w-]+\.local$/.test(t) ||
    t === p.windowName
  return meaningless ? p.windowName || p.id : t
}

/**
 * Claude Code reports pane_current_command as its own version number, e.g.
 * "2.1.263". That is not a word anyone wants to read in "Message 2.1.263…" or
 * in a drawer subtitle, so it is mapped back to the agent's name.
 */
export function displayCommand(cmd: string): string {
  return /^\d+\.\d+\.\d+$/.test(cmd) ? 'claude' : cmd
}

/**
 * Whether a pane is running a coding agent.
 *
 * Mirrors agent.IsAgent on the server. It decides how an action is offered,
 * not whether it is allowed: since #23 the server splits an agent's pane like
 * any other, and this only chooses between a plain button and a hold. Being
 * wrong here costs a gesture, never a refusal. displayCommand does the work
 * for Claude Code, whose pane command is its version number.
 */
export function isAgent(cmd: string): boolean {
  return ['codex', 'claude', 'aider', 'opencode', 'crush'].includes(displayCommand(cmd))
}

/**
 * Mirrors agent.IsShell on the server, whose list is the authority - this one
 * was short two entries (tcsh, csh) while it lived in Composer.tsx.
 */
const SHELLS = new Set([
  'zsh', 'bash', 'fish', 'sh', 'dash', 'ksh', 'tcsh', 'csh', 'nu',
])

export function isShell(cmd: string): boolean {
  return SHELLS.has(displayCommand(cmd))
}

/**
 * What kind of thing a pane is running, for deciding which key chips are worth
 * showing on it.
 *
 * Claude Code and Codex are named separately rather than lumped into `agent`,
 * because the thing chips are most often for is calling a skill and the two
 * spell that differently: Claude takes a slash, Codex takes a dollar. A chip
 * that is right on one is dead text on the other, so "agents" is not a fine
 * enough answer to the question "where does this belong".
 *
 * `agent` is what is left - aider, opencode, crush - and stays a kind rather
 * than being folded into `other`, because those do take a prompt, and a chip
 * meant for every agent should reach them. `other` is a real answer too, not a
 * fallback for failure: a pane running vim or python is none of the above, and
 * chips are chosen for it deliberately rather than by defaulting.
 */
export type PaneKind = 'claude' | 'codex' | 'agent' | 'shell' | 'other'

export function paneKind(cmd: string): PaneKind {
  const name = displayCommand(cmd)
  if (name === 'claude') return 'claude'
  if (name === 'codex') return 'codex'
  if (isAgent(cmd)) return 'agent'
  if (isShell(cmd)) return 'shell'
  return 'other'
}

export function statusLabel(s: Status | undefined): string {
  switch (s) {
    case 'waiting': return 'needs an answer'
    case 'busy': return 'working'
    case 'idle': return 'idle'
    case 'shell': return 'shell'
    default: return 'unknown'
  }
}

/** The mock's dot classes; 'busy' renders as the spinning "working" dot. */
/**
 * The glyph vocabulary, shared with the laptop.
 *
 * These are the same three characters remux writes into the @remux_state pane
 * option for the tmux status line, so a pane reads the same on the phone as it
 * does on the Mac. Only "!" is meant to catch the eye: across twenty agent
 * panes the costly question is not what each is doing, it is which of them is
 * waiting on an answer.
 *
 * A shell gets nothing, and neither does a pane nothing matched - a glyph
 * there would be a confident guess about a screen we could not read.
 */
export function statusGlyph(s: Status | undefined): string {
  if (s === 'waiting') return '!'
  if (s === 'busy') return '✳'
  if (s === 'idle') return '✓'
  return ''
}

export function dotClass(s: Status | undefined): string {
  if (s === 'busy') return 'working'
  if (s === 'waiting' || s === 'idle' || s === 'shell') return s
  return 'stale'
}
