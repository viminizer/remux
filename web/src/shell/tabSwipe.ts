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

// How far the finger travels before the gesture can commit to an axis. Below
// this a drag is still ambiguous and nothing moves. Matches useDrawerSwipe.
export const AXIS = 8

// How much more vertical than horizontal a drag must be before it is given up
// as a scroll. Strictly greater than 1, and that is the whole point: see axis.
export const VERTICAL_BIAS = 2

export type Axis = 'across' | 'down' | 'unsure'

/**
 * Which way a drag is going, once it has gone far enough to tell.
 *
 * The first version asked only whether dy was bigger than dx, and gave up for
 * good the moment it was. That is a hair-trigger on a phone. A thumb swiping
 * across a list travels in an arc, and its first few pixels are as likely to
 * be down as across: a real failing swipe opened with 5px across and 10px
 * down, was written off as a scroll, and then travelled 180 across and 18
 * down. Swipes over the warning bar worked and swipes over the list did not,
 * which is exactly the difference between a deliberate flick and a thumb.
 *
 * So a drag is only given up when it is clearly vertical, and while it is
 * neither it stays unsure rather than being decided wrongly and for ever. The
 * browser is deciding in parallel, and if it starts scrolling it cancels the
 * pointer, which ends the gesture anyway - this only stops us giving up first.
 */
export function axis(dx: number, dy: number): Axis {
  const [ax, ay] = [Math.abs(dx), Math.abs(dy)]
  if (ax >= AXIS && ax > ay) return 'across'
  if (ay >= AXIS && ay > ax * VERTICAL_BIAS) return 'down'
  return 'unsure'
}
