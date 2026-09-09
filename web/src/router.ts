import { useCallback, useEffect, useState } from 'react'

/**
 * Hash routing, deliberately shallow.
 *
 * `#/p/%14` is the only real route, plus `#/settings` and the key pad screen
 * hanging off it. Anything deeper would be navigation the drawer already does
 * in one tap.
 */
export type Route =
  | { name: 'pane'; pane: string | null }
  | { name: 'settings' }
  | { name: 'keypad' }

function parse(hash: string): Route {
  const h = hash.replace(/^#/, '')
  if (h === '/settings') return { name: 'settings' }
  if (h === '/keypad') return { name: 'keypad' }
  const m = /^\/p\/(.+)$/.exec(h)
  if (m) return { name: 'pane', pane: decodeURIComponent(m[1]) }
  return { name: 'pane', pane: null }
}

export function useRoute(): [Route, (r: Route) => void] {
  const [route, setRoute] = useState<Route>(() => parse(location.hash))

  useEffect(() => {
    const on = () => setRoute(parse(location.hash))
    window.addEventListener('hashchange', on)
    return () => window.removeEventListener('hashchange', on)
  }, [])

  // go() never creates a history entry of its own. Entries come only from
  // overlays opening (see App.tsx), and this is what makes back predictable:
  //
  //   an overlay is open   -> back closes it
  //   otherwise            -> back returns to the previous pane
  //   nothing left         -> back leaves the app
  //
  // Opening a pane always happens from an overlay - the drawer, or the
  // long-press action sheet - and replacing here hands that overlay's entry to
  // the pane instead of stranding it. So the pane you came from stays exactly
  // one back press away, while an overlay dismissed without navigating leaves
  // nothing behind at all.
  //
  // Assigning location.hash, as this used to, pushed an entry per call - which
  // meant the redirects did too: the "no pane selected" auto-select and the
  // Settings back button each added a step that back had to walk through.
  //
  // replaceState fires no hashchange, so the route is set here directly.
  const go = useCallback((r: Route) => {
    const hash =
      r.name === 'settings'
        ? '#/settings'
        : r.name === 'keypad'
          ? '#/keypad'
          : r.pane
            ? `#/p/${encodeURIComponent(r.pane)}`
            : '#/'
    if (location.hash !== hash) history.replaceState(null, '', hash)
    setRoute(r)
  }, [])

  return [route, go]
}
