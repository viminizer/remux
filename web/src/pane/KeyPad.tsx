/**
 * The key chips above the composer, in two states.
 *
 * It used to be one row that scrolled sideways. That stopped scaling at about
 * eight chips on a phone: everything past the eighth needed a horizontal swipe
 * to a position you could not predict, and horizontal scroll gives no hint
 * anything is out there. There are 18 built-ins now, plus whatever has been
 * added on the key pad screen.
 *
 * So it is one eight-column grid, and the states differ only in how much of it
 * is rendered: collapsed is the first row, expanded is all of it, and while the
 * keyboard is up it is none of it. Nothing scrolls in any of them. Expanded
 * pushes the output up rather than covering it, because covering the screen you
 * are reading while you decide what to send is the wrong trade.
 *
 * A chip is one of three things. A key name goes through the keys endpoint,
 * which is allowlisted server-side, so this list and KeyAllowlist in
 * internal/tmux/actions.go have to agree. A `text` chip is a literal character
 * and goes through the text endpoint instead - the same path the composer
 * uses, so it needs no server change and does not widen the allowlist, whose
 * whole job is keeping arbitrary strings out of send-keys.
 *
 * A `command` chip is a `text` chip that clears the input line first. Slash
 * commands only register at the start of an empty composer, so typing one into
 * a half-written prompt produces a line that silently does nothing. No
 * built-in is one any more - the two that were are better made by hand - but
 * a custom chip can be.
 *
 * Which chips a pane gets is `hideOn` against paneKind, and which exist at all
 * is the key pad screen: `hidden` turns a built-in off, `custom` adds one.
 */
import { useEffect, useLayoutEffect, useRef } from 'react'
import type { CustomChip } from '../store'
import type { PaneKind } from '../types'
import { paneKind } from '../types'

export type Chip = {
  /**
   * What gets sent. For a key chip this is the tmux key name; for a text or
   * command chip it is the literal string.
   */
  k: string
  /**
   * React key. Two custom chips may legitimately send the same string, so
   * identity cannot come from `k`. Built-ins are unique and fall back to it.
   */
  id?: string
  label: string
  /** Span two grid columns, for a label that will not fit an eighth. */
  wide?: boolean
  /**
   * Start this chip at column 1, breaking the row before it.
   *
   * Set by the pad, never by a definition: it marks the first custom chip, so
   * that block always begins on a line of its own.
   */
  newrow?: boolean
  /**
   * Pane kinds this chip is not worth a cell on. Absent means every pane.
   *
   * A hide-list rather than a show-list so that each entry is a claim someone
   * can check: `hideOn: ['shell']` says "useless at a shell prompt", which is
   * either true or not. A show-list would silently hide the chip from every
   * kind nobody thought to name.
   */
  hideOn?: PaneKind[]
  text?: boolean
  command?: boolean
  danger?: boolean
  /**
   * Whether this chip is pressed over and over.
   *
   * Two things follow from it. The panel does not auto-collapse after one -
   * arrows get tapped three or four times to walk back through history, and ⌫
   * more than that - and holding the chip repeats it. One-shot chips do
   * neither, because after them you are going back to reading.
   */
  repeat?: boolean
}

/** The grid's width in cells. */
export const COLUMNS = 8

/**
 * How many entries of CHIPS make the collapsed row.
 *
 * Not the same number as COLUMNS any more, and that is the whole reason it
 * exists: esc and ⏎ take two cells each, so six chips fill the eight. These
 * two have to stay in step - the first COLLAPSED chips must come to exactly
 * COLUMNS cells, or the collapsed state renders a partial row or spills into
 * the next one.
 */
export const COLLAPSED = 6

/**
 * One flat list in grid order. The first COLLAPSED entries are the row that is
 * always on screen.
 *
 * esc and ⏎ are the two that are worth double: they are the answer to most of
 * what an agent puts on the screen, they are pressed without looking, and a
 * thumb reaching the bottom of a phone finds the far edges before it finds the
 * middle. So esc anchors the left of the row and ⏎ the right, and the four
 * single cells between them - tab, ⌫, $ and / - are the ones that cost a
 * modifier layer on the iOS and Android keyboards, which is the cost these
 * chips exist to remove.
 *
 * ↑ ↓ used to hold the right end and now lead the second row. They are worth a
 * permanent slot far less than ⏎ is: walking back through history is a thing
 * you do deliberately, with the pad already open.
 *
 * Eighteen chips over twenty cells, so the grid is 8 / 8 / 4 before any custom
 * chip is added and before hideOn drops any. Reordering this changes the
 * layout.
 */
