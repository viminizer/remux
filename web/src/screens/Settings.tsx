import type { Health, Conn } from '../types'
import type { Settings } from '../store'
import { ago } from '../store'

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
          </div>
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

        <div className="sec">
          <h3>About</h3>
          <div className="card">
            <div className="crow">
              <div className="lbl">Remux</div>
              <div className="val">{served ?? '—'}</div>
            </div>
            <div className="crow">
              <div className="lbl">
                Update
                {stale && <small>version {served} is ready</small>}
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
