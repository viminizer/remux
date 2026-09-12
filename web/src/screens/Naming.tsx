import { useEffect, useState } from 'react'
import type { Naming, NamingSpend } from '../types'
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
        {n?.spend && <Cost s={n.spend} chars={n.chars} />}
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
      </div>
    </div>
  )
}

/**
 * What the naming has cost.
 *
 * The number is read from the CLI, not estimated from prompt size. That is
 * not fussiness: two calls with byte-identical prompts measured $0.0374 and
 * $0.0030 on this laptop, because the first wrote the CLI's 18k-token system
 * preamble into the prompt cache and the second read it back. Anything derived
 * from characters cannot see that and is wrong by more than ten times, in the
 * direction that makes an expensive feature look cheap.
 *
 * A month is the unit worth showing. It is the one Kevin is billed in, and it
 * is long enough that a quiet afternoon does not read as the feature being
 * free. It survives restarts for the same reason - the service restarts
 * several times on a day he is working on it, and a total since boot answers
 * for the last eleven minutes.
 */
function Cost({ s, chars }: { s: NamingSpend; chars: number }) {
  const per = s.runs > 0 ? s.usd / s.runs : 0
  const days = Math.max(1, Math.round(s.through))
  return (
    <div className="sec">
      <h3>Cost</h3>
      <div className="card">
        <div className="crow">
          <div className="lbl">
            This month
            <small>
              {monthName(s.month)}, {days} {days === 1 ? 'day' : 'days'} in
              {s.unmetered > 0 && ` · at least, ${s.unmetered} runs went unmetered`}
            </small>
          </div>
          <div className="val">{money(s.usd)}</div>
        </div>

        {/* Straight-line, and labelled as a projection rather than a total.
            On the second of the month it is one day multiplied by thirty, so
            the row above says how many days it is standing on. */}
        <div className="crow">
          <div className="lbl">
            On track for
            <small>if the rest of the month looks like the start of it</small>
          </div>
          <div className="val">{money(s.projected)}</div>
        </div>

        <div className="crow">
          <div className="lbl">
            Per call
            <small>
              {s.runs} {s.runs === 1 ? 'call' : 'calls'} · {compact(chars)} characters sent
            </small>
          </div>
          <div className="val">{money(per)}</div>
        </div>

        {s.prevMonth && (
          <div className="crow">
            <div className="lbl">
              {monthName(s.prevMonth)}
              <small>the whole month</small>
            </div>
            <div className="val">{money(s.prevUsd ?? 0)}</div>
          </div>
        )}

        {/* Worth saying once. The CLI reports list price, which is what these
            calls would bill at - a Claude subscription covers them instead,
            and then this is what the feature is worth rather than what it
            costs. Either way it is the number that moves when naming does. */}
        <div className="crow">
          <div className="lbl dim">
            <small>
              List price, as the CLI reports it. A subscription covers these calls rather than
              billing them.
            </small>
          </div>
        </div>
      </div>
    </div>
  )
}

/** "2026-09" -> "September". */
function monthName(m: string): string {
  const [y, mm] = m.split('-').map(Number)
  if (!y || !mm) return m
  return new Date(y, mm - 1, 1).toLocaleString(undefined, { month: 'long' })
}

/**
 * Money, to as many places as the amount deserves.
 *
 * A per-call figure is a third of a cent, so two decimal places would print it
 * as $0.00 and say the feature is free. A month's total is dollars, where the
 * third place is noise.
 */
function money(n: number): string {
  if (!(n > 0)) return '$0'
  if (n >= 100) return `$${Math.round(n)}`
  if (n >= 1) return `$${n.toFixed(2)}`
  return `$${n.toFixed(3)}`
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
                  {/* The character count was only ever a stand-in for the
                      bill, so it appears only where there is no bill to
                      show - the codex tier, which reports nothing. */}
                  {r.metered > 0 ? money(r.usd) : `${compact(r.chars)} chars`}
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
              {/* A cleared pane is a pane the model refused to guess at, which
                  is the answer we want from an empty screen - but it leaves
                  nothing on the pane, so this line is the only trace of it. */}
              {r.cleared > 0 && (
                <div className="run-name run-quiet">
                  {r.cleared} with nothing on screen, unnamed
                </div>
              )}
              {r.tier && !(r.names ?? []).length && !r.cleared && (
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