export const CHIPS: Chip[] = [
  { k: 'Escape', label: 'esc', wide: true },
  { k: 'Tab', label: 'tab', repeat: true },
  { k: 'BSpace', label: '⌫', repeat: true },
  { k: '$', label: '$', text: true },
  { k: '/', label: '/', text: true },
  { k: 'Enter', label: '⏎', wide: true },
  // ── everything below is behind the chevron ──
  { k: 'Up', label: '↑', repeat: true },
  { k: 'Down', label: '↓', repeat: true },
  // /clear and /compact were here, and are not any more. They were the only
  // built-ins that were somebody's particular workflow rather than a key a
  // terminal answers to, and now that a chip can be defined on the key pad
  // screen - `command` and `wide` and all - shipping two opinions about which
  // slash commands matter is worse than shipping none. Anyone who wants them
  // makes them, alongside the ones this list would never have guessed.
  //
  // What they leave behind is used by custom chips and stays: `command`,
  // `wide`, and paneKind naming Claude Code and Codex separately - which no
  // built-in needs, and which is exactly what a hand-made skill chip needs,
  // since the two spell a skill call differently.
  { k: 'Left', label: '←', repeat: true },
  { k: 'Right', label: '→', repeat: true },
  // Answers to an agent's prompt. Hidden only at a shell, where you would
  // type them; kept on `other`, because a digit is a count in vim and `y` is
  // yank, and those are real uses rather than leftovers.
  { k: 'y', label: 'y', hideOn: ['shell'] },
  { k: 'n', label: 'n', hideOn: ['shell'] },
  { k: '1', label: '1', hideOn: ['shell'] },
  { k: '2', label: '2', hideOn: ['shell'] },
  { k: '3', label: '3', hideOn: ['shell'] },
  { k: 'BTab', label: '⇧tab', repeat: true },
  // ^U is the one that answers the common case - accept a suggestion with
  // tab, change your mind, clear the line in one tap. Neither it nor ⌫ is
  // `danger`; they touch the input line, not the process, and ^C stays the
  // only red chip.
  { k: 'C-u', label: '^U' },
  { k: 'C-c', label: '^C', danger: true },
]

/**
 * A custom chip states where it belongs; the pad filters on where a chip does
 * not. This is the one place the two meet.
 *
 * Every one of these excludes `other`, unlike the built-ins: someone who
 * picked a target on the key pad screen said what they meant, and stretching
 * it to cover vim and python would be second-guessing them.
 *
 * `claude` and `codex` are their own targets because a skill call is spelled
 * differently on each - a slash against a dollar - and that is the single most
 * common thing a custom chip is for. `agent` is the wider answer, for a chip
 * that suits any of them.
 */
const HIDE_FOR: Record<CustomChip['on'], PaneKind[]> = {
  all: [],
  claude: ['codex', 'agent', 'shell', 'other'],
  codex: ['claude', 'agent', 'shell', 'other'],
  agent: ['shell', 'other'],
  shell: ['claude', 'codex', 'agent', 'other'],
}

