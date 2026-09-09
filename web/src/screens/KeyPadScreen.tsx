import { useState } from 'react'
import type { Settings, CustomChip } from '../store'
import { CHIPS, COLUMNS } from '../pane/KeyPad'

/**
 * Everything about the key pad, on its own screen.
 *
 * It used to be a section of Settings, and it was the section that pushed
 * everything else off the screen: the chip editor grows with each chip added,
 * while Connection, Notifications and Display are fixed. A screen of its own
 * costs one tap from Settings and gives the part that grows room to grow.
 *
 * Two things live here. The built-ins can be turned off, because eighteen
 * chips is a lot and which of them are worth a cell is a matter of how you
 * work - `y`/`n`/`1`/`2`/`3` answer an agent's prompt and mean nothing if you
 * never get one. And the custom chips are added and edited, as before.
 */

/** The targets a custom chip can be pinned to, in the order they are shown. */
const ON_TARGETS: [CustomChip['on'], string][] = [
  ['all', 'all'],
  ['claude', 'claude'],
  ['codex', 'codex'],
  ['agent', 'agents'],
  ['shell', 'shells'],
]

/**
 * The built-ins, shown as the grid they actually form so that turning one off
 * is a visible edit to the thing you use rather than a switch in a list.
 *
 * Two grids and not one, because the split is the point: the first row is
 * always on screen and the rest costs a tap on the chevron. The first row also
 * keeps its order no matter what is turned off - these are the eight you reach
 * for without looking, and reflowing them would take away the only reason they
 * are pinned. Turning one off shortens the row; it does not reshuffle it.
 */
function BuiltIns({
  hidden,
  patch,
}: {
  hidden: string[]
  patch: (p: Partial<Settings>) => void
}) {
  const toggle = (k: string) =>
    patch({ hiddenKeys: hidden.includes(k) ? hidden.filter((h) => h !== k) : [...hidden, k] })

  const grid = (chips: typeof CHIPS) => (
    <div className="chipgrid">
      {chips.map((c) => {
        const off = hidden.includes(c.k)
        return (
          <button
            key={c.k}
            className={`key ${c.wide ? 'wide' : ''} ${c.danger ? 'danger' : ''} ${off ? 'off' : ''}`}
            aria-pressed={!off}
            aria-label={`${c.label}, ${off ? 'off' : 'on'}`}
            onClick={() => toggle(c.k)}
          >
            {c.label}
          </button>
        )
      })}
    </div>
  )

  const onCount = CHIPS.filter((c) => !hidden.includes(c.k)).length

  return (
    <div className="sec">
      <h3>Built-in chips</h3>
      <div className="card">
        <div className="crow">
          <div className="lbl">
            Tap one to turn it off
            <small>
              {onCount} of {CHIPS.length} on
            </small>
          </div>
        </div>
        <div className="crow chip-grid-row">
          <div className="lbl">
            Always shown
            <small>the row above the composer</small>
          </div>
        </div>
        <div className="crow chip-grid-row">{grid(CHIPS.slice(0, COLUMNS))}</div>
        <div className="crow chip-grid-row">
          <div className="lbl">
            Behind the chevron
            <small>some of these hide themselves on panes they are useless on</small>
          </div>
        </div>
        <div className="crow chip-grid-row">{grid(CHIPS.slice(COLUMNS))}</div>
      </div>
    </div>
  )
}

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
      <h3>Your chips</h3>
      <div className="card">
        <div className="crow">
          <div className="lbl">
            Added by you
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
                // Labels repeat by design, so the label alone does not say
                // which chip this removes. The text is what tells them apart.
                aria-label={`Remove ${c.label.trim() || 'chip'}${c.text ? `, sends ${c.text}` : ''}`}
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
                <b>clear first</b> wipes whatever is already on the input line before
                sending. Slash commands only register on an empty line, so turn this on
                for anything starting with /.
                <br />
                <b>wide</b> gives the chip two of the pad's eight columns, for a label
                that will not fit one.
              </p>

              <div className="chip-opts">
                {/* One choice with one answer, so a segmented group rather
                    than three switches that could all be off. */}
                <span className="chip-on">
                  {ON_TARGETS.map(([v, label]) => (
                    <button
                      key={v}
                      className={(c.on ?? 'all') === v ? 'on' : ''}
                      aria-pressed={(c.on ?? 'all') === v}
                      onClick={() => edit(i, { on: v })}
                    >
                      {label}
                    </button>
                  ))}
                </span>
              </div>
              <p className="chip-help">
                Which panes show it. <b>claude</b> and <b>codex</b> are named on their own
                because a skill call is a slash on one and a dollar on the other, so a
                chip written for one is dead text on the other. <b>agents</b> is both of
                them plus aider and the rest; <b>shells</b> is a plain prompt. Anything
                else - vim, a pager, python - is none of those, so only <b>all</b> reaches
                it.
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


export function KeyPadScreen({
  settings,
  patch,
  onBack,
}: {
  settings: Settings
  patch: (p: Partial<Settings>) => void
  onBack: () => void
}) {
  return (
    <div className="screen on">
      <div className="screen-head">
        <button className="iconbtn" onClick={onBack}>
          ←
        </button>
        <h2>Key pad</h2>
      </div>

      <div className="screen-body">
        <BuiltIns hidden={settings.hiddenKeys} patch={patch} />
        <ChipEditor chips={settings.chips} patch={patch} />
      </div>
    </div>
  )
}
