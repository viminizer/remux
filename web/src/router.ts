import { useCallback, useEffect, useState } from 'react'

/**
 * Hash routing, deliberately shallow.
 *
 * `#/p/%14` is the only real route, plus `#/settings` and the two screens
 * hanging off it - the key pad and the naming journal. Anything deeper would
 * be navigation the drawer already does in one tap.
 *
 * The GitHub screen is the one place with depth, because a repo really does
 * contain issues and an issue really is a thing you open. Those routes carry
 * the repo as a single escaped `owner/name` so a link can be reloaded, and
 * their back behaviour is a stack held in App - see the ghStack note there.
 */
export type Route =
  | { name: 'pane'; pane: string | null }
  | { name: 'settings' }
  | { name: 'keypad' }
  | { name: 'naming' }
  | { name: 'gh' }
  | { name: 'ghRepo'; repo: string; tab: 'issues' | 'prs' }
  | { name: 'ghItem'; repo: string; number: number; kind: 'issue' | 'pr' }

/** True for the GitHub screen and anything reached from it. */
export function isGitHub(r: Route): boolean {
  return r.name === 'gh' || r.name === 'ghRepo' || r.name === 'ghItem'
}

function parse(hash: string): Route {
  const h = hash.replace(/^#/, '')
  if (h === '/settings') return { name: 'settings' }
  if (h === '/keypad') return { name: 'keypad' }
  if (h === '/naming') return { name: 'naming' }

  if (h === '/gh') return { name: 'gh' }

  const repo = /^\/gh\/r\/([^/?]+)(?:\?t=(issues|prs))?$/.exec(h)
  if (repo) {
    return {
      name: 'ghRepo',
      repo: decodeURIComponent(repo[1]),
      tab: repo[2] === 'prs' ? 'prs' : 'issues',
    }
  }

  const item = /^\/gh\/(i|pr)\/([^/]+)\/(\d+)$/.exec(h)
  if (item) {
    return {
      name: 'ghItem',
      repo: decodeURIComponent(item[2]),
      number: Number(item[3]),
      kind: item[1] === 'pr' ? 'pr' : 'issue',
    }
  }

  const m = /^\/p\/(.+)$/.exec(h)
  if (m) return { name: 'pane', pane: decodeURIComponent(m[1]) }
  return { name: 'pane', pane: null }
}

function href(r: Route): string {
  switch (r.name) {
    case 'settings':
      return '#/settings'
    case 'keypad':
      return '#/keypad'
    case 'naming':
      return '#/naming'
    case 'gh':
      return '#/gh'
    case 'ghRepo':
      return `#/gh/r/${encodeURIComponent(r.repo)}${r.tab === 'prs' ? '?t=prs' : ''}`
    case 'ghItem':
      return `#/gh/${r.kind === 'pr' ? 'pr' : 'i'}/${encodeURIComponent(r.repo)}/${r.number}`
    default:
      return r.pane ? `#/p/${encodeURIComponent(r.pane)}` : '#/'
  }
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
    const hash = href(r)
    if (location.hash !== hash) history.replaceState(null, '', hash)
    setRoute(r)
  }, [])

  return [route, go]
}
