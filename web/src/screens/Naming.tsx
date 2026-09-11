import { useEffect, useState } from 'react'
import type { Naming } from '../types'
import { ago } from '../store'
import { api } from '../api'

/**
 * What the pane namer has been doing, on its own screen.
 *
 * Naming is the only part of remux that spends money, and the only one with
 * nothing to look at when it goes wrong. A wrong glyph is visible on the pane;
 * a model call that was never made looks exactly like one that failed, which
 * looks exactly like one that found every name still correct - in all three
 * cases the panes keep the names they had. The log holds failures only,
 * because logging every success would fill it, so "is it actually running"
 * could only be answered by reading remux.log over ssh.
 *
 * It started as a block inside Settings and outgrew it within a day, for the
 * same reason the key pad did: the sections around it are fixed and this one
 * grows with every run. Twenty-four runs of a twenty-pane workspace is a page
 * of its own, and squeezed under the switch it pushed Display and About off
 * the screen while still being too cramped to read.
 *
 * A screen also changes what it can afford to say. The run list is the point,
 * so it gets the room, and the summary that used to be three words now has a
 * card: the state, the chain behind it, and what the whole thing has cost
 * since the service started.
 */

/** How long between polls. */
//
// Slower than a run can finish, which is the point: this is a history, not a
// progress bar, and the one live thing on it - "asking now" - lasts several
// seconds. The report is a ring buffer in the server's memory, so the request
// is cheaper than the render either way.
const POLL_MS = 5000

/**
 * Reads the report, and keeps reading it while the caller is mounted.
 *
 * `every` of 0 fetches once and stops, which is what the Settings row wants: a
 * summary line does not need to tick, and a second poller running behind a
 * screen nobody is on is exactly the kind of thing #32 was about.
 */
export function useNaming(every = POLL_MS): { n: Naming | null; err: string | null } {
  const [n, setN] = useState<Naming | null>(null)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    let alive = true
    const load = () =>
      api
        .naming()
        .then((r) => alive && (setN(r), setErr(null)))
        .catch((e) => alive && setErr(String(e.message ?? e)))
    load()
    if (!every) return () => void (alive = false)
    const t = setInterval(load, every)
    return () => {
      alive = false
      clearInterval(t)
    }
  }, [every])

  return { n, err }
}

/**
 * The one line that answers "is it running".
 *
 * Off and "no CLI installed" are the pair worth separating: they look the same
 * from the phone - no names appearing - and one is a switch, the other is a
 * missing binary on the Mac.
 */
export function namingState(n: Naming): { dot: string; label: string; detail: string } {
  if (!(n.chain ?? []).length)
    return {
      dot: 'stale',
      label: 'No model found',
      detail: 'neither claude nor codex is on the path',
    }
  const chain = (n.chain ?? []).join(' → ')
  if (!n.enabled)
    return { dot: 'shell', label: 'Off', detail: `switched off in Settings; would use ${chain}` }
  if (n.working) return { dot: 'waiting', label: 'Asking now', detail: chain }
  if (n.retryMs > 0)
    return {
      dot: 'stale',
      label: 'Backing off',
      detail: `every tier failed; trying again in ${Math.max(1, Math.round(n.retryMs / 60000))} min`,
    }
  // Away is the presence gate, and reaching this screen does not clear it: it
  // is the last answer the server cached, refreshed on its own schedule.
  if (n.away)
    return { dot: 'idle', label: 'Paused', detail: 'nothing is asked while nobody is reading' }
  return { dot: 'idle', label: 'Watching', detail: chain }
}

export function NamingScreen({ onBack }: { onBack: () => void }) {
  const { n, err } = useNaming()

  return (
    <div className="screen on">
      <div className="screen-head">
        <button className="iconbtn" onClick={onBack}>
          ←
        </button>
        <h2>Naming</h2>
      </div>

      <div className="screen-body">
        {err && (
          <div className="sec">
            <div className="card">
              <div className="crow">
                <div className="lbl dim">{err}</div>
              </div>
            </div>
          </div>
        )}
        {n && <Summary n={n} />}
        {n && <Runs n={n} />}
      </div>
    </div>
  )
}

function Summary({ n }: { n: Naming }) {
  const state = namingState(n)
  return (
    <div className="sec">
      <h3>Right now</h3>
      <div className="card">
        <div className="crow">
          <span className={`dot ${state.dot}`} />
          <div className="lbl">
            {state.label}
            <small>{state.detail}</small>
          </div>
        </div>
        <div className="crow">
          <div className="lbl">
            Model calls
            <small>since the service started</small>
          </div>
          <div className="val">
            {n.calls}
            {n.failed > 0 && <span className="run-bad"> · {n.failed} failed</span>}
          </div>
        </div>
        <div className="crow">
          <div className="lbl">
            Names written
            <small>a pane is asked about at most once every 90 seconds</small>
          </div>
          <div className="val">{n.wrote}</div>
        </div>
        <div className="crow">
          <div className="lbl">
            Sent to a model
            {/* Not a bill. It is the only number here that grows with what the
                feature costs, and it is free to keep. */}
            <small>screens, in characters</small>
          </div>
          <div className="val">{compact(n.chars)}</div>
        </div>
      </div>
    </div>
  )
}

function Runs({ n }: { n: Naming }) {
  const runs = n.runs ?? []
  if (!runs.length) {
    return (
      <div className="sec">
        <h3>Runs</h3>
        <div className="card">
          <div className="crow">
            <div className="lbl dim">
              Nothing yet
              <small>
                {(n.chain ?? []).length
                  ? 'panes are named in one batched call, and only while someone is looking'
                  : 'neither claude nor codex was found, so there is nothing to ask'}
              </small>
            </div>
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="sec">
      <h3>Runs</h3>
      <div className="card">
        <div className="runs">
          {runs.map((r) => (
            <div className="run" key={r.at}>
              <div className="run-head">
                <span className="run-when">{ago(r.at)}</span>
                <span className={r.tier ? 'run-tier' : 'run-tier run-bad'}>
                  {r.tier || 'no answer'}
                </span>
                <span className="run-cost">
                  {r.panes} {r.panes === 1 ? 'pane' : 'panes'} · {(r.ms / 1000).toFixed(1)}s ·{' '}
                  {compact(r.chars)}
                </span>
              </div>
              {(r.names ?? []).map((w) => (
                <div className="run-name" key={w.pane}>
                  {w.project && <span className="run-proj">{w.project}</span>}
                  <span>{w.title}</span>
                  {/* Not a model answer. Worth saying, because the name on the
                      pane gives no hint and a run of these means the chain is
                      down rather than doing well. */}
                  {w.from === 'screen' && <span className="run-from">off screen</span>}
                </div>
              ))}
              {(r.notes ?? []).map((note, i) => (
                <div className="run-note" key={i}>
                  {note}
                </div>
              ))}
              {/* The common case once a workspace has settled, and without a
                  line of its own it reads as a run that failed: the model
                  looked at every pane and said every name was still right. */}
              {r.tier && !(r.names ?? []).length && (
                <div className="run-name run-quiet">every name still fits</div>
              )}
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

/** 29485 -> 29k. A character count is a magnitude, never a figure to read. */
function compact(n: number): string {
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${Math.round(n / 1000)}k`
  return `${(n / 1_000_000).toFixed(1)}M`
}
