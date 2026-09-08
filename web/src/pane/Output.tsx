import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { ansiToHtml } from '../ansi'

/**
 * The real tmux screen, rendered with its real colours.
 *
 * Nothing here parses the output into speaker turns. That is what makes the
 * app work with any agent and any TUI on day one, and what stops it breaking
 * when Codex or Claude Code changes how it renders.
 */
export function Output({ lines, wrap }: { lines: string[]; wrap: boolean }) {
  const box = useRef<HTMLElement>(null)
  const [stuck, setStuck] = useState(true)

  const html = ansiToHtml(lines.join('\n'))

  // Auto-stick to the bottom, but hold position if the reader has scrolled up
  // to read something - new output must not yank the page away from them.
  useLayoutEffect(() => {
    const el = box.current
    if (el && stuck) el.scrollTop = el.scrollHeight
  }, [html, stuck])

  useEffect(() => {
    const el = box.current
    if (!el) return
    const on = () => setStuck(el.scrollHeight - el.scrollTop - el.clientHeight <= 40)
    el.addEventListener('scroll', on, { passive: true })
    return () => el.removeEventListener('scroll', on)
  }, [])

  const toBottom = () => {
    const el = box.current
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' })
  }

  return (
    <>
      <main className="output" ref={box}>
        {/* The HTML is built by ansiToHtml, which escapes all pane text and
            only emits spans and anchors it constructed itself. */}
        <pre
          style={wrap ? undefined : { whiteSpace: 'pre', wordBreak: 'normal' }}
          dangerouslySetInnerHTML={{ __html: html }}
        />
      </main>
      <button className={`jump ${stuck ? '' : 'show'}`} onClick={toBottom}>
        ↓ Jump to latest
      </button>
    </>
  )
}
