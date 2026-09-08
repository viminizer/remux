import { useEffect, useRef, useState } from 'react'
import { displayCommand } from '../types'

const SHELLS = new Set(['zsh', 'bash', 'fish', 'sh', 'dash', 'ksh', 'nu'])

/**
 * Auto-growing textarea, labelled with the target so it is always obvious
 * where the text is going.
 *
 * "Submit with Enter" is a setting rather than a constant, because some agents
 * want the text staged first and answered with a key afterwards.
 */
export function Composer({
  target,
  disabled,
  submitOnEnter,
  onSend,
}: {
  target: string
  disabled: boolean
  submitOnEnter: boolean
  onSend: (text: string, submit: boolean) => void
}) {
  const [text, setText] = useState('')
  const ta = useRef<HTMLTextAreaElement>(null)

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
    setText('')
  }

  const name = displayCommand(target)
  const placeholder = SHELLS.has(name) ? 'Type a command…' : `Message ${name}…`

  return (
    <div className="composer">
      <div className="wrap">
        <textarea
          ref={ta}
          rows={1}
          value={text}
          disabled={disabled}
          placeholder={placeholder}
          spellCheck={false}
          autoCorrect="off"
          autoCapitalize="sentences"
          onChange={(e) => setText(e.target.value)}
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
