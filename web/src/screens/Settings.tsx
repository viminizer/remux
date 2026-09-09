import type { Health, Conn } from '../types'
import type { Settings, CustomChip } from '../store'
import { ago } from '../store'

/**
 * The key pad chips someone added themselves.
 *
 * Edited in place rather than through an add form and a separate edit path:
 * Add appends a blank row and you fill it in, so there is one code path and a
 * typo costs a tap rather than a delete and a retype. A half-filled row simply
 * does not render in the pad, which makes the blank row a normal state instead
 * of something to validate.
 */
function ChipEditor({
  chips,
  patch,
}: {
  chips: CustomChip[]
  patch: (p: Partial<Settings>) => void
}) {
  const write = (next: CustomChip[]) => patch({ chips: next })
  const edit = (i: number, p: Partial<CustomChip>) =>
    write(chips.map((c, n) => (n === i ? { ...c, ...p } : c)))
  const move = (i: number, by: number) => {
    const to = i + by
    if (to < 0 || to >= chips.length) return
    const next = [...chips]
    ;[next[i], next[to]] = [next[to], next[i]]
    write(next)
  }

  return (
    <div className="sec">
      <h3>Key pad</h3>
      <div className="card">
        {chips.length === 0 && (
          <div className="crow">
            <div className="lbl">
              No chips yet
              <small>a chip sends its text to the pane in one tap</small>
            </div>
          </div>
        )}
        {chips.map((c, i) => (
          <div className="crow chip-row" key={c.id}>
            <div className="chip-fields">
              <input
                className="chip-label"
                value={c.label}
                placeholder="label"
                spellCheck={false}
                autoCapitalize="off"
                autoCorrect="off"
                aria-label={`Chip ${i + 1} label`}
                onChange={(e) => edit(i, { label: e.target.value })}
              />
              <input
                className="chip-text"
                value={c.text}
                placeholder="text to send"
                spellCheck={false}
                autoCapitalize="off"
                autoCorrect="off"
                aria-label={`Chip ${i + 1} text`}
                onChange={(e) => edit(i, { text: e.target.value })}
              />
              <div className="chip-opts">
                <button
                  className={`chiptog ${c.command ? 'on' : ''}`}
                  aria-pressed={c.command}
                  title="Clear the input line before sending, the way /clear does"
                  onClick={() => edit(i, { command: !c.command })}
                >
                  clear first
                </button>
                <button
                  className={`chiptog ${c.wide ? 'on' : ''}`}
                  aria-pressed={c.wide}
                  title="Take two columns in the grid"
                  onClick={() => edit(i, { wide: !c.wide })}
                >
                  wide
                </button>
                <span className="chip-move">
                  <button aria-label="Move up" disabled={i === 0} onClick={() => move(i, -1)}>
                    ↑
                  </button>
                  <button
                    aria-label="Move down"
                    disabled={i === chips.length - 1}
                    onClick={() => move(i, 1)}
                  >
                    ↓
                  </button>
                  <button
                    aria-label="Remove chip"
                    onClick={() => write(chips.filter((_, n) => n !== i))}
                  >
                    ✕
                  </button>
                </span>
              </div>
            </div>
          </div>
        ))}
        <div className="crow">
          <div className="lbl">
            Add a chip
            <small>shows first when the pad is expanded</small>
          </div>
          <button
            className="linkbtn"
            onClick={() =>
              write([
                ...chips,
                {
                  // crypto.randomUUID needs a secure context. The tailnet is
                  // one and so is localhost, but --local over plain HTTP to a
                  // LAN address is not, and that is a real way to open this.
                  id:
                    globalThis.crypto?.randomUUID?.() ??
                    `chip-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
                  label: '',
                  text: '',
                  command: false,
                  wide: false,
                },
              ])
            }
          >
            Add
          </button>
        </div>
      </div>
    </div>
  )
}

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

        <ChipEditor chips={settings.chips} patch={patch} />

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
