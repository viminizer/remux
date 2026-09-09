import { useEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'

/**
 * Swipe right to open the drawer, swipe left to close it.
 *
 * The drawer already slides on a transform, so the gesture is the affordance
 * that was missing rather than a new motion. It follows the finger and settles
 * on release, because snapping open at a fixed distance looks wrong against a
 * panel that animates.
 *
 * The interesting part is sharing a direction with mirror mode, where the
 * output pans sideways to read past column 80. The rule is the one a native
 * nested scroller uses: **the output owns the gesture until it is scrolled
 * fully left**, and only then does the next drag belong to the drawer. No edge
 * zone, no timing heuristic. An edge zone would be the worse version of the
 * same idea - it takes away the left edge, which is exactly where you start a
 * leftward pan when you are scrolled right.
 *
 * The cost is that the browser decides who owns a touch when it starts, so
 * there is no taking it back mid-drag. `.phone` is `touch-action: pan-y`,
 * which leaves vertical scrolling native and hands us every horizontal drag -
 * including, in mirror mode, the panning itself, driven here by writing
 * scrollLeft. That gives up momentum flick on the horizontal axis, which for a
 * terminal screen you are aligning by eye is no great loss.
 */

// How far the finger travels before the gesture commits to an axis. Below
// this a drag is still ambiguous and nothing moves.
const AXIS = 8
// Past this fraction of the drawer's width, release settles it open.
const SETTLE = 0.4
// A flick settles in its own direction whatever distance it covered.
const FLICK = 0.35 // px/ms

type Mode = 'idle' | 'undecided' | 'drop' | 'pan' | 'drawer'

export function useDrawerSwipe({
  open,
  setOpen,
  panel,
  enabled,
}: {
  open: boolean
  setOpen: (v: boolean) => void
  /** The drawer element, for its width. */
  panel: RefObject<HTMLElement>
  enabled: boolean
}) {
  // How far out the drawer is, 0 to 1, or null when no drag is in flight.
  // Everything else the gesture needs stays out of React: a drag updates 60
  // times a second and this is the only part a render has to see.
  const [drag, setDrag] = useState<number | null>(null)
  // A callback ref, not useRef: App returns a boot skeleton until the first
  // tree arrives, so the element these listeners belong on does not exist for
  // the first render. With a plain ref the effect ran once against null and
  // never again, and the gesture was silently dead. Same pattern as the
  // composer measurement above it.
  const [root, setRoot] = useState<HTMLElement | null>(null)

  // Read at pointerdown, so the listeners below can attach once and never see
  // a stale `open`.
  const live = useRef({ open, setOpen, enabled })
  live.current = { open, setOpen, enabled }

  useEffect(() => {
    const el = root
    if (!el) return

    let mode: Mode = 'idle'
    let id = -1
    let x0 = 0
    let y0 = 0
    let width = 1
    let opening = true
    let scroller: HTMLElement | null = null
    let left0 = 0
    let lastX = 0
    let lastT = 0
    let vx = 0

    const down = (e: PointerEvent) => {
      // Touch only: a mouse drag over the output is a text selection.
      //
      // Note this does not check `enabled`. `.phone` is touch-action: pan-y
      // whatever the layout, so if we stopped listening when the drawer is
      // unavailable - pinned beside the content, or behind a sheet - nobody
      // would be driving mirror mode's sideways panning and it would simply
      // stop working. A touchscreen laptop is exactly that case. `enabled`
      // gates the drawer alone, further down.
      if (e.pointerType !== 'touch' || !e.isPrimary) return
      mode = 'undecided'
      id = e.pointerId
      x0 = lastX = e.clientX
      y0 = e.clientY
      lastT = e.timeStamp
      vx = 0
      opening = !live.current.open
      width = panel.current?.offsetWidth || 1
      // Only while opening: with the drawer out, the finger is on the drawer
      // or the scrim and there is nothing under it that scrolls sideways.
      scroller = opening ? scrollerAt(e.target) : null
      left0 = scroller ? scroller.scrollLeft : 0
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
        // The scroller is asked first, and in the direction of travel: it
        // owns the drag while it still has room to move that way. Only when
        // it is against its left end does a rightward drag become the
        // drawer's. Asking about the left end alone was wrong in the
        // direction that matters most - dragging left to read past column 80
        // is the whole point of mirror mode, and it went to the drawer's
        // "wrong way, ignore it" branch.
        const room = dx > 0 ? left0 > 0 : left0 < maxLeft(scroller)
        // Opening only ever goes right, closing only ever goes left.
        const wants = live.current.enabled && (opening ? dx > 0 : dx < 0)
        mode = scroller && room ? 'pan' : wants ? 'drawer' : 'drop'
        if (mode === 'drop') return
        // Keeps the rest of the drag coming here even as the scrim turns
        // interactive underneath it. Throws if the pointer is already gone,
        // which is a lost gesture and not worth taking the app down for.
        try {
          el.setPointerCapture(id)
        } catch {
          // ignore
        }
      }

      const dt = e.timeStamp - lastT
      if (dt > 0) vx = (e.clientX - lastX) / dt
      lastX = e.clientX
      lastT = e.timeStamp

      if (mode === 'pan') {
        scroller!.scrollLeft = left0 - dx
        return
      }
      setDrag(at(opening, dx, width))
    }

    const up = (e: PointerEvent) => {
      if (e.pointerId !== id) return
      if (mode === 'drawer') {
        const x = at(opening, e.clientX - x0, width)
        setOpenNow(Math.abs(vx) > FLICK ? vx > 0 : x > SETTLE)
        setDrag(null)
        swallowClick(el)
      }
      mode = 'idle'
      id = -1
    }

    const cancel = () => {
      if (mode === 'drawer') setDrag(null)
      mode = 'idle'
      id = -1
    }

    const setOpenNow = (v: boolean) => live.current.setOpen(v)

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
  }, [root, panel])

  return { rootRef: setRoot, drag }
}

function maxLeft(el: HTMLElement | null): number {
  return el ? el.scrollWidth - el.clientWidth : 0
}

function at(opening: boolean, dx: number, width: number): number {
  const x = (opening ? 0 : width) + dx
  return Math.max(0, Math.min(1, x / width))
}

/** The nearest ancestor of the touch that scrolls sideways, if any. */
function scrollerAt(target: EventTarget | null): HTMLElement | null {
  for (let n = target as Element | null; n instanceof HTMLElement; n = n.parentElement) {
    if (n.scrollWidth > n.clientWidth) {
      const ox = getComputedStyle(n).overflowX
      if (ox === 'auto' || ox === 'scroll') return n
    }
  }
  return null
}

/**
 * A drag that ends on a pane row would otherwise be followed by a click that
 * opens that pane - swiping the drawer shut would navigate. Eat exactly one,
 * and only if it arrives.
 */
function swallowClick(el: HTMLElement) {
  const eat = (e: Event) => {
    e.stopPropagation()
    e.preventDefault()
  }
  el.addEventListener('click', eat, { capture: true, once: true })
  setTimeout(() => el.removeEventListener('click', eat, true), 350)
}
