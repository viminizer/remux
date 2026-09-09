import { useCallback, useEffect, useState } from 'react'

/** Display and behaviour settings, persisted per device. */
export interface Settings {
  fontSize: number
  wrap: boolean
  lines: number
  notifyWaiting: boolean
  notifyDone: boolean
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
}

const DEFAULTS: Settings = {
  fontSize: 13,
  wrap: true,
  lines: 400,
  notifyWaiting: true,
  notifyDone: false,
  submitOnEnter: true,
  askedNotifications: false,
  starred: [],
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

export function saveSnapshot(s: Snapshot) {
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
