// Tests for the rule that decides who owns a horizontal drag.
//
// Run with: npm test
// Same shape as ansi.test.js: esbuild turns the module into a temp file, so
// this needs no test-runner dependency of its own.

import test from 'node:test'
import assert from 'node:assert/strict'
import { claim } from '../.test-build/shell/tabSwipe.js'

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
