import { useCallback, useEffect, useState } from 'react'

/** Display and behaviour settings, persisted per device. */
export interface Settings {
  fontSize: number
  wrap: boolean
  lines: number
  notifyWaiting: boolean
  notifyDone: boolean
  /** Push when one of your pull requests turns red, or a review is asked of you. */
  notifyCi: boolean
  /**
   * Check names that do not count as a failure, matched case-insensitively as
   * substrings.
   *
   * Server-side like the notify toggles, and for the same reason: the gh
   * client that applies it runs there. Vercel is the default - a failed
   * preview deploy turns the whole rollup red and parked two pull requests in
   * "Needs you" with nothing to do about them.
   */
  ignoreChecks: string[]
  /** Server-side, like the three above: the naming pass reads it there. */
  namePanes: boolean
  submitOnEnter: boolean
  /** Whether the opt-in bar has been answered. Asked once, never again. */
  askedNotifications: boolean
  /**
   * Pane ids starred to the top of the drawer.
   *
   * Device-local on purpose. internal/api/settings.go keeps the notification
   * toggles on the server because the watcher runs there and reads them, so a
   * switch that only wrote to localStorage would appear to work and change
   * nothing. Nothing on the server acts on a star - it is drawer ordering and
   * nothing else - so by that same rule it stays here. Widening
   * /api/settings the way the notify toggles work is the escalation if these
   * ever need to follow you between the phone and the laptop.
   */
  starred: string[]
  /**
   * Whether the key pad is expanded into its full grid.
   *
   * Kept here rather than in component state so it survives switching panes
   * and reloading - it is a preference about how you work, not a per-pane
   * mode, and having it reset on every pane open would be its own annoyance.
   * Device-local for the same reason `starred` is: nothing on the server acts
   * on it.
   */
  keypadOpen: boolean
  /**
   * Extra key pad chips, defined here rather than in the code.
   *
   * Device-local for the same reason `starred` and `keypadOpen` are: the
   * notification toggles live on the server because the watcher reads them,
   * and nothing on the server ever acts on a chip. /api/settings is the
   * escalation if these should follow you between the phone and the laptop.
   */
  chips: CustomChip[]
  /**
   * Built-in chips turned off, by their `k`.
   *
   * A hide-list rather than a show-list, so a chip added to CHIPS later
   * appears for everyone instead of only for someone who has never opened
   * this screen. The cost is that a `k` here is a string with no owner: if a
   * built-in is ever renamed or dropped, the stale entry hides nothing, which
   * is the harmless direction to fail in.
   *
   * Device-local, same as `chips`.
   */
  hiddenKeys: string[]
}

/**
 * A chip someone added themselves.
 *
 * Deliberately no key-name variant. A key chip goes through the keys endpoint,
 * which is gated by KeyAllowlist in internal/tmux/actions.go, so allowing one
 * here would mean either widening that allowlist - whose whole job is keeping
 * arbitrary strings out of send-keys - or shipping a chip that the server
 * refuses. Text and commands go through the same endpoint the composer uses
 * and need no server change at all.
 */
export interface CustomChip {
  /**
   * Stable id, so React keys survive reordering and editing.
   *
   * It is also the only identity a chip has. Neither `label` nor `text` is
   * unique and neither is meant to be: the common pair is one label on two
   * chips that send different things - `review` sending /review on Claude and
   * $review on Codex, each pinned with `on` - and the mirror case, two labels
   * sending the same string, is just as legal. Nothing may key off either.
   */
  id: string
  /** What the chip shows. Not unique - see `id`. */
  label: string
  /** What it sends. Not unique either. */
  text: string
  /**
   * Clear the input line first. A slash or dollar command only registers on an
   * empty line, so without this one typed into a half-written prompt silently
   * does nothing.
   */
  command: boolean
  /** Span two grid columns, for a label that will not fit an eighth. */
  wide: boolean
  /**
   * Which panes this chip is worth showing on.
   *
   * Stated as intent rather than as a list of kinds to hide, because that is
   * what someone picking on the key pad screen is actually deciding. KeyPad
   * turns it into the same hide-list the built-in chips use.
   *
   * `claude` and `codex` are separate answers because the two spell a skill
   * call differently - a slash against a dollar - so a chip written for one is
   * dead text on the other. `agent` is still there for a chip that suits any
   * of them.
   */
  on: 'all' | 'claude' | 'codex' | 'agent' | 'shell'
}

const DEFAULTS: Settings = {
  fontSize: 13,
  wrap: true,
  lines: 400,
  notifyWaiting: true,
  notifyDone: false,
  notifyCi: true,
  ignoreChecks: ['Vercel'],
  namePanes: true,
  submitOnEnter: true,
  askedNotifications: false,
  starred: [],
  chips: [],
  hiddenKeys: [],
  keypadOpen: false,
}

