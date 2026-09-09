import { useEffect, useRef } from 'react'
import { displayCommand, isShell } from '../types'

/**
 * Auto-growing textarea, labelled with the target so it is always obvious
 * where the text is going.
 *
 * "Submit with Enter" is a setting rather than a constant, because some agents
 * want the text staged first and answered with a key afterwards.
 *
 * The text lives in App, not here. One Composer is reused for every pane, so
 * state held here followed you between them: half a prompt typed to an agent
 * turned up in the box under a shell, aimed at the wrong process and one tap
 * from being sent there. App keys the drafts by pane; this stays a controlled
 * field.
 */
export function Composer({
  target,
  disabled,
  submitOnEnter,
  text,
  onChange,
  onSend,
  onTyping,
}: {
  target: string
  disabled: boolean
  submitOnEnter: boolean
  text: string
  onChange: (text: string) => void
  onSend: (text: string, submit: boolean) => void
  /**
   * Focus in and out. This is the app's only reading of whether the software
   * keyboard is up: it is the only field on the pane screen, and no browser
   * reports the keyboard directly - visualViewport shrinks for it on iOS but
   * not on Android, and the layout viewport does the opposite, so focus is both
   * simpler and the thing actually being asked about.
   */
  onTyping: (typing: boolean) => void
}) {
  const ta = useRef<HTMLTextAreaElement>(null)

  // Also the thing that resizes the box back down when the draft changes
  // under it on a pane switch, since the height is written inline.
  useEffect(() => {
    const el = ta.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(110, el.scrollHeight) + 'px'
  }, [text])

  const send = () => {
    const t = text.trim()
    if (!t || disabled) return
    onSend(t, submitOnEnter)
    onChange('')
  }

  const name = displayCommand(target)
  const placeholder = isShell(target) ? 'Type a command…' : `Message ${name}…`

  return (
    <div className="composer">
      <div className="wrap">
        {/* No autocapitalise. `sentences` treats an empty field as the start of
            one and spends the shift on the letter after a leading `/`, so a
            typed slash command or skill call arrives as `/Compact` and the
            agent reads it as a message rather than running it. Prompts, paths
            and shell commands all care about case; a prose capital is the only
            thing typed here that does not. Every other field in the app is
            already off. */}
        <textarea
          ref={ta}
          rows={1}
          value={text}
          disabled={disabled}
          placeholder={placeholder}
          spellCheck={false}
          autoCorrect="off"
          autoCapitalize="off"
          onFocus={() => onTyping(true)}
          onBlur={() => onTyping(false)}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={(e) => {
            // Shift+Enter always means "new line", on every platform.
            if (e.key === 'Enter' && !e.shiftKey && submitOnEnter && !isTouch()) {
              e.preventDefault()
              send()
            }
          }}
        />
      </div>
      <button className="send" disabled={disabled || !text.trim()} onClick={send}>
        ➤
      </button>
    </div>
  )
}

// On a phone the Enter key has to insert a newline, or a multi-line prompt is
// impossible to type. The send button is the only way to submit there.
function isTouch(): boolean {
  return window.matchMedia('(pointer: coarse)').matches
}
