import { useEffect, useState } from 'react'

/**
 * The one place the desktop breakpoint is written down.
 *
 * It has to match the @media blocks in styles.css exactly, and it had already
 * drifted. The CSS gained `and (min-height:600px)` so that a wide, short
 * window - a phone in landscape - keeps the phone layout, while the
 * matchMedia call in App still asked for width alone. In that band the CSS
 * drew a burger over an overlay drawer while the script believed the drawer
 * was pinned and closed it on arrival.
 */
export const PINNED = '(min-width:900px) and (min-height:600px)'

/**
 * True while the drawer is pinned beside the content, so there is nothing to
 * open, close, or swipe.
 */
export function usePinned(): boolean {
  const [pinned, setPinned] = useState(() => window.matchMedia(PINNED).matches)
  useEffect(() => {
    const mq = window.matchMedia(PINNED)
    const sync = () => setPinned(mq.matches)
    sync()
    mq.addEventListener('change', sync)
    return () => mq.removeEventListener('change', sync)
  }, [])
  return pinned
}