const KEY = 'remux.settings'

export function loadSettings(): Settings {
  try {
    const raw = localStorage.getItem(KEY)
    return raw ? { ...DEFAULTS, ...JSON.parse(raw) } : { ...DEFAULTS }
  } catch {
    return { ...DEFAULTS }
  }
}

export function saveSettings(s: Settings) {
  try {
    localStorage.setItem(KEY, JSON.stringify(s))
  } catch {
    // A private window with storage blocked is not a reason to break the app.
  }
}

export function useSettings() {
  const [settings, setSettings] = useState<Settings>(loadSettings)
  useEffect(() => {
    saveSettings(settings)
    document.documentElement.style.setProperty('--fs', settings.fontSize + 'px')
  }, [settings])
  // Stable identity: patch is a dependency of effects that prune settings
  // against live data, and a new function every render would re-run them on
  // every render.
  const patch = useCallback((p: Partial<Settings>) => setSettings((s) => ({ ...s, ...p })), [])
  return { settings, patch }
}

// ── offline snapshot cache ────────────────────────────────────────────────
//
// The last screen of every pane opened is kept, so a dead connection still
// shows the last thing the agent said. The hard rule is that stale content
// must never look live: the banner, the dimming and the disabled composer are
// all driven by `cachedAt` below.

export interface Snapshot {
  pane: string
  lines: string[]
  title: string
  sub: string
  at: number
}

const SNAP_KEY = 'remux.snapshots'
const MAX_SNAPSHOTS = 12

function readSnapshots(): Record<string, Snapshot> {
  try {
    return JSON.parse(localStorage.getItem(SNAP_KEY) ?? '{}')
  } catch {
    return {}
  }
}

// Unexported on purpose: queueSnapshot is the only way in, so the per-frame
// localStorage write this replaced cannot come back one import at a time.
function saveSnapshot(s: Snapshot) {
  try {
    const all = readSnapshots()
    all[s.pane] = s
    // Keep only the most recent panes so localStorage cannot grow without
    // bound on a device that has browsed a lot of work.
    const keys = Object.keys(all).sort((a, b) => all[b].at - all[a].at)
    for (const k of keys.slice(MAX_SNAPSHOTS)) delete all[k]
    localStorage.setItem(SNAP_KEY, JSON.stringify(all))
  } catch {
    // Quota or private mode. Losing the cache is acceptable; crashing is not.
  }
}

export function loadSnapshot(pane: string): Snapshot | null {
  return readSnapshots()[pane] ?? null
}

/**
 * How long the cached screen is allowed to lag behind the live one.
 *
 * saveSnapshot is not cheap: it parses the whole store, sorts its keys,
 * stringifies it again and writes it to localStorage, all synchronously on the
 * main thread. Calling it per frame meant doing that up to 2.5 times a second
 * while an agent was producing output, on a few hundred KB.
 *
 * The cache is there so a dead connection still shows the last thing the agent
 * said. It has never needed to be current to the frame - a few seconds stale is
 * the same screen to a person reading it after the fact.
 */
const SNAP_DEBOUNCE_MS = 3000

let pendingSnapshot: Snapshot | null = null
let snapshotTimer: ReturnType<typeof setTimeout> | null = null

/** queueSnapshot keeps the latest screen and writes it at most every few seconds. */
export function queueSnapshot(s: Snapshot) {
  pendingSnapshot = s
  if (snapshotTimer === null) {
    snapshotTimer = setTimeout(flushSnapshot, SNAP_DEBOUNCE_MS)
  }
}

/**
 * flushSnapshot writes whatever is queued right now.
 *
 * Called when the pane changes and when the app goes to the background, which
 * are the two moments the debounce could otherwise lose a screen - and the
 * second is the one that matters, because a phone whose screen goes off is
 * exactly when the cache starts being the only copy.
 */
export function flushSnapshot() {
  if (snapshotTimer !== null) {
    clearTimeout(snapshotTimer)
    snapshotTimer = null
  }
  if (!pendingSnapshot) return
  const s = pendingSnapshot
  pendingSnapshot = null
  saveSnapshot(s)
}

/** "4 min ago" - the highest-value line on the offline screen. */
export function ago(ts: number): string {
  const s = Math.max(0, Math.round((Date.now() - ts) / 1000))
  if (s < 10) return 'just now'
  if (s < 60) return `${s} sec ago`
  const m = Math.round(s / 60)
  if (m < 60) return `${m} min ago`
  const h = Math.round(m / 60)
  if (h < 24) return `${h} hr ago`
  return `${Math.round(h / 24)} d ago`
}
