import { useState } from 'react'
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
 *
 * There is no Save button because every keystroke is already saved. That was
 * not obvious to the first person who used it - the first thing they looked
 * for was Save, and pressing Add produced a second blank row, which read as
 * "the first one was not committed". Two things fix the reading rather than
 * the behaviour: Add is disabled until the row above it is usable, so it can
 * never look like the way to commit, and the section says outright that
 * typing is saving.
 *
 * One row is open at a time. Showing every control for every chip cost about
 * 178px per chip on a 390px phone - two stacked inputs at the 16px the iOS
 * zoom guard forces, then eight controls wrapping onto two lines - so four
 * chips filled the settings screen and each new one made it worse. Collapsed,
 * a chip is a 44px line that says what it is called and what it sends, which
 * is what you are scanning for when you are not editing.
 *
 * Add opens the row it just made, so the model above survives intact: the new
 * chip is still a blank row you fill in, it is just the only one expanded. A
 * chip you leave half-filled says so on its summary line rather than looking
 * like a working chip that never appears in the pad.
 */
function ChipEditor({
  chips,
  patch,
}: {
  chips: CustomChip[]
  patch: (p: Partial<Settings>) => void
}) {
  // The open row is tracked by id, not by index, so moving a chip up or down
  // keeps the one you are editing open instead of handing it to its neighbour.
  const [open, setOpen] = useState<string | null>(null)
  const write = (next: CustomChip[]) => patch({ chips: next })
  const edit = (i: number, p: Partial<CustomChip>) =>
    write(chips.map((c, n) => (n === i ? { ...c, ...p } : c)))
  // Adding while the last row is still blank is what made this look unsaved.
  const last = chips[chips.length - 1]
  const incomplete = last !== undefined && (last.label.trim() === '' || last.text === '')
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
        <div className="crow">
          <div className="lbl">
            Your chips
            <small>saved as you type, no save button</small>
          </div>
        </div>
        {chips.map((c, i) => (
          <div className="crow chip-row" key={c.id}>
            <div className="chip-head">
              {/* The whole line opens the row - the chevron is a hint, not the
                  target. Remove is a sibling and not a child, because a button
                  inside a button is invalid, and a delete that has to
                  stopPropagation to avoid also toggling the row is a trap
                  waiting for the next person to move it. */}
              <button
                className="chip-summary"
                aria-expanded={open === c.id}
                onClick={() => setOpen(open === c.id ? null : c.id)}
              >
                <span className="chip-sum-label">{c.label.trim() || 'new chip'}</span>
                {c.label.trim() !== '' && c.text !== '' ? (
                  <span className="chip-sum-text">{c.text}</span>
                ) : (
                  // A chip missing either half does not render in the pad. That
                  // is deliberate, but it used to be invisible: the row looked
                  // finished and the chip simply never appeared.
                  <span className="chip-sum-text blank">not shown yet</span>
                )}
                <svg
                  viewBox="0 0 24 24"
                  width="13"
                  height="13"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2.6"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  aria-hidden="true"
                >
                  <path d="M5 9l7 7 7-7" />
                </svg>
              </button>
              <button
                className="chip-del"
                aria-label={`Remove ${c.label.trim() || 'chip'}`}
                onClick={() => write(chips.filter((_, n) => n !== i))}
              >
                ✕
              </button>
            </div>
            {open === c.id && (
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
              {/* These four settings were explained in `title` attributes,
                  which is the same as not explaining them: title only appears
                  on hover, and this app runs on a phone. Now that one row is
                  open at a time there is room to say it on the screen. */}
              <div className="chip-opts">
                <button
                  className={`chiptog ${c.command ? 'on' : ''}`}
                  aria-pressed={c.command}
                  onClick={() => edit(i, { command: !c.command })}
                >
                  clear first
                </button>
                <button
                  className={`chiptog ${c.wide ? 'on' : ''}`}
                  aria-pressed={c.wide}
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
                </span>
              </div>
              <p className="chip-help">
                <b>clear first</b> wipes whatever is on the input line before sending, the
                way /clear does. Slash commands only register on an empty line.
                <br />
                <b>wide</b> gives the chip two of the pad's eight columns, for a label
                that will not fit one.
              </p>

              <div className="chip-opts">
                {/* One choice with one answer, so a segmented group rather
                    than three switches that could all be off. */}
                <span className="chip-on">
                  {(['all', 'agent', 'shell'] as const).map((v) => (
                    <button
                      key={v}
                      className={(c.on ?? 'all') === v ? 'on' : ''}
                      aria-pressed={(c.on ?? 'all') === v}
                      onClick={() => edit(i, { on: v })}
                    >
                      {v === 'all' ? 'all' : v === 'agent' ? 'agents' : 'shells'}
                    </button>
                  ))}
                </span>
              </div>
              <p className="chip-help">
                Which panes show it. <b>agents</b> means Claude Code and Codex;{' '}
                <b>shells</b> means a plain prompt. Anything else - vim, a pager, python -
                counts as neither, so only <b>all</b> reaches it.
              </p>
            </div>
            )}
          </div>
        ))}
        <div className="crow">
          <div className="lbl">
            Add a chip
            <small>
              {incomplete
                ? 'fill in the label and text above first'
                : 'shows first when the pad is expanded'}
            </small>
          </div>
          <button
            className="linkbtn"
            disabled={incomplete}
            onClick={() => {
              // crypto.randomUUID needs a secure context. The tailnet is one
              // and so is localhost, but --local over plain HTTP to a LAN
              // address is not, and that is a real way to open this.
              const id =
                globalThis.crypto?.randomUUID?.() ??
                `chip-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
              // Open it. Adding a row you then have to tap to fill in would be
              // two steps where there was one.
              setOpen(id)
              write([
                ...chips,
                {
                  id,
                  label: '',
                  text: '',
                  command: false,
                  wide: false,
                  on: 'all',
                },
              ])
            }}
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
