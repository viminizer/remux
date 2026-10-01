import { useEffect, useState } from 'react'

import { api } from '../api'
import type { Loop } from './types'
import { itemURL, loopDot, project, since } from './types'

/**
 * One loop: what it is on, its instructions, and the last lines of its log.
 *
 * The log is the loop's own pane, read through the same capture call the pane
 * view uses - the loop prints there and nothing else keeps a copy.
 */
export function LoopDetail({
  loop,
  onBack,
  onStop,
  onSave,
}: {
  loop: Loop | null
  onBack: () => void
  onStop: (when: 'now' | 'after') => void
  onSave: (v: { scope: string; instructions: string; instrMode: 'add' | 'replace' }) => Promise<void>
}) {
  const [log, setLog] = useState<string[]>([])
  const [editing, setEditing] = useState(false)
  const [asking, setAsking] = useState(false)
  const [scope, setScope] = useState('')
  const [instr, setInstr] = useState('')
  const [mode, setMode] = useState<'add' | 'replace'>('add')

  const pane = loop?.pane
  useEffect(() => {
    if (!pane) return
    let live = true
    const read = () =>
      api
        .capture(pane, 200)
        .then((r) => {
          if (!live) return
          const lines = r.lines.map((l) => l.replace(/\x1b\[[0-9;]*m/g, ''))
          while (lines.length && !lines[lines.length - 1].trim()) lines.pop()
          setLog(lines.slice(-30))
        })
        .catch(() => {})
    read()
    const t = setInterval(read, 3000)
    return () => {
      live = false
      clearInterval(t)
    }
  }, [pane])

  if (!loop) {
    return (
      <div className="screen on gh-screen">
        <div className="screen-head">
          <button className="iconbtn" onClick={onBack} aria-label="Back">
            ←
          </button>
          <h2>Loop</h2>
        </div>
        <div className="msg">
          <div className="glyph">↻</div>
          <h2>This loop has stopped</h2>
        </div>
      </div>
    )
  }

  const startEdit = () => {
    setScope(loop.scope || 'full')
    setInstr(loop.instructions)
    setMode(loop.instrMode === 'replace' ? 'replace' : 'add')
    setEditing(true)
  }

  return (
    <div className="screen on gh-screen">
      <div className="screen-head">
        <button className="iconbtn" onClick={onBack} aria-label="Back">
          ←
        </button>
        <h2>
          {loop.role === 'review' ? 'review' : loop.agent} · {project(loop)}
          <small>
            <span className={`dot ${loopDot(loop)}`} /> {loop.state || 'starting'} · {since(loop.since)}
          </small>
        </h2>
      </div>

      <div className="screen-body loops-body">
        <div className="sec">
          <h3>Now</h3>
          <div className="card">
            <div className="crow">
              <span className="lbl">
                {loop.state === 'working' && loop.slug ? (
                  <a href={itemURL(loop.slug, loop.issue, loop.role === 'review' ? 'pr' : 'issue')} target="_blank" rel="noreferrer">
                    #{loop.issue} {loop.title}
                  </a>
                ) : loop.state === 'working' ? (
                  `#${loop.issue} ${loop.title}`
                ) : (
                  loop.note ||
                  (loop.state === 'triaging'
                    ? 'No issue was marked ready. Reading the issues to find ones that can start.'
                    : 'No ready issues. Looks again every minute.')
                )}
                <small>{loop.slug || loop.repo}</small>
              </span>
            </div>
          </div>
          {loop.stop && <div className="scopenote">Stopping after this issue.</div>}
        </div>

        <div className="sec">
          <h3>Instructions</h3>
          {editing ? (
            <div className="card loop-form">
              {loop.role !== 'review' && (
                <div className="field">
                  <label>Scope name</label>
                  <input value={scope} onChange={(e) => setScope(e.target.value)} spellCheck={false} autoCapitalize="off" />
                </div>
              )}
              <div className="field">
                <label>Session instructions</label>
                <textarea value={instr} onChange={(e) => setInstr(e.target.value)} rows={4} />
              </div>
              <div className="seg">
                <button className={mode === 'add' ? 'on' : ''} onClick={() => setMode('add')}>
                  Add to project defaults
                </button>
                <button className={mode === 'replace' ? 'on' : ''} onClick={() => setMode('replace')}>
                  Replace them
                </button>
              </div>
              <p className="sheet-note">Used from the next issue. The current one finishes as it started.</p>
            </div>
          ) : (
            <div className="card">
              <div className="crow">
                <span className="lbl">
                  {loop.instructions || <span className="c-dim">None. Project defaults only.</span>}
                  <small>
                    {loop.role !== 'review' && `scope ${loop.scope || 'full'} · `}
                    {loop.instrMode === 'replace' ? 'replaces' : 'adds to'} project defaults
                  </small>
                </span>
              </div>
            </div>
          )}
        </div>

        <div className="sec">
          <h3>Log</h3>
          <pre className="loop-log">{log.join('\n') || '…'}</pre>
        </div>
      </div>

      <div className="foot-bar">
        {asking ? (
          <>
            <p className="sheet-note">Stop after this issue, or now?</p>
            <div className="foot-row">
              <button className="go" onClick={() => onStop('after')}>
                After this issue
              </button>
              <button className="go danger" onClick={() => onStop('now')}>
                Now
              </button>
            </div>
            <button className="cancel" onClick={() => setAsking(false)}>
              Cancel
            </button>
          </>
        ) : editing ? (
          <div className="foot-row">
            <button className="cancel" onClick={() => setEditing(false)}>
              Cancel
            </button>
            <button
              className="go"
              onClick={() =>
                void onSave({ scope: scope.trim() || 'full', instructions: instr, instrMode: mode }).then(() =>
                  setEditing(false),
                )
              }
            >
              Save
            </button>
          </div>
        ) : (
          <div className="foot-row">
            <button className="go ghost" onClick={startEdit}>
              Edit instructions
            </button>
            <button className="go danger" onClick={() => (loop.state === 'working' || loop.state === 'triaging' ? setAsking(true) : onStop('now'))}>
              Stop
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