export function KeyPad({
  onKey,
  onText,
  onCommand,
  expanded,
  onToggle,
  disabled,
  typing,
  custom,
  hidden,
  command,
}: {
  onKey: (k: string) => void
  onText: (t: string) => void
  onCommand: (t: string) => void
  expanded: boolean
  onToggle: () => void
  disabled: boolean
  /** Whether the composer has focus - on a phone, whether the keyboard is up. */
  typing: boolean
  custom: CustomChip[]
  /** `k` of every built-in turned off on the key pad screen. */
  hidden: string[]
  /** pane_current_command of the pane on screen, for choosing chips. */
  command: string
}) {
  // Custom chips come last, on a row of their own. They used to lead the
  // expanded area, which put them first under the thumb but mixed them into
  // the built-ins: a row that was half keys and half whatever you had defined,
  // reflowing every time one was added. A block of their own is easier to aim
  // at precisely because its contents are yours and its position does not move
  // when a pane switch drops a built-in.
  //
  // A half-filled chip is dropped rather than rendered. The key pad screen
  // adds a blank row for you to fill in, so an incomplete one is a normal
  // intermediate state, not an error worth reporting.
  const extra: Chip[] = custom
    .filter((c) => c.label.trim() !== '' && c.text !== '')
    .map((c) => ({
      k: c.text,
      id: c.id,
      label: c.label,
      wide: c.wide,
      text: !c.command,
      command: c.command,
      hideOn: HIDE_FOR[c.on ?? 'all'],
    }))

  // Filtering stops at the collapsed row. Those six are the same chips in the
  // same places on every pane, which is what makes them usable without
  // looking; letting a pane switch reflow them would cost more than the cells
  // it saved. They are also the six that are useful everywhere - esc, tab,
  // backspace, $, / and enter - so nothing is given up by exempting them.
  const kind = paneKind(command)
  const visible = (c: Chip) => !c.hideOn?.includes(kind)

  // Turning a built-in off is a decision, not a guess about the pane, so it
  // applies to the collapsed row too - the exemption above is only from
  // hideOn. The row keeps its order and simply gets shorter: a hole would say
  // "something is missing here", and nothing is.
  const on = (c: Chip) => !hidden.includes(c.k)
  const first = CHIPS.slice(0, COLLAPSED).filter(on)

  // The first custom chip carries `newrow`, which starts it at column 1 and so
  // pushes the whole block onto a fresh line. Doing it on the chip rather than
  // with a spacer element keeps the pad one grid, which is what the height
  // measurement and the two-state render both rest on.
  const mine = extra.filter(visible)
  if (mine.length) mine[0] = { ...mine[0], newrow: true }

  // While the software keyboard is up, the pad is the chevron and nothing else.
  //
  // The keyboard already takes about a third of the screen, and the collapsed
  // row costs another 53px of what is left - three lines of the output you are
  // writing about. It is also the part worth least at that moment: four of its
  // six chips are tab, backspace, $ and /, all of which the keyboard covering
  // the screen has too, and the other two are one tap on the chevron away.
  //
  // `expanded` is left alone rather than cleared, so dismissing the keyboard
  // puts back the pad you had rather than a collapsed one. The rule is the same
  // sentence in both directions: the keyboard and the pad do not share the
  // screen.
  const shown = typing
    ? []
    : expanded
      ? [...first, ...CHIPS.slice(COLLAPSED).filter(on).filter(visible), ...mine]
      : first

  // What the chevron says has to be what is on the screen and not what is
  // saved. Hidden behind the keyboard, the pad is closed as far as anyone
  // looking at it is concerned.
  const open = expanded && !typing

  const press = (key: Chip) => {
    if (key.command) onCommand(key.k)
    else if (key.text) onText(key.k)
    else onKey(key.k)
    if (expanded && !key.repeat) onToggle()
  }

  // Hold a `repeat` chip and it repeats.
  //
  // Deleting is 47% of every key this app has ever sent - 99 backspaces and 43
  // ^U out of 300 - and each one of those backspaces was a separate tap and a
  // separate round trip to the laptop. ^U already answers "clear the whole
  // line" in one tap; this answers "take back the last few characters", which
  // is what the other 99 were.
  //
  // 400ms before the first repeat, so a normal tap can never trigger one, then
  // ten a second. Faster would be closer to a terminal's own key repeat, but
  // every one of these is an HTTP request over the tailnet rather than a
  // keystroke on a wire, and ten a second already clears a word per second.
  const REPEAT_AFTER = 400
  const REPEAT_EVERY = 100

  const timer = useRef<ReturnType<typeof setTimeout>>()
  // Whether the hold sent anything. The click that ends a hold has to be
  // swallowed, or every hold would send one extra on release.
  const held = useRef(false)
  // Losing the connection mid-hold puts pointer-events:none on the pad, so the
  // pointerup that would have ended the hold never arrives. The tick has to be
  // able to see that for itself, and it cannot read `disabled` - it closes over
  // the render the hold started in, where the connection was still up.
  const dead = useRef(disabled)
  dead.current = disabled

  const stopHold = () => {
    if (timer.current) clearTimeout(timer.current)
    timer.current = undefined
  }
  // A chip can be unmounted mid-hold: switching panes re-filters the grid, and
  // the pad hides itself the moment the composer takes focus.
  useEffect(() => stopHold, [])

  const startHold = (key: Chip) => {
    if (!key.repeat || disabled) return
    held.current = false
    const tick = (delay: number) => {
      timer.current = setTimeout(() => {
        if (dead.current) return stopHold()
        held.current = true
        press(key)
        tick(REPEAT_EVERY)
      }, delay)
    }
    tick(REPEAT_AFTER)
  }

  // Opening and closing the pad is a jump cut without this: the output above
  // it moves 70-odd pixels in one frame and you have to re-find where you
  // were reading.
  //
  // The height is measured rather than written down, because the two states
  // do not have fixed heights any more. A pane switch filters chips by kind,
  // Settings adds and removes custom ones, a `wide` chip that will not fit
  // wraps and leaves a hole, and a coarse pointer makes every row 6px taller.
  // A pair of numbers in the stylesheet would be wrong on the first of those
  // and silently wrong on the rest, so the pad is pinned to a measured height
  // at all times: measure the one the new chips want, put back the one we
  // came from, and let the transition cover the gap.
  //
  // It stays pinned rather than going back to `auto` at the end, so the
  // height we came from is always readable from the element itself and never
  // has to be remembered across a render. Nothing that changes the natural
  // height happens without a render - the row count is the same in portrait
  // and landscape, since the grid is eight columns wide either way - so there
  // is no resize case to catch.
  //
  // No dependency array on purpose: any commit can change the chip set, and
  // all of them should re-measure.
  const pad = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    const el = pad.current
    if (!el) return
    const from = el.style.height
    el.style.height = 'auto'
    const to = el.offsetHeight + 'px'
    if (from && from !== to) {
      el.style.height = from
      // Read the layout back, or both writes land in one frame and the
      // browser transitions nothing.
      void el.offsetHeight
    }
    el.style.height = to
  })

  return (
    // The chevron is a sibling of .keypad, never a child: it floats above the
    // pad's top edge, which a grid item cannot do.
    <div className="keypad-wrap">
      {/* `empty` takes away the padding and the top border, so a pad with no
          chips in it is 0px rather than a 17px strip of nothing. It is not only
          the typing case: turn every built-in off on the key pad screen and the
          collapsed row is empty too. */}
      <div ref={pad} className={`keypad ${shown.length ? '' : 'empty'} ${open ? 'open' : ''}`}>
        {shown.map((key) => (
          <button
            key={key.id ?? key.k}
            className={`key ${key.wide ? 'wide' : ''} ${key.newrow ? 'newrow' : ''} ${key.danger ? 'danger' : ''}`}
            disabled={disabled}
            // Pointer events start and stop the hold; the click is still what
            // sends a single press, so a tap behaves exactly as it did and a
            // keyboard activation - which fires no pointer events at all -
            // keeps working.
            onPointerDown={() => startHold(key)}
            onPointerUp={stopHold}
            onPointerLeave={stopHold}
            onPointerCancel={stopHold}
            onClick={() => {
              if (held.current) {
                held.current = false
                return
              }
              press(key)
            }}
          >
            {key.label}
          </button>
        ))}
      </div>
      {/* Not disabled with the chips. Expanding sends nothing to the pane, so
          there is no reason a stale connection should stop you looking at what
          you could send once it comes back. */}
      <button
        className={`kp-toggle ${open ? 'open' : ''}`}
        onClick={onToggle}
        aria-expanded={open}
        aria-label={open ? 'Collapse the key pad' : 'Show all keys'}
      >
        {/* Two chevrons, not one: a single one reads as "scroll up". Drawn
            rather than typed because ⌃ is the only caret with usable metrics
            and there is no double of it. */}
        <svg
          viewBox="0 0 24 24"
          width="14"
          height="14"
          fill="none"
          stroke="currentColor"
          strokeWidth="2.6"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <path d="M5 11 12 5l7 6" />
          <path d="M5 19 12 13l7 6" />
        </svg>
      </button>
    </div>
  )
}
