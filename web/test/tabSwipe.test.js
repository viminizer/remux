// Tests for the rule that decides who owns a horizontal drag.
//
// Run with: npm test
// Same shape as ansi.test.js: esbuild turns the module into a temp file, so
// this needs no test-runner dependency of its own.

import test from 'node:test'
import assert from 'node:assert/strict'
import { claim, isStrip } from '../.test-build/shell/tabSwipe.js'

test('dragging left goes to the next tab', () => {
  assert.equal(claim(-40, 0, 2), 'next')
})

test('dragging right goes back to the previous tab', () => {
  assert.equal(claim(40, 1, 2), 'prev')
})

// The one that matters. useDrawerSwipe takes every rightward drag, and this is
// the only thing that lets it: on the first tab there is nowhere to go, so the
// drag is passed on and the drawer opens as it always did. Claiming it here
// would take away the way off these three screens.
test('a rightward drag on the first tab is left for the drawer', () => {
  assert.equal(claim(40, 0, 2), 'pass')
})

test('a leftward drag on the last tab is not claimed', () => {
  assert.equal(claim(-40, 1, 2), 'pass')
})

test('a drag with no horizontal travel is not claimed', () => {
  assert.equal(claim(0, 0, 2), 'pass')
})

// The regression that shipped dead. A vertical list whose rows overflow
// sideways is not a strip of chips, and treating it as one disabled every
// swipe on a phone while testing clean in a wide desktop window.
test('a vertical list that overflows sideways is not a strip', () => {
  assert.equal(isStrip(115, 1036), false)
})

test('a row of chips is a strip', () => {
  assert.equal(isStrip(240, 0), true)
})

test('sub-pixel overflow is not a strip', () => {
  assert.equal(isStrip(1, 0), false)
})
