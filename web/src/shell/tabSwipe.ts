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

// Rounding. Sub-pixel layout leaves a fraction of overflow on boxes no finger
// could scroll.
export const SLOP = 2

/**
 * Whether a box is a sideways strip, like the filter chips, rather than a
 * vertical list that merely overflows sideways.
 *
 * This is the distinction that killed the gesture on a phone. `.list` and
 * `.screen-body` set overflow-y:auto, and CSS forces the other axis to compute
 * to auto along with it, so a vertical list reports overflow-x:auto too. Asked
 * only "does this scroll sideways", every list said yes the moment one issue
 * title was a pixel wider than the screen - which on a phone is the normal
 * state, and on a wide desktop window is never, so it tested clean and shipped
 * dead.
 *
 * Room across and none down is what actually separates the two.
 */
export function isStrip(hOver: number, vOver: number): boolean {
  return hOver > SLOP && vOver <= SLOP
}
