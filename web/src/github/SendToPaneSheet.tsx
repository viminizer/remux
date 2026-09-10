import { useState } from 'react'
import { api } from '../api'
import { flatten } from '../types'
import type { Pane, Session } from '../types'
import { displayCommand, dotClass, statusLabel } from '../types'
import { GhGroup } from './rows'

/**
 * Send a command to a pane.
 *
 * The text is typed into the pane and deliberately not submitted - the same
 * rule the composer follows. Kevin presses Enter himself on the pane screen,
 * having seen what is about to run. That is what makes it safe for this sheet
 * to offer `gh pr checkout` at all.
 */
export function SendToPaneSheet({
  open,
  command,
  repoPanes,
  sessions,
  path,
  onClose,
  onSent,
  onError,
}: {
  open: boolean
  command: string
  /** Panes already sitting in a checkout of this repo. */
  repoPanes: Pane[]
  sessions: Session[]
  /** Where a new window should start, when there is a pane to copy it from. */
  path?: string
  onClose: () => void
  onSent: (pane: Pane | null, where: string) => void
  onError: (msg: string) => void
}) {
  const [busy, setBusy] = useState(false)

  const sendTo = async (p: Pane) => {
    setBusy(true)
    try {
      await api.text(p.id, command, false)
      onSent(p, `${p.sessionName} · win ${p.windowIndex}`)
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  // A new window is created with -d, so the laptop's active window does not
  // jump. Its pane id is not in the create response - that returns the window
  // - so the tree is refetched to find it.
  const sendToNew = async (session: Session) => {
    setBusy(true)
    try {
      const { id: windowId } = await api.newWindow(session.id, 'gh', path ?? '')
      const tree = await api.tree(false)
      const pane = flatten(tree).find((p) => p.windowId === windowId)
      if (!pane) throw new Error('the new window has no pane yet')
      await api.text(pane.id, command, false)
      onSent(pane, `${session.name} · new window`)
    } catch (e) {
      onError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className={`sheet gh-sheet ${open ? 'on' : ''}`}>
      <div className="grip" />
      <div className="sheet-body">
        <h3>Send to pane</h3>
        <div className="cmdbox">{command}</div>

        {repoPanes.length > 0 && (
          <>
            <GhGroup title="Panes in this repo" n={repoPanes.length} />
            {repoPanes.map((p) => (
              <button key={p.id} className="pick" disabled={busy} onClick={() => void sendTo(p)}>
                <span className="av">{displayCommand(p.command).slice(0, 2)}</span>
                <span className="mid">
                  <span className="nm">
                    {displayCommand(p.command)} · win {p.windowIndex}
                  </span>
                  <span className="sub">
                    <span className={`dot ${dotClass(p.status)}`} /> {statusLabel(p.status)} ·{' '}
                    {p.path}
                  </span>
                </span>
                <span className="add">›</span>
              </button>
            ))}
          </>
        )}

        <GhGroup title={repoPanes.length ? 'Or' : 'Open a new window'} />
        {sessions.map((s) => (
          <button key={s.id} className="pick" disabled={busy} onClick={() => void sendToNew(s)}>
            <span className="av">✚</span>
            <span className="mid">
              <span className="nm">New window in {s.name}</span>
              <span className="sub">created with -d · your layout will not jump</span>
            </span>
            <span className="add">›</span>
          </button>
        ))}

        <p className="sheet-note">
          The text is typed into the pane, not submitted. Press ⏎ on the pane screen yourself.
        </p>

        <button className="cancel" onClick={onClose}>
          Cancel
        </button>
      </div>
    </div>
  )
}
