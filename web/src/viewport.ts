/**
 * Keeps the app exactly as tall as the visible area.
 *
 * The app is one non-scrolling screen with the composer pinned to the bottom,
 * so "visible" has to mean what the user can actually see. `100dvh` does not:
 * it shrinks for browser chrome but not for the software keyboard, so on iOS
 * the keyboard covers the composer and there is nothing to scroll to reach it.
 * `visualViewport` is the only thing that reports the keyboard.
 */
export function trackViewport() {
  const vv = window.visualViewport
  if (!vv) return // dvh fallback in the stylesheet

  const apply = () => {
    // Pinch-zoom also shrinks the visual viewport. Rebuilding the layout for
    // it would fight the user mid-gesture, so hold the last unzoomed height.
    if (vv.scale > 1.01) return
    document.documentElement.style.setProperty('--app-h', vv.height + 'px')
    // iOS scrolls the layout viewport to reveal a focused field. The app has
    // nothing to scroll, so that only pushes the top bar out of sight.
    if (window.scrollY !== 0) window.scrollTo(0, 0)
  }

  vv.addEventListener('resize', apply)
  vv.addEventListener('scroll', apply)
  // Android with interactive-widget=resizes-content shrinks the layout
  // viewport instead, which only the window reports.
  window.addEventListener('resize', apply)
  window.addEventListener('orientationchange', apply)
  apply()
}
