import { useState } from 'react'

import type { HarnessItem } from './types'
import { itemURL } from './types'

/**
 * Answering a stuck agent, the most common thing Kevin does here.
 *
 * The question is at the top, the options are buttons, and "Send and resume"
 * posts the reply and puts the item back in the queue. Tapping an option is
 * enough; typing is for when none of them fits.
 */
export function AnswerScreen({
  slug,
  number,
  item,
  loading,
  onBack,
  onSend,
}: {
  slug: string
  number: number
  item: HarnessItem | null
  loading: boolean
  onBack: () => void
  onSend: (text: string) => Promise<void>
}) {
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const kind = item?.kind ?? 'issue'

  return (
    <div className="screen on gh-screen">
      <div className="screen-head">
        <button className="iconbtn" onClick={onBack} aria-label="Back">
          ←
        </button>
        <h2>
          {slug} #{number}
          <small>{item?.title ?? (loading ? 'reading…' : '')}</small>
        </h2>
      </div>

      <div className="screen-body loops-body">
        {!item && !loading && (
          <div className="msg">
            <div className="glyph" style={{ color: 'var(--green)' }}>
              ✓
            </div>
            <h2>Not stuck any more</h2>
            <p>Someone already answered this one.</p>
          </div>
        )}
        {item && (
          <>
            <div className="question">{item.question || 'The agent left no question. Open it on GitHub to see why.'}</div>
            {(item.options ?? []).map((o, i) => (
              <button
                key={o}
                className={`opt ${text === `${i + 1}. ${o}` ? 'on' : ''}`}
                onClick={() => setText(`${i + 1}. ${o}`)}
              >
                <span className="n">{i + 1}</span>
                {o}
              </button>
            ))}
            <div className="field" style={{ marginTop: 14 }}>
              <label>Reply</label>
              <textarea value={text} onChange={(e) => setText(e.target.value)} rows={3} placeholder="Or type a short reply" />
            </div>
            <a className="linkbtn" href={item.url || itemURL(slug, number, kind)} target="_blank" rel="noreferrer">
              Open on GitHub ↗
            </a>
          </>
        )}
      </div>

      {item && (
        <div className="foot-bar">
          <button
            className="go"
            disabled={busy}
            onClick={() => {
              setBusy(true)
              void onSend(text).finally(() => setBusy(false))
            }}
          >
            {busy ? 'Sending…' : 'Send and resume'}
          </button>
        </div>
      )}
    </div>
  )
}
