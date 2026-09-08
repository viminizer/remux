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
import { PaneActionsSheet } from './sheets/PaneActionsSheet'
import { SettingsScreen } from './screens/Settings'
import { BootSkeleton, NoTmux, NotAuthorized, PaneGone, StaleBar } from './screens/Messages'
import { HoldButton } from './components/HoldButton'
import { Toaster, toast } from './components/Toast'
import { enablePush, notificationState, pullNewBuild, registerServiceWorker } from './push'

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
  const [sheet, setSheet] = useState<'none' | 'new' | 'rename' | 'actions'>('none')
  const [renameTarget, setRenameTarget] = useState<Pane | null>(null)
  // The pane a drawer long-press targeted. Deliberately not `current`: the
  // long-pressed row is usually not the pane being viewed, and every action in
  // the sheet has to follow this one.
  const [actionTarget, setActionTarget] = useState<Pane | null>(null)
  const [staleWhy, setStaleWhy] = useState(false)
  const [optIn, setOptIn] = useState(false)
  const [notifState, setNotifState] = useState(notificationState())

  const sock = useRef<Socket | null>(null)
  const [inputbar, setInputbar] = useState<HTMLDivElement | null>(null)

  // The toast floats above the key pad and composer, and the composer grows
  // with the text in it. Publishing the measured height keeps the toast clear
  // of it instead of guessing a fixed offset.
  useEffect(() => {
    if (!inputbar) return
    const measure = () =>
      document.documentElement.style.setProperty('--inputbar-h', inputbar.offsetHeight + 'px')
    measure() // ResizeObserver only fires once the tab paints
    const ro = new ResizeObserver(measure)
    ro.observe(inputbar)
    return () => ro.disconnect()
  }, [inputbar])

  const panes = useMemo(() => (sessions ? flatten({ sessions }) : []), [sessions])
  const currentId = route.name === 'pane' ? route.pane : null

  // Settings is an overlay you come back from, not a destination. Without
  // this, Back lands on whatever auto-select picks rather than the pane you
  // were reading.
  const lastPane = useRef<string | null>(null)
  if (currentId) lastPane.current = currentId
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
        // The notification toggles are the server's, not this device's.
        try {
          const n = await api.settings()
          if (!cancelled) patch({ notifyWaiting: n.notifyWaiting, notifyDone: n.notifyDone })
        } catch {
          // Non-fatal: the toggles just show the last known local values.
        }
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
  // the first pane. It has to key off the route name, not off the absence of a
  // pane - `currentId` is null on every non-pane route too, and reading that as
  // "first load" redirected Settings away the moment it opened.
  useEffect(() => {
    if (route.name !== 'pane' || currentId || !panes.length) return
    const wanted = panes.find((p) => p.status === 'waiting') ?? panes[0]
    go({ name: 'pane', pane: wanted.id })
  }, [route.name, panes, currentId, go])

  // Closing the topmost overlay: the menu, then any sheet, then the drawer.
  // Held in a ref so the popstate listener below can stay mounted once instead
  // of re-subscribing whenever one of them changes.
  // Returns whether anything is still open underneath, which the popstate
  // handler needs synchronously - setState has not landed by then, so it
  // cannot just re-read the flags.
  const closeTop = useRef((): boolean => false)
  closeTop.current = () => {
    if (menuOpen) {
      setMenuOpen(false)
      return sheet !== 'none' || drawerOpen
    }
    if (sheet !== 'none') {
      setSheet('none')
      return drawerOpen
    }
    if (drawerOpen) {
      setDrawerOpen(false)
      return false
    }
    return false
  }

  // Android back closes the drawer, then any sheet, then leaves the app.
  //
  // That is what the comment here always claimed, but the code only listened
  // for Escape, and Android back drives history navigation rather than a
  // keydown - so on a phone with no hardware keyboard none of it ran. Escape
  // stays for the pinned-open desktop layout at min-width:900px, where a
  // hardware keyboard is the real case.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') closeTop.current()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // One history entry stands for "some overlay is open", not one per overlay.
  //
  // Opening the first overlay pushes it; back pops it and closes the topmost
  // overlay. If another is still open underneath - a sheet opened over the
  // drawer - this effect immediately pushes a fresh entry, so the next back
  // closes that one too. One entry, re-armed as it is spent.
  //
  // Closing by any other means (the scrim, Cancel, finishing a rename) pops
  // the entry, so it cannot go stale and leave a back press doing nothing.
  //
  // The exception is a navigation. go() replaces the entry and clears this
  // marker, so the check below fails and the entry is handed to the pane
  // instead of popped - which is deliberate, and is the only way a pane gets
  // a history entry at all. Popping there would have reverted the navigation
  // the moment the drawer closed.
  const overlayOpen = menuOpen || sheet !== 'none' || drawerOpen
  const marked = useRef(false)

  useEffect(() => {
    if (overlayOpen && !marked.current) {
      history.pushState({ remuxOverlay: true }, '')
      marked.current = true
    } else if (!overlayOpen && marked.current) {
      marked.current = false
      if (history.state?.remuxOverlay) history.back()
    }
  }, [overlayOpen])

  useEffect(() => {
    const onPop = () => {
      if (!marked.current) return
      marked.current = false
      // Re-arm here rather than leaving it to the effect above: closing a
      // sheet over an open drawer does not change overlayOpen, so that effect
      // would not re-run and the drawer would be left with no entry - the next
      // back would leave the app instead of closing it.
      if (closeTop.current()) {
        history.pushState({ remuxOverlay: true }, '')
        marked.current = true
      }
    }
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

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

  const toggleStar = (p: Pane) =>
    patch({
      starred: settings.starred.includes(p.id)
        ? settings.starred.filter((id) => id !== p.id)
        : [...settings.starred, p.id],
    })

  // Drop stars for panes that no longer exist.
  //
  // tmux never reuses a pane id while its server lives, but the counter resets
  // when that server restarts - so a star saved before a restart can latch
  // onto an unrelated new pane, and you open a starred row to find a stranger.
  //
  // Only ever against a live tree. The offline path keeps working from cached
  // snapshots, and pruning against an empty tree would silently wipe every
  // star the first time the Mac was unreachable.
  useEffect(() => {
    if (conn !== 'live' || !panes.length || !settings.starred.length) return
    const live = new Set(panes.map((p) => p.id))
    const kept = settings.starred.filter((id) => live.has(id))
    if (kept.length !== settings.starred.length) patch({ starred: kept })
  }, [conn, panes, settings.starred, patch])

  // Notification toggles must reach the server or they do nothing.
  const patchNotify = (p: Partial<typeof settings>) => {
    patch(p)
    if ('notifyWaiting' in p || 'notifyDone' in p) {
      const next = { ...settings, ...p }
      guard('save settings', () =>
        api.saveSettings({ notifyWaiting: next.notifyWaiting, notifyDone: next.notifyDone }),
      )
    }
  }

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

  // A slash command, typed but not run.
  //
  // C-u first, because these only register at the start of an empty composer:
  // dropped into a half-written prompt, or onto a suggestion just accepted with
  // tab, "/clear" appends and the line silently does nothing.
  //
  // It stops there rather than submitting. /clear discards a conversation and
  // cannot be undone, and the chip row scrolls, so chips move under your thumb
  // between glances - one mis-tap wiping a long session is a bad trade for
  // saving a single tap on ⏎. Typing it for you is the part that hurts on a
  // phone; confirming it is not.
  const sendCommand = async (text: string) => {
    if (!currentId) return
    if (await guard('key', () => api.keys(currentId, ['C-u']))) sendText(text, false)
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
          setActionTarget(p)
          setSheet('actions')
        }}
        starred={settings.starred}
        onStar={toggleStar}
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
            patch={patchNotify}
            notifState={notifState}
            onEnableNotifications={onEnablePush}
            onBack={() => go({ name: 'pane', pane: lastPane.current })}
            onCheck={checkNow}
            onUpdate={pullNewBuild}
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
                starred={settings.starred.includes(current.id)}
                onClose={() => setMenuOpen(false)}
                onStar={() => {
                  toggleStar(current)
                  setMenuOpen(false)
                }}
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

            <div className="inputbar" ref={setInputbar}>
              <KeyPad
                onKey={sendKey}
                onText={(t) => sendText(t, false)}
                onCommand={sendCommand}
                disabled={inputDisabled}
              />

              <Composer
                target={meta?.cmd ?? current?.command ?? 'shell'}
                disabled={inputDisabled}
                submitOnEnter={settings.submitOnEnter}
                onSend={sendText}
              />
            </div>
          </>
        )}
      </div>

      <PaneActionsSheet
        pane={actionTarget}
        open={sheet === 'actions'}
        starred={actionTarget ? settings.starred.includes(actionTarget.id) : false}
        onClose={() => setSheet('none')}
        onOpen={() => {
          if (!actionTarget) return
          go({ name: 'pane', pane: actionTarget.id })
          setSheet('none')
          setDrawerOpen(false)
        }}
        onStar={() => {
          if (actionTarget) toggleStar(actionTarget)
          setSheet('none')
        }}
        onRename={() => {
          setRenameTarget(actionTarget)
          setSheet('rename')
        }}
        onInterrupt={async () => {
          if (!actionTarget) return
          setSheet('none')
          if (await guard('interrupt', () => api.interrupt(actionTarget.id)))
            toast('sent ^C to ' + actionTarget.id)
        }}
        onFocus={async () => {
          if (!actionTarget) return
          setSheet('none')
          if (await guard('focus', () => api.focus(actionTarget.id)))
            toast(`laptop switched to ${actionTarget.sessionName}:${actionTarget.windowIndex}`)
        }}
        onKill={async () => {
          if (!actionTarget) return
          const target = actionTarget
          setSheet('none')
          if (await guard('kill', () => api.killPane(target.id))) {
            toast('killed pane ' + target.id)
            // "Pane gone" is the screen for the pane you were looking at. Kill
            // a different one from the drawer and there is nothing to report -
            // the tree push removes its row on its own.
            if (target.id === currentId) setGone(target.id)
          }
        }}
      />

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
        what="pane"
        current={renameTarget?.remuxTitle ?? ''}
        placeholder={renameTarget ? paneTitle(renameTarget) : ''}
        onClose={() => setSheet('none')}
        onRename={async (name) => {
          if (!renameTarget) return
          if (await guard('rename', () => api.renamePane(renameTarget.id, name))) {
            toast(name ? 'renamed to ' + name : 'name cleared')
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
  onStar,
  onKill,
  starred,
}: {
  pane: Pane
  wrap: boolean
  fontSize: number
  starred: boolean
  onClose: () => void
  onWrap: () => void
  onFont: (d: number) => void
  onInterrupt: () => void
  onFocus: () => void
  onRename: () => void
  onStar: () => void
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
      <button className="mi" onClick={onStar}>
        {starred ? 'Unstar pane' : 'Star pane'}
      </button>
      <button className="mi" onClick={onRename}>
        Rename pane
      </button>
      <HoldButton label={`Kill pane ${pane.id}`} onConfirm={onKill} />
    </div>
  )
}
