import { useEffect, useRef, useState } from 'react'

import { claim } from './tabSwipe'

/**
 * Swipe sideways to change tab, on the two screens that have tabs.
 *
 * The segmented control stays: this is the affordance for the hand already
 * holding the phone, not a replacement for the one you tap. Three taps on a
 * 44px target at the top of the screen were the only way to move between
 * Inbox and Repos, and the thumb is nowhere near the top.
 *
 * ## Sharing the axis with the drawer
 *
 * useDrawerSwipe listens at the app root and takes every rightward drag. This
 * hook listens on the screen, so it sees the same drag first, and where it
 * wants the gesture it stops the event before the root handler exists to see
 * it. Both commit to an axis at the same AXIS distance, so the drawer is still
 * `undecided` at the moment that happens and simply never decides.
 *
 * Where it does not want the gesture - a rightward drag on the first tab, a
 * vertical scroll, a drag that started on the filter chips - it lets the event
 * through untouched and the drawer behaves exactly as it did before. That is
 * the same rule mirror mode already follows, and it is what keeps the drawer
 * reachable from these screens: the drawer is how you leave them.
 *
 * ## Why the content does not follow the finger
 *
 * The drawer follows the finger because it is one panel sliding over another
 * and the gesture is the animation. A tab is a whole screen of rows that has
 * to exist twice to be dragged between, which on the repo screen means
 * fetching the pull requests to animate past them. The tab changes on release
 * and the incoming list slides in from the side it came from - the direction
 * is what the eye needs, and it costs one CSS keyframe.
 */

// Matches useDrawerSwipe. Both hooks must commit at the same distance, or the
// drawer decides on an earlier move than this one and takes the gesture.
const AXIS = 8
// How far a drag has to travel to count. A quarter of the screen is further
// than a tap slips and closer than a two-handed swipe.
const TRAVEL = 0.25
// A flick counts whatever distance it covered. Same units as useDrawerSwipe.
const FLICK = 0.35 // px/ms

export function useTabSwipe({
  index,
  count,
  onChange,
  enabled = true,
}: {
  /** Which tab is showing, 0-based. */
  index: number
  count: number
  onChange: (next: number) => void
  enabled?: boolean
}) {
  // A callback ref for the same reason useDrawerSwipe uses one: these screens
  // render a skeleton before the first snapshot arrives, so the element is
  // null on the first render and a plain ref would never see the real one.
  const [root, setRoot] = useState<HTMLElement | null>(null)

  // Read at pointerdown, so the listeners attach once and never close over a
  // stale tab index.
  const live = useRef({ index, count, onChange, enabled })
  live.current = { index, count, onChange, enabled }

  useEffect(() => {
    const el = root
    if (!el) return

    let mode: 'idle' | 'undecided' | 'drop' | 'swipe' = 'idle'
    let id = -1
    let x0 = 0
    let y0 = 0
    let want: 'prev' | 'next' = 'next'
    let lastX = 0
    let lastT = 0
    let vx = 0

    const down = (e: PointerEvent) => {
      // Touch only, like the drawer: a mouse drag is a text selection.
      if (e.pointerType !== 'touch' || !e.isPrimary || !live.current.enabled) return
      // A drag that starts on the filter chips belongs to the chips. Same
      // question useDrawerSwipe asks, and it has to be asked here too -
      // otherwise scrolling the chips changes tab underneath them.
      if (scrollsSideways(e.target, el)) return
      mode = 'undecided'
      id = e.pointerId
      x0 = lastX = e.clientX
      y0 = e.clientY
      lastT = e.timeStamp
      vx = 0
    }

    const move = (e: PointerEvent) => {
      if (e.pointerId !== id || mode === 'idle' || mode === 'drop') return
      const dx = e.clientX - x0
      const dy = e.clientY - y0

      if (mode === 'undecided') {
        if (Math.abs(dy) > Math.abs(dx) && Math.abs(dy) > AXIS) {
          mode = 'drop' // a scroll, and the browser is already doing it
          return
        }
        if (Math.abs(dx) < AXIS) return
        const c = claim(dx, live.current.index, live.current.count)
        if (c === 'pass') {
          // Nowhere to go this way. Leave the event alone so the drawer can
          // have it, and take no further part in this gesture.
          mode = 'drop'
          return
        }
        want = c
        mode = 'swipe'
      }

      const dt = e.timeStamp - lastT
      if (dt > 0) vx = (e.clientX - lastX) / dt
      lastX = e.clientX
      lastT = e.timeStamp

      // The drawer's root listener is still undecided at this point, and this
      // is what keeps it that way for the rest of the gesture.
      e.stopPropagation()
    }

    const up = (e: PointerEvent) => {
      if (e.pointerId !== id) return
      if (mode === 'swipe') {
        e.stopPropagation()
        const dx = e.clientX - x0
        const far = Math.abs(dx) > el.clientWidth * TRAVEL
        // A flick only counts in the direction the gesture committed to. A
        // drag out and back is a change of mind, not a flick.
        const flicked = Math.abs(vx) > FLICK && (vx < 0) === (want === 'next')
        if (far || flicked) {
          const { index: i, count: n, onChange } = live.current
          onChange(want === 'next' ? Math.min(i + 1, n - 1) : Math.max(i - 1, 0))
        }
        swallowClick(el)
      }
      mode = 'idle'
      id = -1
    }

    const cancel = () => {
      mode = 'idle'
      id = -1
    }

    el.addEventListener('pointerdown', down, { passive: true })
    el.addEventListener('pointermove', move, { passive: true })
    el.addEventListener('pointerup', up, { passive: true })
    el.addEventListener('pointercancel', cancel, { passive: true })
    return () => {
      el.removeEventListener('pointerdown', down)
      el.removeEventListener('pointermove', move)
      el.removeEventListener('pointerup', up)
      el.removeEventListener('pointercancel', cancel)
    }
  }, [root])

  return setRoot
}

/** True if the touch started inside something that scrolls sideways itself. */
function scrollsSideways(target: EventTarget | null, stop: HTMLElement): boolean {
  for (let n = target as Element | null; n instanceof HTMLElement; n = n.parentElement) {
    if (n === stop) return false
    if (n.scrollWidth > n.clientWidth) {
      const ox = getComputedStyle(n).overflowX
      if (ox === 'auto' || ox === 'scroll') return true
    }
  }
  return false
}

/**
 * A swipe that ends on a row would otherwise be followed by a click that opens
 * it - changing tab would also open an issue. Eat exactly one, and only if it
 * arrives. Same trick, and same reason, as useDrawerSwipe.
 */
function swallowClick(el: HTMLElement) {
  const eat = (e: Event) => {
    e.stopPropagation()
    e.preventDefault()
  }
  el.addEventListener('click', eat, { capture: true, once: true })
  setTimeout(() => el.removeEventListener('click', eat, true), 350)
}
