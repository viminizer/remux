import type { HarnessItem, Loop } from './types'
import { loopDot, loopWho, project, since } from './types'

/**
 * The agent loops: an overlay at #/loops, like the GitHub screen.
 *
 * Two tabs. Loops is one card per loop, problems first. Inbox is only what
 * needs Kevin: a stuck agent's question, or a company PR now with the
 * supervisor. The main button sits at the bottom, where a thumb is.
 */
export function LoopsScreen({
  tab,
  loops,
  inbox,
  inboxErrors,
  loading,
  onTab,
  onBack,
  onRefresh,
  onOpenLoop,
  onOpenItem,
  onStart,
  onAsk,
}: {
  tab: 'loops' | 'inbox'
  loops: Loop[] | null
  inbox: HarnessItem[] | null
  inboxErrors: string[]
  loading: boolean
  onTab: (t: 'loops' | 'inbox') => void
  onBack: () => void
  onRefresh: () => void
  onOpenLoop: (l: Loop) => void
  onOpenItem: (it: HarnessItem) => void
  onStart: () => void
  onAsk: () => void
}) {
  const stuck = inbox?.filter((it) => it.why === 'stuck').length ?? 0
  return (
    <div className="screen on gh-screen">
      <div className="screen-head">
        <button className="iconbtn" onClick={onBack} aria-label="Back">
          ←
        </button>
        <h2>
          Loops
          <small>{loops ? `${loops.length} running` : 'reading…'}</small>
        </h2>
        <button className={`iconbtn ${loading ? 'spin' : ''}`} onClick={onRefresh} aria-label="Refresh">
          ⟳
        </button>
      </div>

      <div className="gh-seg">
        <button className={tab === 'loops' ? 'on' : ''} onClick={() => onTab('loops')}>
          Loops <span className="n">{loops?.length ?? '·'}</span>
        </button>
        <button className={tab === 'inbox' ? 'on' : ''} onClick={() => onTab('inbox')}>
          Inbox <span className="n">{inbox ? stuck : '·'}</span>
        </button>
      </div>

      {tab === 'loops' ? (
        <div className="screen-body loops-body">
          {loops?.map((l) => <LoopCard key={l.name} loop={l} onOpen={() => onOpenLoop(l)} />)}
          {loops && !loops.length && (
            <div className="msg" style={{ paddingBottom: 8 }}>
              <div className="glyph">↻</div>
              <h2>No loops running</h2>
              <p>Start one and the agents take ready issues on their own.</p>
            </div>
          )}
        </div>
      ) : (
        <div className="screen-body loops-body">
          {inboxErrors.map((e) => (
            <div key={e} className="scopenote warn">
              {e}
            </div>
          ))}
          {inbox?.map((it) => (
            <InboxRow key={`${it.slug}#${it.number}`} item={it} onOpen={() => onOpenItem(it)} />
          ))}
          {inbox && !inbox.length && (
            <div className="msg">
              <div className="glyph" style={{ color: 'var(--green)' }}>
                ✓
              </div>
              <h2>Nothing needs you</h2>
              <p>No agent is stuck.</p>
            </div>
          )}
        </div>
      )}

      <div className="foot-bar">
        <div className="foot-row">
          <button className="go ghost" onClick={onAsk}>
            Ask the supervisor
          </button>
          <button className="go" onClick={onStart}>
            ✚ Start loop
          </button>
        </div>
      </div>
    </div>
  )
}

function LoopCard({ loop: l, onOpen }: { loop: Loop; onOpen: () => void }) {
  const who = loopWho(l)
  let what: string
  if (l.state === 'working') what = `#${l.issue} ${l.title}`
  else if (l.role === 'chat') what = 'tap to ask about the loops'
  else if (l.state === 'triaging') what = 'finding issues that can start'
  else if (l.state === 'blocked') what = l.note || 'blocked'
  else what = 'idle · no ready issues'
  return (
    <button className="repo-card" onClick={onOpen}>
      <span className="av">{who.slice(0, 2)}</span>
      <span className="mid">
        <span className="nm">
          <span className="owner">{who} · </span>
          {project(l)}
        </span>
        <span className="st">
          <span className={`dot ${loopDot(l)}`} />
          <span className="loop-what">{what}</span>
        </span>
        <span className="st loop-meta">
          {since(l.since)}
          {l.role === 'build' && ` · ${l.scope || 'full'}`}
          {l.stop && ' · stopping after this issue'}
        </span>
      </span>
      <span className="chev">›</span>
    </button>
  )
}

function InboxRow({ item, onOpen }: { item: HarnessItem; onOpen: () => void }) {
  const stuck = item.why === 'stuck'
  const checking = item.why === 'checking'
  return (
    <button className="repo-card" onClick={onOpen}>
      <span className="av">{stuck ? '?' : checking ? '…' : '→'}</span>
      <span className="mid">
        <span className="nm">
          <span className="owner">
            {item.slug} #{item.number} ·{' '}
          </span>
          {item.title}
        </span>
        <span className="st">
          <span className={`dot ${stuck ? 'waiting' : checking ? 'working' : 'stale'}`} />
          <span className="loop-what">
            {stuck
              ? item.question || 'Stuck - open to see why'
              : checking
                ? 'The supervisor is checking this. You only hear if it needs you.'
                : 'With your supervisor'}
          </span>
        </span>
      </span>
      <span className="chev">›</span>
    </button>
  )
}
