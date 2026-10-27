import { test } from 'node:test'
import assert from 'node:assert/strict'
import { formFor, runningIn, restoredDraft } from '../.test-build/loopForm.js'

const ordinary = {
  repo: '/repo',
  role: 'build',
  agent: 'codex',
  name: 'loop-codex',
  scope: 'frontend',
  instructions: 'ordinary brief',
  model: '',
  instrMode: 'add',
}
const quality = {
  ...ordinary,
  name: 'loop-quality',
  scope: 'code-quality',
  instructions: 'quality brief',
  model: 'gpt-6.1-sol',
  instrMode: 'replace',
}
for (const loops of [
  [ordinary, quality],
  [quality, ordinary],
]) {
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

const base = {
  repo: '/repo',
  agents: ['codex'],
  reviews: 0,
  review: false,
  supervise: false,
  cleanup: false,
  scope: 'frontend',
  instructions: '',
  instrMode: 'add',
}
const named = { ...ordinary, name: 'loop-codex-repo', state: 'working' }
const { previewImpact } = await import('../.test-build/loopForm.js')
test('ordinary reconciliation preserves quality and previews lowering real counts', () => {
  const second = { ...named, name: 'loop-codex2-repo', state: 'idle' }
  const rows = previewImpact(base, [named, second, quality])
  assert.equal(rows.filter((x) => x.kind === 'update').length, 1)
  assert.equal(rows.filter((x) => x.kind === 'stop').length, 1)
  assert.match(rows.find((x) => x.kind === 'stop').text, /now/)
  assert.match(rows.find((x) => x.kind === 'keep').text, /1 other loop/)
})
test('only working or triaging removals defer their stop', () => {
  for (const state of ['working', 'triaging', 'blocked', 'idle', '']) {
    const rows = previewImpact({ ...base, agents: ['claude'] }, [
      { ...named, state },
    ])
    assert.match(
      rows.find((x) => x.kind === 'stop').text,
      state === 'working' || state === 'triaging'
        ? /after current task/
        : /now/,
    )
  }
})
test('quality has one coordinator and never ordinary stop or reviewer counts', () => {
  const rows = previewImpact({ ...base, scope: 'code-quality' }, [
    named,
    { ...quality, name: 'loop-quality-codex-repo' },
  ])
  assert.equal(rows.filter((x) => x.kind === 'update').length, 1)
  assert.match(
    rows.find((x) => x.kind === 'update').text,
    /loop-quality-codex-repo/,
  )
  assert.match(rows.find((x) => x.kind === 'keep').text, /1 other loop/)
})
test('distinct clone collisions are reported instead of pretending they start', () => {
  const rows = previewImpact(base, [{ ...named, repo: '/another/repo' }])
  assert.equal(rows[0].kind, 'error')
})
test('provider replacement is explicit and requires idle loop', () => {
  const rows = previewImpact(
    { ...base, supervise: true, superviseAgent: 'claude' },
    [
      {
        ...named,
        name: 'loop-supervise-repo',
        role: 'supervise',
        agent: 'codex',
      },
    ],
  )
  assert.match(rows.find((x) => x.kind === 'replace').text, /requires idle/)
})

test('quality leaves historical quality sessions untouched like the server', () => {
  const rows = previewImpact({ ...base, scope: 'code-quality' }, [
    named,
    quality,
  ])
  assert.equal(rows.filter((x) => x.kind === 'stop').length, 0)
  assert.match(rows.find((x) => x.kind === 'keep').text, /2 other loops/)
})
test('queued stop is reported only when present', () => {
  assert.doesNotMatch(previewImpact(base, [named])[0].text, /Queued stop/)
  assert.match(
    previewImpact(base, [{ ...named, stop: 'after' }])[0].text,
    /Queued stop/,
  )
})


test('reload draft survives repeated render initialization until mount consumes it', () => {
  const key = 'remux-start-update-draft'
  const draft = {
    saved: Date.now(),
    target: '/typed/project',
    form: { ...formFor('/typed/project', [], null), scope: 'custom', instr: 'Keep this guidance' },
  }
  const values = new Map([[key, JSON.stringify(draft)]])
  const previous = globalThis.sessionStorage
  globalThis.sessionStorage = {
    getItem: (key) => values.get(key) ?? null,
    removeItem: (key) => values.delete(key),
  }
  try {
    assert.deepEqual(restoredDraft(), draft)
    assert.deepEqual(restoredDraft(), draft)
    assert.ok(values.has(key), 'render must not consume the draft')
    sessionStorage.removeItem(key)
    assert.equal(restoredDraft(), null)
    values.set(key, JSON.stringify({ ...draft, saved: Date.now() - 300001 }))
    assert.equal(restoredDraft(), null, 'expired drafts are ignored')
  } finally {
    if (previous === undefined) delete globalThis.sessionStorage
    else globalThis.sessionStorage = previous
  }
})

const { scopeDraftKey, scopeSelection } = await import('../.test-build/loopForm.js')
test('saved presets sharing a scope keep distinct guidance and project drafts', () => {
  const baseline = formFor('/repo', [], null)
  const accessibility = { name: 'Accessibility', scope: 'custom', instructions: 'Keyboard access', instrMode: 'add' }
  const performance = { name: 'Performance', scope: 'custom', instructions: 'Fast rendering', instrMode: 'replace' }
  const drafts = new Map()
  const first = scopeSelection('/repo', 'custom', baseline, drafts, accessibility)
  assert.equal(first.instr, 'Keyboard access')
  drafts.set(scopeDraftKey('/repo', first.scope, first.presetName), { ...first, instr: 'Edited access', scope: 'access' })
  const second = scopeSelection('/repo', 'custom', first, drafts, performance)
  assert.equal(second.instr, 'Fast rendering')
  assert.equal(second.mode, 'replace')
  const restored = scopeSelection('/repo', 'custom', second, drafts, accessibility)
  assert.equal(restored.instr, 'Edited access')
  assert.equal(restored.scope, 'access')
  assert.equal(scopeSelection('/other', 'custom', baseline, drafts, accessibility).instr, 'Keyboard access')
})
test('named built-in guidance remains distinct from direct built-in drafts', () => {
  const baseline = formFor('/repo', [], null)
  const drafts = new Map()
  for (const scope of ['frontend', 'backend', 'code-quality']) {
    const a = { name: `First ${scope}`, scope, instructions: 'first', instrMode: 'add' }
    const b = { ...a, name: `Second ${scope}`, instructions: 'second' }
    const direct = { ...baseline, scope, instr: 'direct edit' }
    drafts.set(scopeDraftKey('/repo', scope), direct)
    const selected = scopeSelection('/repo', scope, direct, drafts, a)
    assert.equal(selected.instr, 'first')
    drafts.set(scopeDraftKey('/repo', scope, a.name), { ...selected, instr: 'named edit' })
    assert.equal(scopeSelection('/repo', scope, selected, drafts, b).instr, 'second')
    assert.equal(scopeSelection('/repo', scope, selected, drafts, a).instr, 'named edit')
    const restored = scopeSelection('/repo', scope, selected, drafts, undefined, a)
    assert.equal(restored.instr, 'direct edit')
    assert.equal(restored.presetName, undefined)
  }
})
