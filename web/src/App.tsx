import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, ApiError } from './api'
import { Socket } from './ws'
import { useRoute } from './router'
import { useSettings, saveSnapshot, loadSnapshot } from './store'
import type { Conn, Health, Pane, Session, SnapMeta } from './types'
import { displayCommand, flatten, paneTitle } from './types'

import { Drawer } from './shell/Drawer'
import { TopBar } from './shell/TopBar'
import { Output } from './pane/Output'
import { KeyPad } from './pane/KeyPad'
import { Composer } from './pane/Composer'
import { NewSheet } from './sheets/NewSheet'
import { RenameSheet } from './sheets/RenameSheet'
import { SettingsScreen } from './screens/Settings'
import { BootSkeleton, NoTmux, NotAuthorized, PaneGone, StaleBar } from './screens/Messages'
import { HoldButton } from './components/HoldButton'
import { Toaster, toast } from './components/Toast'
import { enablePush, notificationState, registerServiceWorker } from './push'

export default function App() {
  const { settings, patch } = useSettings()
  const [route, go] = useRoute()

  const [sessions, setSessions] = useState<Session[] | null>(null)
  const [health, setHealth] = useState<Health | null>(null)
  const [conn, setConn] = useState<Conn>('connecting')
  const [denied, setDenied] = useState<{ login?: string; allowed?: string } | null>(null)
  const [lastReached, setLastReached] = useState<number | null>(null)

  const [lines, setLines] = useState<string[]>([])
  const [meta, setMeta] = useState<SnapMeta | null>(null)
  const [gone, setGone] = useState<string | null>(null)
  const [live, setLive] = useState(false) // is the current screen from the server?

  const [drawerOpen, setDrawerOpen] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const [sheet, setSheet] = useState<'none' | 'new' | 'rename'>('none')
  const [renameTarget, setRenameTarget] = useState<Pane | null>(null)
  const [staleWhy, setStaleWhy] = useState(false)
  const [optIn, setOptIn] = useState(false)
  const [notifState, setNotifState] = useState(notificationState())

  const sock = useRef<Socket | null>(null)

  const panes = useMemo(() => (sessions ? flatten({ sessions }) : []), [sessions])
  const currentId = route.name === 'pane' ? route.pane : null
  const current = useMemo(
    () => panes.find((p) => p.id === currentId) ?? null,
    [panes, currentId],
  )

  // ── socket ──────────────────────────────────────────────────────────────

  useEffect(() => {
    const s = new Socket({
      onTree: (next) => {
        setSessions(next)
        setLastReached(Date.now())
        setDenied(null)
      },
      onSnap: (pane, snapLines, snapMeta) => {
        setGone((g) => (g === pane ? null : g))
        setLines(snapLines)
        setMeta(snapMeta)
        setLive(true)
        setLastReached(Date.now())
      },
      onGone: (pane) => setGone(pane),
      onConn: (state) => {
        setConn(state)
        if (state !== 'live') setLive(false)
      },
    })
    sock.current = s
    s.connect()
    return () => {
      s.close()
      sock.current = null
    }
  }, [])

  // The tree also comes over the socket, but the first paint should not wait
  // for a socket handshake.
  useEffect(() => {
    let cancelled = false
    const boot = async () => {
      try {
        const [h, t] = await Promise.all([api.health(), api.tree()])
        if (cancelled) return
        setHealth(h)
        setSessions(t.sessions)
        setLastReached(Date.now())
      } catch (e) {
        if (cancelled) return
        if (e instanceof ApiError && e.status === 403) {
          const b = e.body as { login?: string; allowed?: string } | null
          setDenied({ login: b?.login, allowed: b?.allowed })
          setConn('denied')
        }
      }
    }
    boot()
    registerServiceWorker()
    return () => {
      cancelled = true
    }
  }, [])

  // ── pane subscription ───────────────────────────────────────────────────

  useEffect(() => {
    if (!currentId) {
      sock.current?.subscribe(null)
      return
    }
    // Show the cached screen at once, clearly marked stale, rather than an
    // empty box while the first snapshot is in flight.
    const cached = loadSnapshot(currentId)
    setLines(cached?.lines ?? [])
    setMeta(null)
    setLive(false)
    setGone(null)
    sock.current?.subscribe(currentId, settings.lines)
  }, [currentId, settings.lines])

  // Keep the last screen of every pane opened, so a dead connection still
  // shows the last thing the agent said.
  useEffect(() => {
    if (!currentId || !live || !lines.length) return
    saveSnapshot({
      pane: currentId,
      lines,
      title: current ? paneTitle(current) : currentId,
      sub: current ? `${current.sessionName} · win ${current.windowIndex} · ${current.command}` : '',
      at: Date.now(),
    })
  }, [currentId, lines, live, current])

  // Land on something sensible on first load: whatever needs an answer, else
  // the first pane.
  useEffect(() => {
    if (currentId || !panes.length) return
    const wanted = panes.find((p) => p.status === 'waiting') ?? panes[0]
    go({ name: 'pane', pane: wanted.id })
  }, [panes, currentId, go])

  // Android back closes the drawer, then any sheet, then leaves the app.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      if (menuOpen) setMenuOpen(false)
      else if (sheet !== 'none') setSheet('none')
      else if (drawerOpen) setDrawerOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [menuOpen, sheet, drawerOpen])

  // Ask about notifications once, after the first pane has been opened - never
  // on launch, because a denial is painful to reverse on Android.
  useEffect(() => {
    if (settings.askedNotifications || !currentId || notifState !== 'default') return
    const t = setTimeout(() => setOptIn(true), 2500)
    return () => clearTimeout(t)
  }, [settings.askedNotifications, currentId, notifState])

  // ── actions ─────────────────────────────────────────────────────────────

  const guard = useCallback(
    async (what: string, fn: () => Promise<unknown>) => {
      try {
        await fn()
        return true
      } catch (e) {
        toast(e instanceof ApiError ? `${what}: ${e.message}` : `${what} failed`)
        return false
      }
    },
    [],
  )

  const stale = conn !== 'live' || !live
  const inputDisabled = stale || !current

  // The ported CSS keys the stale treatment off `body.offline` - the output
  // dimmed to 62%, the composer and key pad greyed and pointer-events off.
  // Those, plus the banner naming the age, are the three things that stop
  // cached content from ever looking live, so the class has to be set here
  // rather than left to the individual components.
  useEffect(() => {
    document.body.classList.toggle('offline', stale)
    return () => document.body.classList.remove('offline')
  }, [stale])

  const sendText = (text: string, submit: boolean) => {
    if (!currentId) return
    guard('send', () => api.text(currentId, text, submit))
  }

  const sendKey = (k: string) => {
    if (!currentId) return
    guard('key', () => api.keys(currentId, [k]))
  }

  const checkNow = async () => {
    try {
      const t0 = performance.now()
      const h = await api.health()
      setHealth(h)
      setLastReached(Date.now())
      toast(`reachable · ${Math.round(performance.now() - t0)} ms`)
    } catch {
      toast('could not reach your Mac')
    }
  }

  const onEnablePush = async () => {
    const r = await enablePush()
    setNotifState(notificationState())
    patch({ askedNotifications: true })
    setOptIn(false)
    toast(
      r === 'ok'
        ? 'notifications enabled'
        : r === 'denied'
          ? 'blocked - allow it in browser settings'
          : r === 'unsupported'
            ? 'add to home screen first'
            : 'could not enable notifications',
    )
  }

  // ── screens that replace everything ─────────────────────────────────────

  if (denied) return <NotAuthorized login={denied.login} allowed={denied.allowed} />
  if (sessions === null) return <BootSkeleton />

  const title = current ? paneTitle(current) : gone ? gone : 'remux'
  const sub = current
    ? `${current.sessionName} · win ${current.windowIndex} · ${displayCommand(meta?.cmd ?? current.command)}`
    : ''

  return (
    <div className="phone">
      <div className="scrim" onClick={() => { setDrawerOpen(false); setSheet('none') }}
           style={{ opacity: drawerOpen || sheet !== 'none' ? 1 : 0,
                    pointerEvents: drawerOpen || sheet !== 'none' ? 'auto' : 'none' }} />

      <Drawer
        open={drawerOpen}
        panes={panes}
        current={currentId}
        conn={conn}
        onOpen={(p) => {
          go({ name: 'pane', pane: p.id })
          setDrawerOpen(false)
        }}
        onMenu={(p) => {
          setRenameTarget(p)
          setSheet('rename')
        }}
        onNew={() => {
          setDrawerOpen(false)
          setSheet('new')
        }}
        onSettings={() => {
          setDrawerOpen(false)
          go({ name: 'settings' })
        }}
      />

      <div className="pane-col">
        {route.name === 'settings' ? (
          <SettingsScreen
            health={health}
            conn={conn}
            lastReached={lastReached}
            settings={settings}
            patch={patch}
            notifState={notifState}
            onEnableNotifications={onEnablePush}
            onBack={() => go({ name: 'pane', pane: currentId })}
            onCheck={checkNow}
          />
        ) : !panes.length ? (
          <NoTmux onCreate={() => setSheet('new')} />
        ) : gone ? (
          <PaneGone pane={gone} onBack={() => { setGone(null); setDrawerOpen(true) }} />
        ) : (
          <>
            <TopBar
              title={title}
              sub={sub}
              status={meta?.status ?? current?.status}
              stale={stale}
              onBurger={() => setDrawerOpen(true)}
              onKebab={() => setMenuOpen((v) => !v)}
            />

            {stale && (
              <StaleBar
                lastReached={lastReached}
                expanded={staleWhy}
                onToggle={() => setStaleWhy((v) => !v)}
                onRetry={() => {
                  sock.current?.resume()
                  checkNow()
                }}
              />
            )}

            {optIn && (
              <div className="optin" style={{ display: 'flex' }}>
                <p>
                  <b>Get notified</b> when an agent needs an answer.
                </p>
                <button className="yes" onClick={onEnablePush}>
                  Enable
                </button>
                <button
                  className="no"
                  onClick={() => {
                    setOptIn(false)
                    patch({ askedNotifications: true })
                    toast('you can enable it in Settings')
                  }}
                >
                  Not now
                </button>
              </div>
            )}

            {menuOpen && current && (
              <PaneMenu
                pane={current}
                wrap={settings.wrap}
                fontSize={settings.fontSize}
                onClose={() => setMenuOpen(false)}
                onWrap={() => {
                  patch({ wrap: !settings.wrap })
                  toast(settings.wrap ? 'mirror — exact tmux screen' : 'wrap on — reflowed for reading')
                }}
                onFont={(d) =>
                  patch({ fontSize: Math.min(20, Math.max(10, settings.fontSize + d)) })
                }
                onInterrupt={async () => {
                  setMenuOpen(false)
                  if (await guard('interrupt', () => api.interrupt(current.id)))
                    toast('sent ^C to ' + current.id)
                }}
                onFocus={async () => {
                  setMenuOpen(false)
                  if (await guard('focus', () => api.focus(current.id)))
                    toast(`laptop switched to ${current.sessionName}:${current.windowIndex}`)
                }}
                onRename={() => {
                  setMenuOpen(false)
                  setRenameTarget(current)
                  setSheet('rename')
                }}
                onKill={async () => {
                  setMenuOpen(false)
                  if (await guard('kill', () => api.killPane(current.id))) {
                    toast('killed pane ' + current.id)
                    setGone(current.id)
                  }
                }}
              />
            )}

            <Output lines={lines} wrap={settings.wrap} />

            <KeyPad onKey={sendKey} disabled={inputDisabled} />

            <Composer
              target={meta?.cmd ?? current?.command ?? 'shell'}
              disabled={inputDisabled}
              submitOnEnter={settings.submitOnEnter}
              onSend={sendText}
            />
          </>
        )}
      </div>

      <NewSheet
        open={sheet === 'new'}
        sessions={sessions}
        defaultSession={current?.sessionId ?? null}
        onClose={() => setSheet('none')}
        onCreate={async (kind, name, sessionId, path) => {
          const ok = await guard('create', () =>
            kind === 'session' ? api.newSession(name, path) : api.newWindow(sessionId, name, path),
          )
          if (ok) {
            toast(`created ${name || kind}`)
            setSheet('none')
            sock.current?.resume()
          }
        }}
      />

      <RenameSheet
        open={sheet === 'rename'}
        what="window"
        current={renameTarget?.windowName ?? ''}
        onClose={() => setSheet('none')}
        onRename={async (name) => {
          if (!renameTarget) return
          if (await guard('rename', () => api.renameWindow(renameTarget.windowId, name))) {
            toast('renamed to ' + name)
            setSheet('none')
            sock.current?.resume()
          }
        }}
      />

      <Toaster />
    </div>
  )
}

