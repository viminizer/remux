import { useEffect, useState } from 'react'
import type { Health, Conn, Naming } from '../types'
import type { Settings } from '../store'
import { ago } from '../store'
import { api } from '../api'

/**
 * scripts/build.sh strips the leading v from the git tag so the string stays
 * valid semver - it is what the update check compares and what keys the
 * service worker's cache. This puts it back for reading, and only for reading.
 * A repo with no tags reports "dev", which gets no v.
 */
const tag = (s: string) => (/^\d/.test(s) ? 'v' + s : s)

const CONN_DOT: Record<Conn, string> = {
  connecting: 'shell',
  live: 'idle',
  offline: 'stale',
  denied: 'waiting',
}

/**
 * There is no connection screen in remux, so connection lives here as a
 * status card. tsnet removed everything a connection screen would have
 * configured: no server URL, no token, no pairing.
 */
export function SettingsScreen({
  health,
  conn,
  lastReached,
  settings,
  patch,
  notifState,
  onEnableNotifications,
  onBack,
  onKeyPad,
  onCheck,
  onUpdate,
}: {
  health: Health | null
  conn: Conn
  lastReached: number | null
  settings: Settings
  patch: (p: Partial<Settings>) => void
  notifState: NotificationPermission | 'unsupported'
  onEnableNotifications: () => void
  onBack: () => void
  onKeyPad: () => void
  onCheck: () => void
  onUpdate: () => void
}) {
  // The server's version is the one being served; __BUILD_VERSION__ is the one
  // running. They differ exactly when a new build has been deployed and this
  // tab is still on the old bundle.
  //
  // Unknown is not the same as up to date: with no health response there is
  // nothing to compare, so the button stays available rather than claiming a
  // state it cannot see.
  const running = __BUILD_VERSION__
  const served = health?.version ?? null
  const stale = served !== null && served !== running
  return (
    <div className="screen on">
      <div className="screen-head">
        <button className="iconbtn" onClick={onBack}>
          ←
        </button>
        <h2>Settings</h2>
      </div>

      <div className="screen-body">
        <div className="sec">
          <h3>Connection</h3>
          <div className="card">
            <div className="crow">
              <span className={`dot ${CONN_DOT[conn]}`} />
              <div className="lbl">
                {conn === 'live' ? 'Connected' : conn === 'offline' ? 'Offline' : 'Connecting'}
                <small>{location.host}</small>
              </div>
              <button className="linkbtn" onClick={onCheck}>
                Check
              </button>
            </div>
            <div className="crow">
              <div className="lbl">Signed in as</div>
              <div className="val">{health?.login || '—'}</div>
            </div>
            <div className="crow">
              <div className="lbl">Mac</div>
              <div className="val">{health?.hostname || '—'}</div>
            </div>
            <div className="crow">
              <div className="lbl">tmux</div>
              <div className="val">{health ? `${health.tmux} · ${health.panes} panes` : '—'}</div>
            </div>
            <div className="crow">
              <div className="lbl">Last reached</div>
              <div
                className="val"
                style={{ color: conn === 'live' ? 'var(--green)' : 'var(--peach)' }}
              >
                {lastReached ? ago(lastReached) : 'never'}
              </div>
            </div>
          </div>
        </div>

        <div className="sec">
          <h3>Notifications</h3>
          <div className="card">
            {notifState === 'unsupported' && (
              <div className="crow">
                <div className="lbl">
                  Not available
                  <small>needs the home-screen install over https</small>
                </div>
              </div>
            )}
            {notifState === 'denied' && (
              <div className="crow">
                <div className="lbl">
                  Blocked in browser settings
                  <small>allow notifications for this site to re-enable</small>
                </div>
              </div>
            )}
            {notifState === 'default' && (
              <div className="crow">
                <div className="lbl">
                  Not enabled yet
                  <small>one tap, asked once</small>
                </div>
                <button className="linkbtn" onClick={onEnableNotifications}>
                  Enable
                </button>
              </div>
            )}
            <div className="crow">
              <div className="lbl">
                An agent needs an answer
                <small>waiting state</small>
              </div>
              <button
                className={`sw-toggle ${settings.notifyWaiting ? 'on' : ''}`}
                role="switch"
                aria-checked={settings.notifyWaiting}
                aria-label="An agent needs an answer"
                onClick={() => patch({ notifyWaiting: !settings.notifyWaiting })}
              />
            </div>
            <div className="crow">
              <div className="lbl">
                A task finished
                <small>busy → idle</small>
              </div>
              <button
                className={`sw-toggle ${settings.notifyDone ? 'on' : ''}`}
                role="switch"
                aria-checked={settings.notifyDone}
                aria-label="A task finished"
                onClick={() => patch({ notifyDone: !settings.notifyDone })}
              />
            </div>
            <div className="crow">
              <div className="lbl">
                GitHub needs you
                <small>your PR turns red, or a review is asked of you</small>
              </div>
              <button
                className={`sw-toggle ${settings.notifyCi ? 'on' : ''}`}
                role="switch"
                aria-checked={settings.notifyCi}
                aria-label="GitHub needs you"
                onClick={() => patch({ notifyCi: !settings.notifyCi })}
              />
            </div>
          </div>
        </div>

        <div className="sec">
          <h3>GitHub</h3>
          <IgnoreChecks
            names={settings.ignoreChecks}
            onChange={(names) => patch({ ignoreChecks: names })}
          />
        </div>

        <div className="sec">
          <h3>Panes</h3>
          <div className="card">
            <div className="crow">
              <div className="lbl">
                Name each agent pane
                <small>a cheap model reads the screen and writes what it is doing</small>
              </div>
              <button
                className={`sw-toggle ${settings.namePanes ? 'on' : ''}`}
                role="switch"
                aria-checked={settings.namePanes}
                aria-label="Name each agent pane"
                onClick={() => patch({ namePanes: !settings.namePanes })}
              />
            </div>
          </div>
          <NamingActivity />
        </div>

        <div className="sec">
          <h3>Display</h3>
          <div className="card">
            <div className="crow">
              <div className="lbl">
                Wrap lines
                <small>off = exact tmux screen</small>
              </div>
              <button
                className={`sw-toggle ${settings.wrap ? 'on' : ''}`}
                role="switch"
                aria-checked={settings.wrap}
                aria-label="Wrap lines"
                onClick={() => patch({ wrap: !settings.wrap })}
              />
            </div>
            <div className="crow">
              <div className="lbl">Font size</div>
              <div className="val">{settings.fontSize}px</div>
              <span className="stepper">
                <button
                  aria-label="Smaller"
                  onClick={() => patch({ fontSize: Math.max(10, settings.fontSize - 1) })}
                >
                  −
                </button>
                <button
                  aria-label="Larger"
                  onClick={() => patch({ fontSize: Math.min(20, settings.fontSize + 1) })}
                >
                  +
                </button>
              </span>
            </div>
            <div className="crow">
              <div className="lbl">
                Submit with Enter
                <small>off = stage the text, send with the ⏎ key</small>
              </div>
              <button
                className={`sw-toggle ${settings.submitOnEnter ? 'on' : ''}`}
                role="switch"
                aria-checked={settings.submitOnEnter}
                aria-label="Submit with Enter"
                onClick={() => patch({ submitOnEnter: !settings.submitOnEnter })}
              />
            </div>
            <div className="crow">
              <div className="lbl">
                Scrollback
                <small>lines captured per pane</small>
              </div>
              <div className="val">{settings.lines}</div>
              <span className="stepper">
                <button
                  aria-label="Fewer lines"
                  onClick={() => patch({ lines: Math.max(100, settings.lines - 100) })}
                >
                  −
                </button>
                <button
                  aria-label="More lines"
                  onClick={() => patch({ lines: Math.min(2000, settings.lines + 100) })}
                >
                  +
                </button>
              </span>
            </div>
          </div>
        </div>

        {/* A link, not a section. The chip editor grows with every chip added
            and the sections around it do not, so in here it was the thing that
            pushed Connection, Notifications and Display off the screen. */}
        <div className="sec">
          <h3>Key pad</h3>
          <div className="card">
            <button className="crow rowlink" onClick={onKeyPad}>
              <div className="lbl">
                Chips
                <small>
                  {settings.chips.length} custom
                  {settings.hiddenKeys.length > 0 &&
                    `, ${settings.hiddenKeys.length} built-in off`}
                </small>
              </div>
              <span className="val">›</span>
            </button>
          </div>
        </div>

        <div className="sec">
          <h3>About</h3>
          <div className="card">
            <div className="crow">
              <div className="lbl">Remux</div>
              <div className="val">{served ? tag(served) : '—'}</div>
            </div>
            <div className="crow">
              <div className="lbl">
                Update
                {stale && served && <small>{tag(served)} is ready</small>}
              </div>
              <button className="linkbtn" onClick={onUpdate}>
                {stale ? 'Update now' : 'Check for update'}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

/**
 * Checks that do not count as a failure.
 *
 * One noisy check turns a commit's whole rolled-up state red, and the rollup is
 * all a row has room for - so a failing Vercel preview deploy parked two of
 * Kevin's pull requests in "Needs you" with nothing he could do about either.
 * Naming the check is the fix, and it is a preference rather than a per-item
 * decision, so it lives here.
 *
 * The names are still shown, still red, on a pull request's own screen. This
 * only decides what counts as blocked.
 */
function IgnoreChecks({
  names,
  onChange,
}: {
  names: string[]
  onChange: (names: string[]) => void
}) {
  const [draft, setDraft] = useState('')

  const add = () => {
    const v = draft.trim()
    if (!v) return
    // Case-folded, because the server matches that way and two entries that
    // differ only in case would look like a bug.
    if (!names.some((n) => n.toLowerCase() === v.toLowerCase())) onChange([...names, v])
    setDraft('')
  }

  return (
    <div className="card">
      <div className="crow col">
        <div className="lbl">
          Checks that don't count as red
          <small>matched anywhere in the name, case-insensitive</small>
        </div>
        <div className="chipset">
          {names.map((n) => (
            <span className="chip" key={n}>
              {n}
              <button
                className="chip-x"
                aria-label={`Stop ignoring ${n}`}
                onClick={() => onChange(names.filter((x) => x !== n))}
              >
                ✕
              </button>
            </span>
          ))}
          {!names.length && <span className="dim">Every check counts.</span>}
        </div>
        <div className="addrow">
          <input
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && add()}
            placeholder="Check name, e.g. Vercel"
            spellCheck={false}
            autoCorrect="off"
            autoCapitalize="off"
          />
          <button className="addbtn sm" onClick={add} disabled={!draft.trim()}>
            Add
          </button>
        </div>
      </div>
    </div>
  )
}

/**
 * What the pane namer has been doing.
 *
 * Naming is the only part of remux that spends money, and the only one with
 * nothing to look at when it goes wrong. A wrong glyph is visible on the pane;
 * a model call that was never made looks exactly like one that came back with
 * nothing, and both look exactly like the feature working - the panes simply
 * keep whatever names they already had. So the first question anyone asks is
 * "is it actually running", and until now the only answer was to read
 * remux.log over ssh, which holds failures and no successes at all.
 *
 * This shows the run, not just the result: which tier answered, how long it
 * took, how big the prompt was, and every name it wrote. Where a name came
 * from is the part worth the pixels - a screen-fallback name and a model name
 * are indistinguishable on the pane and mean opposite things about whether the
 * chain is alive.
 *
 * It polls only while this screen is up. The whole report is a ring buffer in
 * the server's memory, so the request is cheaper than the render.
 */
function NamingActivity() {
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
    // Slower than a run can finish, which is the point: this is a history, not
    // a progress bar, and the one live thing on it - "asking now" - lasts
    // several seconds.
    const t = setInterval(load, 5000)
    return () => {
      alive = false
      clearInterval(t)
    }
  }, [])

  if (err) return <div className="card"><div className="crow"><div className="lbl dim">{err}</div></div></div>
  if (!n) return null

  const runs = n.runs ?? []
  const chain = n.chain ?? []
  const state = namingState(n)

  return (
    <div className="card">
      <div className="crow">
        <span className={`dot ${state.dot}`} />
        <div className="lbl">
          {state.label}
          <small>{state.detail}</small>
        </div>
      </div>

      {/* Totals since the service started, which is also how long the runs
          below go back at most. */}
      <div className="crow">
        <div className="lbl dim">Since start</div>
        <div className="val">
          {n.calls} {n.calls === 1 ? 'call' : 'calls'} · {n.wrote} named
          {n.failed > 0 && <span className="run-bad"> · {n.failed} failed</span>}
        </div>
      </div>

      {!runs.length ? (
        <div className="crow">
          <div className="lbl dim">
            Nothing yet
            <small>
              {chain.length
                ? 'a pane is asked about at most once every 90 seconds, and only while someone is looking'
                : 'neither claude nor codex was found, so there is nothing to ask'}
            </small>
          </div>
        </div>
      ) : (
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
                  {Math.max(1, Math.round(r.chars / 1000))}k
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
      )}
    </div>
  )
}

/**
 * The one line that answers "is it running".
 *
 * Off and "no CLI installed" are the pair worth separating: they look the same
 * from the phone - no names appearing - and one is a switch, the other is a
 * missing binary on the Mac.
 */
function namingState(n: Naming): { dot: string; label: string; detail: string } {
  if (!(n.chain ?? []).length)
    return { dot: 'stale', label: 'No model found', detail: 'neither claude nor codex is on the path' }
  const chain = (n.chain ?? []).join(' → ')
  if (!n.enabled) return { dot: 'shell', label: 'Off', detail: `switched off above; would use ${chain}` }
  if (n.working) return { dot: 'waiting', label: 'Asking now', detail: chain }
  if (n.retryMs > 0)
    return {
      dot: 'stale',
      label: 'Backing off',
      detail: `every tier failed; trying again in ${Math.max(1, Math.round(n.retryMs / 60000))} min`,
    }
  // Away is the presence gate, and reaching this screen does not clear it: it
  // is the last answer the server cached, refreshed on its own schedule.
  if (n.away) return { dot: 'idle', label: 'Paused', detail: 'nothing is being asked while nobody is reading' }
  return { dot: 'idle', label: 'Watching', detail: chain }
}
