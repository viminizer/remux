import { useState } from 'react'

import type { HarnessRepo, LoopStart, Preset } from './types'

const OTHER = '__other__'

/**
 * Start loops in one repo. Everything is pre-filled from the last start, and
 * a saved preset fills the instructions in one tap, so most starts are just
 * the Start button.
 */
export function StartLoop({
  repos,
  last,
  presets,
  onBack,
  onStart,
  onSavePresets,
}: {
  repos: HarnessRepo[]
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
  const [claude, setClaude] = useState(last ? last.agents.includes('claude') : true)
  const [codex, setCodex] = useState(last ? last.agents.includes('codex') : false)
  const [review, setReview] = useState(last ? last.review : true)
  const [scope, setScope] = useState(last?.scope ?? 'full')
  const [instr, setInstr] = useState(last?.instructions ?? '')
  const [mode, setMode] = useState<'add' | 'replace'>(last?.instrMode ?? 'add')
  const [busy, setBusy] = useState(false)

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
  const agents = [...(claude ? ['claude' as const] : []), ...(codex ? ['codex' as const] : [])]
  const ok = !!target && (agents.length > 0 || review) && !busy

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
          <select value={repo} onChange={(e) => setRepo(e.target.value)}>
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
          <label>Loops</label>
          <div className="seg toggles">
            <button className={claude ? 'on' : ''} onClick={() => setClaude(!claude)}>
              Claude
            </button>
            <button className={codex ? 'on' : ''} onClick={() => setCodex(!codex)}>
              Codex
            </button>
            <button className={review ? 'on' : ''} onClick={() => setReview(!review)}>
              Review
            </button>
          </div>
        </div>

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
            void onStart({ repo: target, agents, review, scope: scope.trim() || 'full', instructions: instr, instrMode: mode }).finally(
              () => setBusy(false),
            )
          }}
        >
          {busy ? 'Starting…' : 'Start'}
        </button>
      </div>
    </div>
  )
}
