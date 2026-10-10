import { useState } from 'react'

import type { HarnessRepo, Loop, LoopStart, Preset } from './types'

const OTHER = '__other__'

/**
 * The loops one repo should have. Start makes the repo match: missing loops
 * start, extra ones stop, running ones take the new scope and instructions.
 * The form shows the loops running in the repo, or the last start when none
 * are, and a saved preset fills the instructions in one tap.
 */
export function StartLoop({
  repos,
  running,
  last,
  presets,
  onBack,
  onStart,
  onSavePresets,
}: {
  repos: HarnessRepo[]
  running: Loop[]
  last: LoopStart | null
  presets: Preset[]
  onBack: () => void
  onStart: (v: LoopStart) => Promise<void>
  onSavePresets: (p: Preset[]) => Promise<void>
}) {
  const known = (p: string) => repos.some((r) => r.path === p)
  const first = last?.repo ?? repos[0]?.path ?? ''
  const [repo, setRepo] = useState(first && known(first) ? first : first ? OTHER : '')
  const [path, setPath] = useState(first && !known(first) ? first : '')
  const init = formFor(first, running, last)
  const [claude, setClaude] = useState(init.claude)
  const [codex, setCodex] = useState(init.codex)
  const [reviews, setReviews] = useState(init.reviews)
  const [supervise, setSupervise] = useState(init.supervise)
  const [supAgent, setSupAgent] = useState<'claude' | 'codex'>(init.supAgent)
  const [scope, setScope] = useState(init.scope)
  const [instr, setInstr] = useState(init.instr)
  const [mode, setMode] = useState<'add' | 'replace'>(init.mode)
  const [busy, setBusy] = useState(false)

  const fill = (p: string) => {
    const f = formFor(p, running, last)
    setClaude(f.claude)
    setCodex(f.codex)
    setReviews(f.reviews)
    setSupervise(f.supervise)
    setSupAgent(f.supAgent)
    setScope(f.scope)
    setInstr(f.instr)
    setMode(f.mode)
  }

  const apply = (p: Preset) => {
    setScope(p.scope || 'full')
    setInstr(p.instructions)
    setMode(p.instrMode)
  }
  const save = () => {
    const name = scope.trim() || 'full'
    const next = [...presets.filter((p) => p.name !== name), { name, scope: name, instructions: instr, instrMode: mode }]
    void onSavePresets(next)
  }
  const target = repo === OTHER ? path.trim() : repo
  const agents = [
    ...Array<'claude'>(claude).fill('claude'),
    ...Array<'codex'>(codex).fill('codex'),
  ]
  const ok = !!target && (agents.length > 0 || reviews > 0 || supervise) && !busy
  const live = runningIn(target, running).length > 0

  return (
    <div className="screen on gh-screen">
      <div className="screen-head">
        <button className="iconbtn" onClick={onBack} aria-label="Back">
          ←
        </button>
        <h2>Start loop</h2>
      </div>

      <div className="screen-body loops-body">
        {presets.length > 0 && (
          <div className="sec">
            <h3>Presets</h3>
            <div className="chipset">
              {presets.map((p) => (
                <span key={p.name} className="chip">
                  <button className="chip-pick" onClick={() => apply(p)}>
                    {p.name}
                  </button>
                  <button
                    className="chip-x"
                    aria-label={`Delete ${p.name}`}
                    onClick={() => void onSavePresets(presets.filter((x) => x.name !== p.name))}
                  >
                    ✕
                  </button>
                </span>
              ))}
            </div>
          </div>
        )}

        <div className="field">
          <label>Project</label>
          <select
            value={repo}
            onChange={(e) => {
              setRepo(e.target.value)
              if (e.target.value !== OTHER) fill(e.target.value)
            }}
          >
            {repos.map((r) => (
              <option key={r.path} value={r.path}>
                {r.slug}
              </option>
            ))}
            <option value={OTHER}>Other path…</option>
          </select>
        </div>
        {repo === OTHER && (
          <div className="field">
            <label>Repo path on the Mac</label>
            <input
              value={path}
              onChange={(e) => setPath(e.target.value)}
              placeholder="~/Desktop/github/remux"
              spellCheck={false}
              autoCapitalize="off"
            />
          </div>
        )}

        <div className="field">
          <label>Build loops</label>
          <Stepper name="Claude" value={claude} onChange={setClaude} />
          <Stepper name="Codex" value={codex} onChange={setCodex} />
        </div>

        <div className="field">
          <label>Review loops</label>
          <Stepper name="Review (Codex)" value={reviews} onChange={setReviews} />
        </div>

        <div className="field">
          <label>Also run</label>
          <div className="seg toggles">
            <button className={supervise ? 'on' : ''} onClick={() => setSupervise(!supervise)}>
              Supervisor
            </button>
          </div>
        </div>

        {supervise && (
          <div className="field">
            <label>Supervisor agent</label>
            <div className="seg">
              <button className={supAgent === 'claude' ? 'on' : ''} onClick={() => setSupAgent('claude')}>
                Claude
              </button>
              <button className={supAgent === 'codex' ? 'on' : ''} onClick={() => setSupAgent('codex')}>
                Codex
              </button>
            </div>
          </div>
        )}

        <div className="field">
          <label>Scope name</label>
          <input value={scope} onChange={(e) => setScope(e.target.value)} spellCheck={false} autoCapitalize="off" />
        </div>
        <div className="field">
          <label>Session instructions</label>
          <textarea
            value={instr}
            onChange={(e) => setInstr(e.target.value)}
            rows={3}
            placeholder="Only do the backend parts of the issues."
          />
        </div>
        <div className="seg">
          <button className={mode === 'add' ? 'on' : ''} onClick={() => setMode('add')}>
            Add to project defaults
          </button>
          <button className={mode === 'replace' ? 'on' : ''} onClick={() => setMode('replace')}>
            Replace them
          </button>
        </div>
        <button className="linkbtn" onClick={save} disabled={!instr.trim()}>
          Save as preset “{scope.trim() || 'full'}”
        </button>
      </div>

      <div className="foot-bar">
        <button
          className="go"
          disabled={!ok}
          onClick={() => {
            setBusy(true)
            void onStart({ repo: target, agents, review: reviews > 0, reviews, supervise, superviseAgent: supAgent, scope: scope.trim() || 'full', instructions: instr, instrMode: mode }).finally(
              () => setBusy(false),
            )
          }}
        >
          {busy ? 'Starting…' : live ? 'Update loops' : 'Start'}
        </button>
      </div>
    </div>
  )
}

