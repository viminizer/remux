import { useCallback, useEffect, useState } from 'react'

/**
 * Hash routing, deliberately shallow.
 *
 * `#/p/%14` is the only real route, plus `#/settings`. Anything deeper would
 * be navigation the drawer already does in one tap.
 */
export type Route = { name: 'pane'; pane: string | null } | { name: 'settings' }

function parse(hash: string): Route {
  const h = hash.replace(/^#/, '')
  if (h === '/settings') return { name: 'settings' }
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

  const go = useCallback((r: Route) => {
    const hash = r.name === 'settings' ? '#/settings' : r.pane ? `#/p/${encodeURIComponent(r.pane)}` : '#/'
    if (location.hash !== hash) location.hash = hash
    else setRoute(r)
  }, [])

  return [route, go]
}
