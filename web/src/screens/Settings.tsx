import { useState } from 'react'
import type { Health, Conn } from '../types'
import type { Settings } from '../store'
import { ago } from '../store'

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
