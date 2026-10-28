import { useRef, useState, type ReactNode } from 'react'
import type { Health, Conn, Naming } from '../types'
import type { Settings } from '../store'
import { ago } from '../store'
import { namingState, useNaming } from './Naming'

const tag = (s: string) => (/^\d/.test(s) ? 'v' + s : s)
type SaveState = {
  group: string
  pending: boolean
  failed: boolean
  retry?: () => void
}

export function SettingsScreen({
  health,
  conn,
  lastReached,
  settings,
  patch,
  notifState,
  onEnableNotifications,
  onBack,
  onKeyPad,
  onNaming,
  onCheck,
  onUpdate,
}: {
  health: Health | null
  conn: Conn
  lastReached: number | null
  settings: Settings
  patch: (p: Partial<Settings>) => Promise<void>
  notifState: NotificationPermission | 'unsupported'
  onEnableNotifications: () => void
  onBack: () => void
  onKeyPad: () => void
  onNaming: () => void
  onCheck: () => Promise<void>
  onUpdate: () => Promise<void>
}) {
  const { n: naming } = useNaming(0)
  const [save, setSave] = useState<SaveState | null>(null)
  const saving = useRef(false)
  // A failed save retains its intended change, but a later bootstrap response
  // can refresh the other settings before Retry. Use the current payload builder.
  const latestPatch = useRef(patch)
  latestPatch.current = patch
  const latestSettings = useRef(settings)
  latestSettings.current = settings
  const [draft, setDraft] = useState('')
  const [check, setCheck] = useState<'idle' | 'checking' | 'failed' | 'done'>(
    'idle',
  )
  const [reloading, setReloading] = useState(false)
  const served = health?.version ?? null
  const stale = served !== null && served !== __BUILD_VERSION__
  async function saveMac(
    group: string,
    value: Partial<Settings> | ((current: Settings) => Partial<Settings>),
    done?: () => void,
  ) {
    if (saving.current) return
    saving.current = true
    setSave({ group, pending: true, failed: false })
    try {
      await latestPatch.current(
        typeof value === 'function' ? value(latestSettings.current) : value,
      )
      done?.()
      setSave({ group, pending: false, failed: false })
    } catch {
      setSave({
        group,
        pending: false,
        failed: true,
        retry: () => void saveMac(group, value, done),
      })
    } finally {
      saving.current = false
    }
  }
  async function checkNow() {
    setCheck('checking')
    try {
      await onCheck()
      setCheck('done')
    } catch {
      setCheck('failed')
    }
  }
  const status = (group: string) =>
    save?.group === group && (
      <p
        className={`settings-save ${save.failed ? 'failed' : ''}`}
        role="status"
      >
        {save.pending
          ? 'Saving to your Mac…'
          : save.failed
            ? 'Couldn’t save. Previous setting kept.'
            : 'Saved to your Mac'}
        {save.failed && (
          <button className="linkbtn" onClick={save.retry}>
            Retry
          </button>
        )}
      </p>
    )
  const section = (
    title: string,
    mac: boolean,
    children: ReactNode,
    group?: string,
  ) => (
    <section>
      <div className="settings-heading">
        <h3>{title}</h3>
        <span>{mac ? 'REMUX ON YOUR MAC' : 'THIS DEVICE'}</span>
      </div>
      <div className="card">{children}</div>
      {group && status(group)}
    </section>
  )
  const row = (title: string, hint: string, control: ReactNode) => (
    <div className="crow">
      <div className="lbl">
        {title}
        <small>{hint}</small>
      </div>
      {control}
    </div>
  )
  const toggle = (
    label: string,
    key: keyof Settings,
    mac = false,
    group = 'notifications',
  ) => (
    <label className="settings-switch-target">
      <button
        className={`sw-toggle ${settings[key] ? 'on' : ''}`}
        role="switch"
        aria-label={label}
        aria-checked={!!settings[key]}
        disabled={mac && !!save?.pending}
        onClick={() =>
          mac
            ? void saveMac(group, { [key]: !settings[key] })
            : void patch({ [key]: !settings[key] })
        }
      />
    </label>
  )
  const stepper = (
    key: 'fontSize' | 'lines',
    min: number,
    max: number,
    step: number,
    less: string,
    more: string,
  ) => (
    <span className="stepper settings-stepper">
      <button
        aria-label={less}
        disabled={settings[key] <= min}
        onClick={() =>
          void patch({ [key]: Math.max(min, settings[key] - step) })
        }
      >
        −
      </button>
      <output aria-live="polite">
        {settings[key]}
        {key === 'fontSize' ? 'px' : ''}
      </output>
      <button
        aria-label={more}
        disabled={settings[key] >= max}
        onClick={() =>
          void patch({ [key]: Math.min(max, settings[key] + step) })
        }
      >
        +
      </button>
    </span>
  )
  function addCheck() {
    const value = draft.trim()
    if (
      !value ||
      settings.ignoreChecks.some((n) => n.toLowerCase() === value.toLowerCase())
    )
      return
    void saveMac(
      'notifications',
      (current) => ({
        ignoreChecks: current.ignoreChecks.some(
          (n) => n.toLowerCase() === value.toLowerCase(),
        )
          ? current.ignoreChecks
          : [...current.ignoreChecks, value],
      }),
      () => setDraft((current) => (current.trim() === value ? '' : current)),
    )
  }
  const connectionTitle = {
    live: 'Connected to your Mac',
    offline: 'Your Mac is offline',
    denied: 'Access denied',
    connecting: 'Connecting to your Mac…',
  }[conn]
  const connectionHint =
    conn === 'denied'
      ? 'This account is not allowed to access Remux on this Mac.'
      : conn === 'live'
        ? `Remux is reachable · last reached ${lastReached ? ago(lastReached) : 'just now'}`
        : 'Device preferences still work. Check your Mac and Tailscale connection.'
  const permission = {
    granted: [
      '✓ Permission allowed on this device',
      'Your browser allows notifications. Event preferences are saved on your Mac.',
    ],
    default: [
      'Enable notifications on this device',
      'Event preferences are saved on your Mac.',
    ],
    denied: [
      'Blocked in browser settings',
      'Allow notifications for this site in your browser settings. Your event preferences are preserved.',
    ],
    unsupported: [
      'Notifications aren’t available here',
      'Use a supported browser; on iPhone, add Remux to your Home Screen.',
    ],
  }[notifState]
  return (
    <div className="screen on settings-screen">
      <div className="screen-head">
        <button className="iconbtn" aria-label="Back" onClick={onBack}>
          ←
        </button>
        <h2>Settings</h2>
      </div>
      <div className="screen-body">
        <div className="settings-content">
          <div className="settings-connection card">
            <div className="settings-connection-top">
              <span
                className={`dot ${conn === 'live' ? 'idle' : conn === 'denied' ? 'waiting' : 'stale'}`}
              />
              <div className="lbl">
                <strong>{connectionTitle}</strong>
                <p className="settings-hint">{connectionHint}</p>
              </div>
              <button
                className="settings-button"
                disabled={check === 'checking'}
                onClick={() => void checkNow()}
              >
                {check === 'checking' ? 'Checking…' : 'Check'}
              </button>
            </div>
            {check === 'failed' && (
              <p className="settings-save failed" role="status">
                Couldn’t reach your Mac. Try again.
              </p>
            )}
            {check === 'done' && (
              <p className="settings-save" role="status">
                Remux was reachable at the last check.
              </p>
            )}
            <details>
              <summary>Connection details</summary>
              <dl>
                <dt>Address</dt>
                <dd>{location.host}</dd>
                <dt>Signed in as</dt>
                <dd>{health?.login || '—'}</dd>
                <dt>Mac</dt>
                <dd>{health?.hostname || '—'}</dd>
                <dt>tmux</dt>
                <dd>
                  {health ? `${health.tmux} · ${health.panes} panes` : '—'}
                </dd>
                <dt>Last reached</dt>
                <dd>{lastReached ? ago(lastReached) : 'Never'}</dd>
              </dl>
            </details>
          </div>
          <div className="settings-grid">
            {section(
              'Reading',
              false,
              <>
                {row(
                  'Font size',
                  'Applied to terminal output',
                  stepper('fontSize', 10, 20, 1, 'Smaller font', 'Larger font'),
                )}
                <div
                  className="settings-sample"
                  style={{ fontSize: settings.fontSize }}
                >
                  <span>›</span> Review the latest changes
                  <br />
                  <em>Ready for your next instruction.</em>
                </div>
                {row(
                  'Wrap long lines',
                  'Fit terminal text to your screen',
                  toggle('Wrap long lines', 'wrap'),
                )}
                {row(
                  'Scrollback',
                  'Lines loaded from each pane',
                  stepper('lines', 100, 2000, 100, 'Fewer lines', 'More lines'),
                )}
              </>,
            )}
            {section(
              'Notifications',
              true,
              <>
                <div
                  className={`settings-permission ${notifState === 'granted' ? 'ready' : ''}`}
                >
                  <div className="lbl">
                    {permission[0]}
                    <p className="settings-hint">{permission[1]}</p>
                  </div>
                  {notifState === 'default' && (
                    <button
                      className="settings-button"
                      onClick={onEnableNotifications}
                    >
                      Enable
                    </button>
                  )}
                </div>
                {row(
                  'An agent needs an answer',
                  'When an agent is waiting for your input',
                  toggle('An agent needs an answer', 'notifyWaiting', true),
                )}
                {row(
                  'A task finishes',
                  'When a busy agent becomes idle',
                  toggle('A task finishes', 'notifyDone', true),
                )}
                {row(
                  'GitHub needs your attention',
                  'A PR check fails or someone requests your review',
                  toggle('GitHub needs your attention', 'notifyCi', true),
                )}
                <details className="settings-ignored">
                  <summary>
                    Ignored GitHub checks · {settings.ignoreChecks.length}
                  </summary>
                  <p className="settings-hint">
                    These checks don’t mark a PR as blocked in Remux. They still
                    appear on the PR.
                  </p>
                  <div className="chipset">
                    {settings.ignoreChecks.map((n) => (
                      <span className="chip" key={n}>
                        {n}
                        <button
                          className="chip-x"
                          aria-label={`Stop ignoring ${n}`}
                          disabled={!!save?.pending}
                          onClick={() =>
                            void saveMac('notifications', (current) => ({
                              ignoreChecks: current.ignoreChecks.filter(
                                (x) => x.toLowerCase() !== n.toLowerCase(),
                              ),
                            }))
                          }
                        >
                          ×
                        </button>
                      </span>
                    ))}
                    {!settings.ignoreChecks.length && (
                      <span className="dim">Every check counts.</span>
                    )}
                  </div>
                  <div className="addrow">
                    <input
                      aria-label="Check name to ignore"
                      value={draft}
                      onChange={(e) => setDraft(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') {
                          e.preventDefault()
                          addCheck()
                        }
                      }}
                      placeholder="Check name, e.g. Vercel"
                      spellCheck={false}
                      autoCorrect="off"
                      autoCapitalize="off"
                    />
                    <button
                      className="settings-button"
                      disabled={
                        !!save?.pending ||
                        !draft.trim() ||
                        settings.ignoreChecks.some(
                          (n) => n.toLowerCase() === draft.trim().toLowerCase(),
                        )
                      }
                      onClick={addCheck}
                    >
                      Add
                    </button>
                  </div>
                  <p className="settings-hint">
                    Matches part of a check name, ignoring case.
                  </p>
                </details>
              </>,
              'notifications',
            )}
            {section(
              'Input & keypad',
              false,
              <>
                {row(
                  'Send with Enter',
                  'When off, use the send key to submit.',
                  toggle('Send with Enter', 'submitOnEnter'),
                )}
                <button className="crow rowlink" onClick={onKeyPad}>
                  <div className="lbl">
                    Customize keypad
                    <small>
                      {settings.chips.length} custom shortcuts ·{' '}
                      {settings.hiddenKeys.length
                        ? `${settings.hiddenKeys.length} built-in keys hidden`
                        : 'all built-in keys shown'}
                    </small>
                  </div>
                  <span className="val">›</span>
                </button>
              </>,
            )}
            {section(
              'Agent naming',
              true,
              <>
                {row(
                  'Automatically name agent panes',
                  'Uses model calls to summarize what each agent is doing.',
                  toggle(
                    'Automatically name agent panes',
                    'namePanes',
                    true,
                    'naming',
                  ),
                )}
                <button className="crow rowlink" onClick={onNaming}>
                  <div className="lbl">
                    Naming activity<small>{namingSummary(naming)}</small>
                  </div>
                  <span className="val">›</span>
                </button>
              </>,
              'naming',
            )}
            {section(
              'App & updates',
              false,
              <>
                {row(
                  'Remux',
                  'Loaded on this device',
                  <span className="val">{tag(__BUILD_VERSION__)}</span>,
                )}
                <div className="settings-update">
                  <p className="settings-hint" role="status">
                    {check === 'checking'
                      ? 'Checking the interface served by your Mac…'
                      : check === 'failed'
                        ? 'Couldn’t check for an update. Try again.'
                        : stale
                          ? `${tag(served!)} is available from your Mac. Reload to use it.`
                          : served === null
                            ? 'Check the interface version served by your Mac.'
                            : 'Your interface is up to date.'}
                  </p>
                  <button
                    className={`settings-button ${stale ? 'primary' : ''}`}
                    disabled={
                      check === 'checking' || reloading || !!save?.pending
                    }
                    onClick={() => {
                      if (stale) {
                        setReloading(true)
                        void onUpdate().catch(() => setReloading(false))
                      } else void checkNow()
                    }}
                  >
                    {reloading
                      ? 'Reloading…'
                      : stale
                        ? 'Reload to update'
                        : check === 'checking'
                          ? 'Checking…'
                          : 'Check for update'}
                  </button>
                  <p className="settings-hint">
                    Reloading loads the interface served by your Mac. Running
                    loops keep working.
                  </p>
                </div>
              </>,
            )}
          </div>
          <p className="settings-note">
            Device preferences save here. Mac settings apply to Remux across
            your devices.
          </p>
        </div>
      </div>
    </div>
  )
}

function namingSummary(n: Naming | null): string {
  if (!n) return 'Recent model calls and the pane names they produced'
  const { label } = namingState(n)
  if (!n.calls) return label
  return `${label} · ${n.calls} ${n.calls === 1 ? 'call' : 'calls'} · ${n.wrote} panes named`
}