/** The ⋮ menu. Kill is hold-to-confirm; everything else is a single tap. */
function PaneMenu({
  pane,
  wrap,
  onClose,
  onWrap,
  onFont,
  onInterrupt,
  onFocus,
  onRename,
  onKill,
}: {
  pane: Pane
  wrap: boolean
  fontSize: number
  onClose: () => void
  onWrap: () => void
  onFont: (d: number) => void
  onInterrupt: () => void
  onFocus: () => void
  onRename: () => void
  onKill: () => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const on = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose()
    }
    // Defer, or the click that opened the menu closes it again.
    const t = setTimeout(() => document.addEventListener('click', on), 0)
    return () => {
      clearTimeout(t)
      document.removeEventListener('click', on)
    }
  }, [onClose])

  return (
    <div className="menu on" ref={ref}>
      <button className="mi" onClick={onInterrupt}>
        Interrupt <small>^C</small>
      </button>
      {/* The one action that moves the laptop's own cursor, labelled as such. */}
      <button className="mi" onClick={onFocus}>
        Focus on laptop
      </button>
      <div className="msep" />
      <button className="mi" onClick={onWrap}>
        Wrap lines <small>{wrap ? 'on' : 'off'}</small>
      </button>
      <div className="mi" style={{ cursor: 'default' }}>
        Font size
        <span className="stepper">
          <button onClick={() => onFont(-1)}>−</button>
          <button onClick={() => onFont(1)}>+</button>
        </span>
      </div>
      <div className="msep" />
      <button className="mi" onClick={onRename}>
        Rename window
      </button>
      <HoldButton label={`Kill pane ${pane.id}`} onConfirm={onKill} />
    </div>
  )
}