/** The loops a start manages in repo p, running now. The chat supervisor is not one. */
function runningIn(p: string, running: Loop[]): Loop[] {
  return running.filter((l) => l.repo === p && (l.role === 'build' || l.role === 'review' || l.role === 'supervise'))
}

/**
 * The form for repo p: what runs there now, so lowering a count stops a loop
 * that is really running. With nothing running, the last start, if it was in
 * this repo.
 */
function formFor(p: string, running: Loop[], last: LoopStart | null) {
  const mine = runningIn(p, running)
  if (mine.length) {
    const builds = mine.filter((l) => l.role === 'build')
    const sup = mine.find((l) => l.role === 'supervise')
    const ref = builds[0] ?? mine[0]
    return {
      claude: builds.filter((l) => l.agent === 'claude').length,
      codex: builds.filter((l) => l.agent === 'codex').length,
      reviews: mine.filter((l) => l.role === 'review').length,
      supervise: !!sup,
      supAgent: (sup?.agent || 'claude') as 'claude' | 'codex',
      scope: ref.scope || 'full',
      instr: ref.instructions,
      mode: (ref.instrMode || 'add') as 'add' | 'replace',
    }
  }
  const l = last?.repo === p ? last : null
  const many = (a: string) => l?.agents.filter((x) => x === a).length ?? 0
  return {
    claude: l ? many('claude') : 1,
    codex: l ? many('codex') : 0,
    reviews: l ? (l.reviews ?? (l.review ? 1 : 0)) : 1,
    supervise: l ? !!l.supervise : true,
    supAgent: l?.superviseAgent ?? ('claude' as const),
    scope: l?.scope ?? 'full',
    instr: l?.instructions ?? '',
    mode: l?.instrMode ?? ('add' as const),
  }
}

/** How many build loops of one agent: 0 to 5, each its own session. */
function Stepper({ name, value, onChange }: { name: string; value: number; onChange: (n: number) => void }) {
  return (
    <div className="stepper-row">
      <span className="grow">{name}</span>
      <button disabled={value <= 0} onClick={() => onChange(value - 1)} aria-label={`Fewer ${name} loops`}>
        −
      </button>
      <span className="n">{value}</span>
      <button disabled={value >= 5} onClick={() => onChange(value + 1)} aria-label={`More ${name} loops`}>
        +
      </button>
    </div>
  )
}
