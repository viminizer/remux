import { ago } from '../store'

/** Skeleton, never a spinner: the first tree lands in ~200 ms. */
export function BootSkeleton() {
  const bars = [52, 34, 100, 88, 94, 61, 75, 82]
  return (
    <div className="screen on">
      <div className="screen-body" style={{ paddingTop: 26 }}>
        {bars.map((w, i) => (
          <div
            key={i}
            className="sk"
            style={{
              width: `${w}%`,
              height: i < 2 ? (i === 0 ? 15 : 11) : 11,
              marginBottom: i === 1 || i === 5 ? 26 : 9,
            }}
          />
        ))}
      </div>
    </div>
  )
}

export function NoTmux({ onCreate }: { onCreate: () => void }) {
  return (
    <div className="screen on">
      <div className="msg">
        <div className="glyph" style={{ color: 'var(--ov1)' }}>
          ▢
        </div>
        <h2>No tmux server running</h2>
        <p>Your Mac is reachable, but nothing is running in tmux right now.</p>
        <button className="cta" onClick={onCreate}>
          Create first session
        </button>
      </div>
    </div>
  )
}

/**
 * The one thing this screen has to make unmistakable is that nothing reached
 * tmux. A 403 here is a rejection at the door, not a failed action.
 */
export function NotAuthorized({ login, allowed }: { login?: string; allowed?: string }) {
  return (
    <div className="screen on">
      <div className="msg">
        <div
          className="glyph"
          style={{
            color: 'var(--red)',
            background: 'rgba(243,139,168,.10)',
            borderColor: 'rgba(243,139,168,.28)',
          }}
        >
          ⊘
        </div>
        <h2>Not authorized</h2>
        <p>
          This app only accepts{' '}
          <b style={{ color: 'var(--sub1)' }}>{allowed || 'the enrolled account'}</b>.
          {login && (
            <>
              <br />
              You're signed in to the tailnet as
              <br />
              <code style={{ fontFamily: 'var(--mono)', color: 'var(--peach)' }}>{login}</code>
            </>
          )}
        </p>
        <div className="checklist" style={{ marginTop: 24 }}>
          <div>
            <span>›</span>
            <span>
              Nothing was sent to tmux. The request was rejected before it reached the server.
            </span>
          </div>
        </div>
      </div>
    </div>
  )
}

export function PaneGone({ pane, onBack }: { pane: string; onBack: () => void }) {
  return (
    <div className="screen on">
      <div className="msg">
        <div className="glyph" style={{ color: 'var(--ov1)' }}>
          ⌫
        </div>
        <h2>This pane is gone</h2>
        <p>
          <code style={{ fontFamily: 'var(--mono)' }}>{pane}</code> exited or was killed while you
          were reading it.
        </p>
        <button className="cta" onClick={onBack}>
          Back to panes
        </button>
      </div>
    </div>
  )
}

/**
 * The stale banner.
 *
 * Cached content must never look live. This names the age, and the output
 * dimming and the disabled composer do the rest. "Last reached" is the line
 * that separates walking into a lift from the Mac having slept an hour ago.
 */
export function StaleBar({
  lastReached,
  expanded,
  onToggle,
  onRetry,
}: {
  lastReached: number | null
  expanded: boolean
  onToggle: () => void
  onRetry: () => void
}) {
  const online = navigator.onLine
  return (
    <div className="stale-bar" style={{ display: 'flex' }}>
      <div className="stale-top" onClick={onToggle}>
        <span className="dot stale" />
        <span className="grow">
          {online ? 'Cached view' : 'Your phone is offline'}
          {lastReached ? ` · last reached ${ago(lastReached)}` : ''}
        </span>
        <span style={{ color: 'var(--ov1)', fontSize: 11 }}>Why?</span>
      </div>
      <div className={`stale-why ${expanded ? 'on' : ''}`}>
        <span className="chk">
          · Is <b>Tailscale</b> on this phone?
        </span>
        <span className="chk">
          · Is the Mac <b>awake and powered</b>?
        </span>
        <span className="chk">
          · Phone network: <b>{online ? 'online' : 'offline'}</b>
        </span>
        <button
          className="retry"
          onClick={(e) => {
            e.stopPropagation()
            onRetry()
          }}
        >
          Try again
        </button>
      </div>
    </div>
  )
}
