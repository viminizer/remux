// Tests for the socket's listener bookkeeping.
//
// Run with: npm test
// ws.ts is transformed by esbuild into a temp module, the same way ansi.ts is,
// so this needs no test-runner or DOM dependency of its own. Only the handful
// of browser globals the constructor and close() touch are stubbed.

import test from 'node:test'
import assert from 'node:assert/strict'

function stubDOM() {
  const listeners = []
  const target = () => ({
    addEventListener: (type, fn) => listeners.push({ type, fn }),
    removeEventListener: (type, fn) => {
      const i = listeners.findIndex((l) => l.type === type && l.fn === fn)
      if (i >= 0) listeners.splice(i, 1)
    },
  })
  globalThis.document = { ...target(), visibilityState: 'visible' }
  globalThis.window = target()
  // Sharing one array across both targets is deliberate: what this asserts is
  // that nothing is left armed anywhere, not which target held it.
  globalThis.location = { protocol: 'http:', host: 'localhost' }
  return listeners
}

function fire(listeners, type) {
  for (const l of [...listeners]) if (l.type === type) l.fn()
}

const { Socket } = await import('../.test-build/ws.js')

test('close removes every listener it added', () => {
  const listeners = stubDOM()
  const s = new Socket({
    onTree: () => {},
    onSnap: () => {},
    onGone: () => {},
    onConn: () => {},
    onGh: () => {},
  })
  assert.ok(listeners.length > 0, 'constructor added nothing')

  s.close()
  assert.deepEqual(
    listeners.map((l) => l.type),
    [],
    'still armed after close',
  )
})

test('a closed socket no longer reports going offline', () => {
  // The bug: `offline` was added as an inline arrow, so close() had no
  // reference to remove. App.tsx builds the Socket in an effect, which React
  // StrictMode mounts twice in development - leaving the first, dead socket
  // able to flip the connection state behind the live one.
  const listeners = stubDOM()
  const seen = []
  const s = new Socket({
    onTree: () => {},
    onSnap: () => {},
    onGone: () => {},
    onConn: (state) => seen.push(state),
    onGh: () => {},
  })

  fire(listeners, 'offline')
  assert.deepEqual(seen, ['offline'], 'a live socket should report it')

  s.close()
  fire(listeners, 'offline')
  assert.deepEqual(seen, ['offline'], 'a closed socket reported it again')
})
