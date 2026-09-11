import { useEffect, useRef } from 'react'

/**
 * Two tabs side by side in one horizontal scroller, paged by the browser.
 *
 * ## Why the browser and not us
 *
 * The first three attempts at this were a pointer-event gesture that competed
 * with the list underneath it, and all three worked on a desktop and did
 * nothing on the phone. The list is a scroll container. When a finger lands on
 * one and starts to move, the browser decides whether it owns the gesture
 * before any JavaScript can claim it, and when it takes it, it fires
 * pointercancel and stops sending moves. A thumb swiping sideways across a
 * list starts its arc downward, so the browser took it every time.
 *
 * Nothing above that is tunable. The axis test, the travel threshold and the
 * strip check were all arguing about events that stop arriving. Swipes over
 * the warning bar worked for exactly one reason: there is no scroller under
 * it, so nothing cancelled the pointer.
 *
 * Synthetic PointerEvents cannot reproduce it either - they are injected below
 * the touch pipeline, so no scroll ever starts and no cancel is ever sent. The
 * bug was invisible to every test that could be run here, which is the part
 * worth remembering.
 *
 * So the paging is the browser's. Nested scrollers on two axes are something
 * it does natively and well: this one scrolls across, the list inside each
 * page scrolls down, and there is no arbitration to lose. Scroll snapping
 * gives the finger-following that the old version explicitly gave up on, for
 * free.
 *
 * ## What it costs
 *
 * `.phone` is `touch-action: pan-y`, which is what hands every horizontal drag
 * to useDrawerSwipe. A descendant cannot widen that - the effective value is
 * the intersection down the tree - so the screens using this pager set
 * `pan-x pan-y` on `.phone` itself and give up the drawer swipe while they are
 * open. Both screens have a back arrow, so the drawer is still one tap away.
 *
 * Both tabs are mounted, because a page has to exist to be scrolled to. On the
 * repo screen that means the pull requests are fetched when the repo opens
 * rather than when the tab is first tapped - one `gh pr list`, measured at
 * about half a second, in exchange for the tab being there when you arrive.
 */
export function TabPager({
  index,
  onIndex,
  children,
}: {
  index: number
  onIndex: (i: number) => void
  children: React.ReactNode[]
}) {
  const ref = useRef<HTMLDivElement>(null)
  // What the parent last asked for. Scrolling fires a stream of events and
  // each one would otherwise be reported back as a change, including the ones
  // caused by our own scrollTo.
  const shown = useRef(index)

  // Follow the segmented control. Instant on the first paint so a route that
  // arrives on the second tab does not animate in from the first.
  useEffect(() => {
    const el = ref.current
    if (!el || index === shown.current) return
    shown.current = index
    // Smooth only when there is someone to see it. A smooth scroll in a
    // hidden document is deferred and may never run, which would leave the
    // control saying one thing and the pager showing another - and it is why
    // this could not be tested here until it was written this way.
    el.scrollTo({
      left: index * el.clientWidth,
      behavior: document.hidden ? 'auto' : 'smooth',
    })
  }, [index])

  useEffect(() => {
    const el = ref.current
    if (!el) return
    // Straight off the scroll event, with no frame in between. A scroll fires
    // events the whole way across, but the rounded page only changes once, at
    // the half way point, so the work here is a divide and a compare and the
    // early return covers every other event.
    //
    // It was coalesced with requestAnimationFrame at first. That is the kind
    // of caution that costs more than it buys: rAF does not run in a hidden
    // tab, so the sync could not be tested here at all, and the control now
    // lights the tab you are heading for while your finger is still moving,
    // which is what a pager should do.
    const on = () => {
      if (!el.clientWidth) return
      const i = Math.round(el.scrollLeft / el.clientWidth)
      if (i === shown.current) return
      shown.current = i
      onIndex(i)
    }
    el.addEventListener('scroll', on, { passive: true })
    return () => el.removeEventListener('scroll', on)
  }, [onIndex])

  // Keep the right page under the viewport when the phone is rotated or the
  // keyboard opens. Without this a resize leaves the scroller half way between
  // two pages, and the snap does not re-run on its own.
  useEffect(() => {
    const el = ref.current
    if (!el || typeof ResizeObserver === 'undefined') return
    // Only on a real width change. Observing fires once on its own, and
    // again for layout the scroll itself caused, and an unconditional reset
    // there pins the scroller at the page it started on - which is exactly
    // how this first went wrong.
    let width = el.clientWidth
    const ro = new ResizeObserver(() => {
      if (el.clientWidth === width) return
      width = el.clientWidth
      el.scrollLeft = shown.current * width
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  return (
    <div className="tab-pager" ref={ref}>
      {children.map((child, i) => (
        <div className="tab-page" key={i}>
          {child}
        </div>
      ))}
    </div>
  )
}
