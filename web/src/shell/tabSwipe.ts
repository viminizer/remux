/**
 * Which way a horizontal drag across a two-tab screen should go.
 *
 * Split out from the hook because it is the whole of the decision and none of
 * the DOM. The rule is the one useDrawerSwipe already uses for mirror mode:
 * **the inner gesture owns the drag only while it has somewhere to go.** On
 * the first tab a rightward drag has nowhere to go, so it belongs to the
 * drawer, exactly as a rightward drag on output scrolled fully left does.
 *
 * The alternative - tabs take every horizontal drag - would take the drawer
 * away on three of the app's screens, and the drawer is how you leave them.
 */
export type Claim = 'prev' | 'next' | 'pass'

export function claim(dx: number, index: number, count: number): Claim {
  if (dx < 0) return index < count - 1 ? 'next' : 'pass'
  if (dx > 0) return index > 0 ? 'prev' : 'pass'
  return 'pass'
}
