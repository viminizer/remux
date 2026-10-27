import { test } from 'node:test'
import assert from 'node:assert/strict'
import { formFor, runningIn } from '../.test-build/loopForm.js'

const ordinary = { repo: '/repo', role: 'build', agent: 'codex', name: 'loop-codex', scope: 'frontend', instructions: 'ordinary brief', model: '', instrMode: 'add' }
const quality = { ...ordinary, name: 'loop-quality', scope: 'code-quality', instructions: 'quality brief', model: 'gpt-6.1-sol', instrMode: 'replace' }
for (const loops of [[ordinary, quality], [quality, ordinary]]) {
  test(`mixed lane reconstruction prefers ordinary (${loops[0].name} first)`, () => {
    const f = formFor('/repo', loops, null)
    assert.equal(f.codex, 1)
    assert.equal(f.scope, 'frontend')
    assert.equal(f.instr, 'ordinary brief')
    assert.deepEqual(runningIn('/repo', loops, true), [quality])
    assert.deepEqual(runningIn('/repo', loops, false), [ordinary])
  })
}
test('quality-only initialization has one coordinator and no ancillary roles', () => {
  const f = formFor('/repo', [quality], null)
  assert.equal(f.scope, 'code-quality')
  assert.equal(f.codex, 1)
  assert.equal(f.claude, 0)
  assert.equal(f.reviews, 0)
  assert.equal(f.supervise, false)
  assert.equal(f.cleanup, false)
})
