import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, ApiError } from './api'
import { Socket } from './ws'
import { isGitHub, useRoute } from './router'
import type { Route } from './router'
import { useSettings, queueSnapshot, flushSnapshot, loadSnapshot } from './store'
import type { Conn, GhBadge, Health, Pane, Session, SnapMeta } from './types'
import { displayCommand, flatten, paneTitle } from './types'

import { Drawer } from './shell/Drawer'
import { usePinned } from './shell/layout'
import { useDrawerSwipe } from './shell/useDrawerSwipe'
import { TopBar } from './shell/TopBar'
import { Output } from './pane/Output'
import { KeyPad } from './pane/KeyPad'
import { Composer } from './pane/Composer'
import { NewSheet } from './sheets/NewSheet'
import { RenameSheet } from './sheets/RenameSheet'
import { PaneActionsSheet } from './sheets/PaneActionsSheet'
import { SettingsScreen } from './screens/Settings'
import { GitHubScreen } from './github/GitHubScreen'
import { RepoScreen } from './github/RepoScreen'
import { ItemScreen } from './github/ItemScreen'
import { AddRepoSheet } from './github/AddRepoSheet'
import { SendToPaneSheet } from './github/SendToPaneSheet'
import { useGitHub } from './github/useGitHub'
import type { InboxItem } from './github/types'
import { KeyPadScreen } from './screens/KeyPadScreen'
import { NamingScreen } from './screens/Naming'
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

  // GitHub. The badge arrives on the tree tick; the snapshot is only fetched
  // while the screen is open. ghStack is the back stack for the screens
  // reached from it - see the closeTop tier below.
  const [ghBadge, setGhBadge] = useState<GhBadge | null>(null)
  const [ghStack, setGhStack] = useState<Route[]>([])
  const [ghSheet, setGhSheet] = useState<'none' | 'add' | 'topane'>('none')
  const [ghCommand, setGhCommand] = useState('')
  const [optIn, setOptIn] = useState(false)
  const [notifState, setNotifState] = useState(notificationState())

  const sock = useRef<Socket | null>(null)
  // Composer focus, which on a phone is the software keyboard being up. The
  // key pad hides itself while it is - see the note in KeyPad.
  const [typing, setTyping] = useState(false)
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

  // ── GitHub ──────────────────────────────────────────────────────────────

  const ghOpen = isGitHub(route)
  const { snap: gh, loading: ghLoading, load: ghLoad, refresh: ghRefresh, apply: ghApply } =
    useGitHub(ghOpen, ghBadge?.at ?? 0)

  /* Muting is "not now", not "delete".
     The server records the item's own updatedAt, so the row comes back by
     itself the moment anything happens to it, and until then it is listed
     under Muted at the foot of the inbox. Both calls answer with the whole
     snapshot, so the screen and the drawer badge move together. */
  const ghMute = useCallback(
    async (it: InboxItem) => {
      try {
        ghApply(await api.githubMute(it.repo, it.number))
        toast(`muted ${it.repo}#${it.number}`)
      } catch (e) {
        toast(e instanceof Error ? e.message : String(e))
      }
    },
    [ghApply],
  )

  const ghUnmute = useCallback(
    async (it: InboxItem) => {
      try {
        ghApply(await api.githubUnmute(it.repo, it.number))
      } catch (e) {
        toast(e instanceof Error ? e.message : String(e))
      }
    },
    [ghApply],
  )

  const paneById = useMemo(() => new Map(panes.map((p) => [p.id, p])), [panes])

  const panesInRepo = useCallback(
    (repo: string): Pane[] =>
      (gh.panes?.[repo] ?? []).map((id) => paneById.get(id)).filter((p): p is Pane => !!p),
    [gh.panes, paneById],
  )

  // Navigating deeper pushes the current route onto a stack, so back inside
  // the GitHub screens walks back through them and only then leaves for the
  // pane. Without this, back from an issue would drop straight out of GitHub.
  const ghGo = useCallback(
    (next: Route) => {
      setGhStack((st) => [...st, route])
      go(next)
    },
    [route, go],
  )

  const ghBack = useCallback(() => {
    setGhStack((st) => {
      const prev = st[st.length - 1]
      go(prev ?? { name: 'pane', pane: lastPane.current })
      return st.slice(0, -1)
    })
  }, [go])

  // Which repo the GitHub sheets act on, whichever GitHub screen is showing.
  const ghRepo = route.name === 'ghRepo' || route.name === 'ghItem' ? route.repo : null

  const openPaneFromGitHub = useCallback(
    (p: Pane) => {
      setGhStack([])
      setGhSheet('none')
      go({ name: 'pane', pane: p.id })
    },
    [go],
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
      onGh: (badge) => setGhBadge(badge),
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
          if (!cancelled) {
            patch({
              notifyWaiting: n.notifyWaiting,
              notifyDone: n.notifyDone,
              notifyCi: n.notifyCi,
              ignoreChecks: n.ignoreChecks ?? [],
              namePanes: n.namePanes,
            })
          }
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
    // Whatever the pane we are leaving had queued is written now, under its
    // own id, before the new pane starts overwriting it.
    flushSnapshot()
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
  //
  // Queued, not written. This runs on every frame - `lines` is in the deps and
  // a working agent produces up to 2.5 a second - and the write parses and
  // stringifies the whole snapshot store synchronously. See queueSnapshot.
  useEffect(() => {
    if (!currentId || !live || !lines.length) return
    queueSnapshot({
      pane: currentId,
      lines,
      title: current ? paneTitle(current) : currentId,
      sub: current ? `${current.sessionName} · ${current.command}` : '',
      at: Date.now(),
    })
  }, [currentId, lines, live, current])

  // A backgrounded phone is exactly when the cached screen stops being a
  // convenience and starts being the only copy, so the queue is written out
  // before the app can be frozen. pagehide covers the iOS case where
  // visibilitychange is not guaranteed to arrive.
  useEffect(() => {
    const onHide = () => {
      if (document.visibilityState === 'hidden') flushSnapshot()
    }
    document.addEventListener('visibilitychange', onHide)
    window.addEventListener('pagehide', flushSnapshot)
    return () => {
      document.removeEventListener('visibilitychange', onHide)
      window.removeEventListener('pagehide', flushSnapshot)
      flushSnapshot()
    }
  }, [])

  // Land on something sensible on first load: whatever needs an answer, else
  // the first pane. It has to key off the route name, not off the absence of a
  // pane - `currentId` is null on every non-pane route too, and reading that as
  // "first load" redirected Settings away the moment it opened.
  useEffect(() => {
    if (route.name !== 'pane' || currentId || !panes.length) return
    const wanted = panes.find((p) => p.status === 'waiting') ?? panes[0]
    go({ name: 'pane', pane: wanted.id })
  }, [route.name, panes, currentId, go])

  // Past the breakpoint the drawer is pinned beside the content and the burger
  // is hidden, so there is nothing to open or close. drawerOpen can still be
  // true on arrival there - open the drawer on a phone, turn it landscape,
  // cross the breakpoint - and a stale true is not cosmetic: overlayOpen feeds
  // the history entry that Android back consumes, so it would leave an overlay
  // marked open that the user can neither see nor dismiss.
  const pinned = usePinned()
  useEffect(() => {
    if (pinned) setDrawerOpen(false)
  }, [pinned])

  // Swipe right to open the drawer, left to close it. Off while a sheet is up
  // - the sheet is the thing being answered - and off past the breakpoint,
  // where the drawer is already beside the content.
  const drawerEl = useRef<HTMLElement>(null)
  const { rootRef, drag } = useDrawerSwipe({
    open: drawerOpen,
    setOpen: setDrawerOpen,
    panel: drawerEl,
    enabled: !pinned && sheet === 'none',
  })

  // Stable identity on purpose. PaneMenu's outside-click effect lists its
  // onClose in the dependency array, so an inline arrow re-ran that effect on
  // every render of this component - which is every poll - tearing the
  // document listener down and re-arming it behind a setTimeout(0). A tap
  // landing in that gap hit no listener and the menu stayed open.
  const closeMenu = useCallback(() => setMenuOpen(false), [])

  // Closing the topmost overlay: the menu, then any sheet, then the expanded
  // key pad, then the drawer.
  // Held in a ref so the popstate listener below can stay mounted once instead
  // of re-subscribing whenever one of them changes.
  // Returns whether anything is still open underneath, which the popstate
  // handler needs synchronously - setState has not landed by then, so it
  // cannot just re-read the flags.
  //
  // The key pad sits above the drawer and below the sheets. It is the only
  // tier here that is also a saved preference, so back collapsing it writes
  // that preference - which is right: you closed it, it stays closed.
  const closeTop = useRef((): boolean => false)
  closeTop.current = () => {
    if (menuOpen) {
      setMenuOpen(false)
      return sheet !== 'none' || settings.keypadOpen || drawerOpen
    }
    if (sheet !== 'none') {
      setSheet('none')
      return ghStack.length > 0 || settings.keypadOpen || drawerOpen
    }
    if (ghSheet !== 'none') {
      setGhSheet('none')
      return ghStack.length > 0 || settings.keypadOpen || drawerOpen
    }
    // Inside GitHub, back walks up its own stack before it leaves.
    if (ghStack.length > 0) {
      ghBack()
      return ghStack.length > 1 || settings.keypadOpen || drawerOpen
    }
    if (settings.keypadOpen) {
      patch({ keypadOpen: false })
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
  const overlayOpen =
    menuOpen ||
    sheet !== 'none' ||
    ghSheet !== 'none' ||
    ghStack.length > 0 ||
    settings.keypadOpen ||
    drawerOpen
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
    if (
      'notifyWaiting' in p ||
      'notifyDone' in p ||
      'notifyCi' in p ||
      'ignoreChecks' in p ||
      'namePanes' in p
    ) {
      const next = { ...settings, ...p }
      guard('save settings', async () => {
        await api.saveSettings({
          notifyWaiting: next.notifyWaiting,
          notifyDone: next.notifyDone,
          notifyCi: next.notifyCi,
          ignoreChecks: next.ignoreChecks,
          namePanes: next.namePanes,
        })
        // The ignore list changes what counts as red, and the server kicks the
        // poller on save. Pick the new snapshot up rather than waiting for the
        // next badge tick to notice.
        if ('ignoreChecks' in p) void ghLoad()
      })
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

  // An unsent draft belongs to the pane it was written for.
  //
  // The composer used to keep its own text, and one composer is reused for
  // every pane, so the text came with you: type half a prompt to an agent,
  // tap another pane to check on it, and the half prompt is sitting in the
  // box under a shell, aimed at the wrong process.
  //
  // `key={currentId}` on the Composer was the one-line version of this, and it
  // fixes the bleed by throwing the draft away on every switch - which trades
  // one surprise for another, since checking another pane mid-sentence is
  // exactly what this app is for. Keyed drafts cost a few lines more and lose
  // nothing.
  //
  // In memory, not localStorage. A draft is worth the trip to another pane and
  // back, not a reload - and unlike the snapshot cache, which exists so a dead
  // connection still shows something, there is nothing to show here. Nothing
  // prunes the map: tmux never reuses a pane id, so a dead pane's entry is one
  // short string that goes away with the next reload.
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const draft = currentId ? (drafts[currentId] ?? '') : ''
  const setDraft = useCallback(
    (text: string) => {
      if (!currentId) return
      setDrafts((d) => ({ ...d, [currentId]: text }))
    },
    [currentId],
  )

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
  //
  // The C-u is best effort. Claude Code's vim mode ignores it in normal mode,
  // where nothing clears the line, so a command tapped onto a half-written
  // prompt still appends. The command text itself always arrives intact -
  // SendText pastes rather than types - and an empty composer, which is when
  // you reach for these, is unaffected either way.
  const sendCommand = async (text: string) => {
    if (!currentId) return
    if (await guard('key', () => api.keys(currentId, ['C-u']))) sendText(text, false)
  }

  // The chevron dismisses the software keyboard rather than toggling, when the
  // keyboard is what is hiding the pad.
  //
  // viewport.ts shrinks --app-h while the keyboard is up, and the pad takes
  // another 53 to 190px, which between them would leave almost no output
  // visible - so KeyPad renders nothing at all while typing. That makes the
  // chevron's job there unambiguous: put the pad back. Toggling instead would
  // flip a preference nobody can see the effect of, and on a pad that was
  // already open it would be a tap that changed nothing on the screen.
  //
  // `keypadOpen` is deliberately not touched, so the pad you get back is the
  // one you had. The key pad exists so the keyboard is not needed to answer an
  // agent; asking for the pad is asking for the keyboard to go.
  const toggleKeypad = () => {
    if (typing) {
      if (document.activeElement instanceof HTMLElement) document.activeElement.blur()
      return
    }
    patch({ keypadOpen: !settings.keypadOpen })
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

  const title = current ? paneTitle(current) : gone ? gone : 'Remux'
  // Anything the scrim should dim and close on a tap outside.
  const scrimUp = drawerOpen || sheet !== 'none' || ghSheet !== 'none'

  // Passed as parts, not one string, so the top bar can pick the project out.
  // It reads session · project · agent: which workspace, which repo inside it,
  // which tool.
  const session = current?.sessionName ?? ''
  const project = current?.remuxProject ?? ''
  const cmd = current ? displayCommand(meta?.cmd ?? current.command) : ''

  return (
    <div className="phone" ref={rootRef}>
      {/* The scrim has to know about every sheet, not just the pane ones.
          The GitHub sheets were left out when they were added, so tapping
          beside one did nothing and Done or back were the only ways out. */}
      <div className={`scrim ${drag !== null ? 'dragging' : ''} ${ghSheet !== 'none' ? 'over-screen' : ''}`}
           onClick={() => { setDrawerOpen(false); setSheet('none'); setGhSheet('none') }}
           style={{ opacity: drag ?? (scrimUp ? 1 : 0),
                    pointerEvents: scrimUp ? 'auto' : 'none' }} />

      <Drawer
        open={drawerOpen}
        panelRef={drawerEl}
        drag={drag}
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
        gh={ghBadge}
        onGitHub={() => {
          setDrawerOpen(false)
          setGhStack([])
          go({ name: 'gh' })
        }}
      />

      <div className="pane-col">
        {route.name === 'gh' ? (
          <GitHubScreen
            snap={gh}
            loading={ghLoading}
            panes={paneById}
            onBack={ghBack}
            onRefresh={() => void ghRefresh()}
            onOpenRepo={(repo) => ghGo({ name: 'ghRepo', repo, tab: 'issues' })}
            onOpenItem={(it) =>
              ghGo({
                name: 'ghItem',
                repo: it.repo,
                number: it.number,
                kind: it.kind === 'pr' ? 'pr' : 'issue',
              })
            }
            onOpenPane={openPaneFromGitHub}
            onAdd={() => setGhSheet('add')}
            onMute={(it) => void ghMute(it)}
            onUnmute={(it) => void ghUnmute(it)}
          />
        ) : route.name === 'ghRepo' ? (
          <RepoScreen
            repo={route.repo}
            meta={gh.repos.find((r) => r.full === route.repo)}
            viewer={gh.viewer}
            tab={route.tab}
            panes={paneById}
            onTab={(tab) => go({ name: 'ghRepo', repo: route.repo, tab })}
            onBack={ghBack}
            onOpenIssue={(n) =>
              ghGo({ name: 'ghItem', repo: route.repo, number: n, kind: 'issue' })
            }
            onOpenPR={(n) => ghGo({ name: 'ghItem', repo: route.repo, number: n, kind: 'pr' })}
            onOpenPane={openPaneFromGitHub}
            onSendToPane={() => {
              setGhCommand(`gh repo view ${route.repo} --web`)
              setGhSheet('topane')
            }}
            onUnwatch={async () => {
              try {
                await api.githubUnwatch(route.repo)
                await ghLoad()
                ghBack()
                toast('stopped watching ' + route.repo)
              } catch (e) {
                toast(e instanceof Error ? e.message : String(e))
              }
            }}
          />
        ) : route.name === 'ghItem' ? (
          <ItemScreen
            repo={route.repo}
            number={route.number}
            kind={route.kind}
            panes={panesInRepo(route.repo)}
            viewer={gh.viewer}
            onBack={ghBack}
            onOpenPane={openPaneFromGitHub}
            onSendToPane={() => {
              // gh issue develop creates a branch and checks it out; gh pr
              // checkout switches to one. Both are typed, never submitted, so
              // the decision to run them stays Kevin's.
              setGhCommand(
                route.kind === 'pr'
                  ? `gh pr checkout ${route.number} -R ${route.repo}`
                  : `gh issue develop ${route.number} -R ${route.repo} --checkout`,
              )
              setGhSheet('topane')
            }}
          />
        ) : route.name === 'settings' ? (
          <SettingsScreen
            health={health}
            conn={conn}
            lastReached={lastReached}
            settings={settings}
            patch={patchNotify}
            notifState={notifState}
            onEnableNotifications={onEnablePush}
            onBack={() => go({ name: 'pane', pane: lastPane.current })}
            onKeyPad={() => go({ name: 'keypad' })}
            onNaming={() => go({ name: 'naming' })}
            onCheck={checkNow}
            onUpdate={pullNewBuild}
          />
        ) : route.name === 'keypad' ? (
          <KeyPadScreen
            settings={settings}
            patch={patch}
            onBack={() => go({ name: 'settings' })}
          />
        ) : route.name === 'naming' ? (
          <NamingScreen onBack={() => go({ name: 'settings' })} />
        ) : !panes.length ? (
          <NoTmux onCreate={() => setSheet('new')} />
        ) : gone ? (
          <PaneGone pane={gone} onBack={() => { setGone(null); setDrawerOpen(true) }} />
        ) : (
          <>
            <TopBar
              title={title}
              session={session}
              project={project}
              cmd={cmd}
              status={meta?.status ?? current?.status}
              stale={stale}
              onBurger={() => {
                setMenuOpen(false)
                setDrawerOpen(true)
              }}
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
                starred={settings.starred.includes(current.id)}
                onClose={closeMenu}
                onStar={() => {
                  toggleStar(current)
                  setMenuOpen(false)
                }}
                onWrap={() => {
                  // Every tap in this menu closes it now. The only thing that
                  // does not is Kill pane, and that is not a tap - HoldButton
                  // fires on a long press, so a stray tap there leaves both the
                  // pane and the menu alone, which is the point of the hold.
                  setMenuOpen(false)
                  patch({ wrap: !settings.wrap })
                  toast(settings.wrap ? 'mirror — exact tmux screen' : 'wrap on — reflowed for reading')
                }}
                onInterrupt={async () => {
                  setMenuOpen(false)
                  if (await guard('interrupt', () => api.interrupt(current.id)))
                    toast('sent ^C to ' + current.id)
                }}
                onFocus={async () => {
                  setMenuOpen(false)
                  if (await guard('focus', () => api.focus(current.id)))
                    toast(`laptop switched to ${paneTitle(current)}`)
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
                expanded={settings.keypadOpen}
                onToggle={toggleKeypad}
                disabled={inputDisabled}
                typing={typing}
                custom={settings.chips}
                hidden={settings.hiddenKeys}
                command={meta?.cmd ?? current?.command ?? 'shell'}
              />

              <Composer
                target={meta?.cmd ?? current?.command ?? 'shell'}
                disabled={inputDisabled}
                submitOnEnter={settings.submitOnEnter}
                text={draft}
                onChange={setDraft}
                onSend={sendText}
                onTyping={setTyping}
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
        onSplit={async (direction) => {
          if (!actionTarget) return
          const target = actionTarget
          setSheet('none')
          if (await guard('split', () => api.newPane(target.id, direction)))
            toast(`split ${target.id} ${direction}`)
        }}
        onInterrupt={async () => {
          if (!actionTarget) return
          setSheet('none')
          if (await guard('interrupt', () => api.interrupt(actionTarget.id)))
            toast('sent ^C to ' + actionTarget.id)
        }}
        onFocus={async () => {
          if (!actionTarget) return
          const target = actionTarget
          setSheet('none')
          if (await guard('focus', () => api.focus(target.id)))
            toast(`laptop switched to ${paneTitle(target)}`)
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

      <AddRepoSheet
        open={ghSheet === 'add'}
        panes={paneById}
        watched={new Set(gh.repos.map((r) => r.full.toLowerCase()))}
        onClose={() => setGhSheet('none')}
        onChanged={() => void ghLoad()}
      />

      <SendToPaneSheet
        open={ghSheet === 'topane'}
        command={ghCommand}
        repoPanes={ghRepo ? panesInRepo(ghRepo) : []}
        sessions={sessions}
        path={ghRepo ? panesInRepo(ghRepo)[0]?.path : undefined}
        onClose={() => setGhSheet('none')}
        onSent={(pane, where) => {
          setGhSheet('none')
          toast(`typed into ${where} · press ⏎ there`)
          if (pane) openPaneFromGitHub(pane)
        }}
        onError={(msg) => toast(msg)}
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
  onInterrupt,
  onFocus,
  onRename,
  onStar,
  onKill,
  starred,
}: {
  pane: Pane
  wrap: boolean
  starred: boolean
  onClose: () => void
  onWrap: () => void
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
      {/* Font size was here, with a stepper. It was the only control in the
          menu you were meant to press more than once, which is why the menu
          used to stay open after a tap - one exception that made the whole
          menu feel unresponsive on every other item. It lives in Settings,
          where a stepper belongs and where it already was. */}
      <button className="mi" onClick={onWrap}>
        Wrap lines <small>{wrap ? 'on' : 'off'}</small>
      </button>
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
