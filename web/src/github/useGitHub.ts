import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '../api'
import type { GitHubSnapshot } from './types'
import { emptySnapshot } from './types'

const CACHE_KEY = 'remux.github'

/**
 * The last snapshot, kept so the screen has something to draw before the first
 * fetch lands and while the phone is offline.
 *
 * This is the same bargain the pane snapshot cache makes: stale and stamped
 * beats blank. The screen shows its age and a Retry, so nothing here can be
 * mistaken for live.
 */
function readCache(): GitHubSnapshot {
  try {
    const raw = localStorage.getItem(CACHE_KEY)
    return raw ? (JSON.parse(raw) as GitHubSnapshot) : emptySnapshot
  } catch {
    return emptySnapshot
  }
}

function writeCache(s: GitHubSnapshot) {
  try {
    localStorage.setItem(CACHE_KEY, JSON.stringify(s))
  } catch {
    // A full or disabled localStorage is not a reason to fail a screen.
  }
}

/**
 * Owns the GitHub snapshot for the app.
 *
 * The server polls once for everybody, so this never sets a timer of its own.
 * It refetches when the screen opens and when the badge on the WebSocket says
 * the server has newer data - which is the same tick the drawer badge uses, so
 * an open screen and a closed one cost the same.
 */
export function useGitHub(open: boolean, serverAt: number) {
  const [snap, setSnap] = useState<GitHubSnapshot>(readCache)
  const [loading, setLoading] = useState(false)
  const alive = useRef(true)

  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])

  const load = useCallback(async () => {
    try {
      const next = await api.github()
      if (!alive.current) return next
      setSnap(next)
      // Only a successful read is worth caching. Caching a failure would
      // replace good stale data with an empty screen that claims an age.
      if (next.at && !next.errorKind) writeCache(next)
      return next
    } catch {
      // Leave whatever is on screen. The connection dot already says the
      // server is unreachable, and a second banner adds nothing.
      return null
    }
  }, [])

  // Fetch when the screen opens, and again whenever the server's poll lands.
  useEffect(() => {
    if (!open) return
    void load()
  }, [open, serverAt, load])

  /**
   * The refresh button.
   *
   * Kicking the server's poller is asynchronous, so this waits for the
   * snapshot's timestamp to actually move rather than reporting success the
   * moment the POST returns. It gives up after a few seconds and shows
   * whatever arrived - a slow GitHub should not leave a spinner running.
   */
  const refresh = useCallback(async () => {
    setLoading(true)
    const before = snap.at
    try {
      await api.githubRefresh()
      for (let i = 0; i < 10; i++) {
        await new Promise((r) => setTimeout(r, 600))
        const next = await load()
        if (!alive.current) return
        if (next?.at && next.at !== before) break
      }
    } catch {
      // load() already left the previous data in place.
    } finally {
      if (alive.current) setLoading(false)
    }
  }, [snap.at, load])

  return { snap, loading, load, refresh }
}
