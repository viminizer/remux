import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import { toast } from '../components/Toast'
import type { Route } from '../router'
import { AnswerScreen } from './AnswerScreen'
import { LoopDetail } from './LoopDetail'
import { LoopsScreen } from './LoopsScreen'
import { StartLoop } from './StartLoop'
import type { HarnessItem, LoopsPayload } from './types'

const say = (e: unknown) => toast(e instanceof Error ? e.message : String(e))

/**
 * The loop list, read from tmux on the Mac. Polled only while something is
 * showing it - the screens, or the drawer row - because it is a tmux call and
 * nothing else needs it.
 */
export function useLoops(active: boolean) {
  const [data, setData] = useState<LoopsPayload | null>(null)
  const refresh = useCallback(() => api.loops().then(setData).catch(() => {}), [])
  useEffect(() => {
    if (!active) return
    void refresh()
    const t = setInterval(refresh, 4000)
    return () => clearInterval(t)
  }, [active, refresh])
  return { data, refresh }
}

/** Every loop screen, picked by route. */
export function LoopsRoutes({
  route,
  go,
  data,
  refresh,
  onClose,
}: {
  route: Route
  go: (r: Route) => void
  data: LoopsPayload | null
  refresh: () => Promise<void>
  onClose: () => void
}) {
  const [inbox, setInbox] = useState<HarnessItem[] | null>(null)
  const [errors, setErrors] = useState<string[]>([])
  const [loading, setLoading] = useState(false)

  const loadInbox = useCallback(async () => {
    setLoading(true)
    try {
      const r = await api.harnessInbox()
      setInbox(r.items)
      setErrors(r.errors)
    } catch (e) {
      say(e)
    } finally {
      setLoading(false)
    }
  }, [])

  // The inbox reads GitHub, so it is fetched when it is about to be seen,
  // not polled with the loop list.
  const wantsInbox = (route.name === 'loops' && route.tab === 'inbox') || route.name === 'loopQ'
  useEffect(() => {
    if (wantsInbox) void loadInbox()
  }, [wantsInbox, loadInbox])

  if (route.name === 'loop') {
    const loop = data?.loops.find((l) => l.name === route.loop) ?? null
    const home = { name: 'loops', tab: loop?.role === 'supervise' ? 'supervisor' : 'loops' } as const
    return (
      <LoopDetail
        loop={data ? loop : null}
        onBack={() => go(home)}
        onStop={async (when) => {
          try {
            const r = await api.stopLoop(route.loop, when)
            toast(r.when === 'after' ? 'stops after this issue' : 'stopped')
            await refresh()
            if (r.when !== 'after') go(home)
          } catch (e) {
            say(e)
          }
        }}
        onSave={async (v) => {
          try {
            await api.editLoop(route.loop, v)
            toast('saved - used from the next issue')
            await refresh()
          } catch (e) {
            say(e)
            throw e
          }
        }}
      />
    )
  }

  if (route.name === 'loopStart') {
    return (
      <StartLoop
        repos={data?.repos ?? []}
        running={data?.loops ?? []}
        last={data?.last ?? null}
        presets={data?.presets ?? []}
        onBack={() => go({ name: 'loops', tab: 'loops' })}
        onStart={async (v) => {
          try {
            const r = await api.startLoops(v)
            toast(startSummary(r))
            await refresh()
            go({ name: 'loops', tab: 'loops' })
          } catch (e) {
            say(e)
          }
        }}
        onSavePresets={async (p) => {
          try {
            await api.savePresets(p)
            await refresh()
          } catch (e) {
            say(e)
          }
        }}
      />
    )
  }

  if (route.name === 'loopQ') {
    const item = inbox?.find((it) => it.slug === route.slug && it.number === route.number && it.why !== 'supervisor') ?? null
    return (
      <AnswerScreen
        slug={route.slug}
        number={route.number}
        item={item}
        loading={inbox === null || loading}
        onBack={() => go({ name: 'loops', tab: 'inbox' })}
        onSend={async (text) => {
          if (!item) return
          try {
            await api.resume(item.slug, item.number, item.kind, text)
            toast('sent - back in the queue')
            setInbox((xs) => xs?.filter((x) => x !== item) ?? null)
            go({ name: 'loops', tab: 'inbox' })
          } catch (e) {
            say(e)
          }
        }}
      />
    )
  }

  const tab = route.name === 'loops' ? route.tab : 'loops'
  return (
    <LoopsScreen
      tab={tab}
      loops={data?.loops ?? null}
      inbox={inbox}
      inboxErrors={errors}
      loading={loading}
      onTab={(t) => go({ name: 'loops', tab: t })}
      onBack={onClose}
      onRefresh={() => void (tab === 'inbox' ? loadInbox() : refresh())}
      onOpenLoop={(l) => go({ name: 'loop', loop: l.name })}
      onOpenItem={(it) =>
        it.why === 'supervisor' ? window.open(it.url, '_blank') : go({ name: 'loopQ', slug: it.slug, number: it.number })
      }
      onStart={() => go({ name: 'loopStart' })}
      onAsk={async (agent) => {
        try {
          const r = await api.supervisor(agent)
          await refresh()
          go({ name: 'pane', pane: r.pane })
        } catch (e) {
          say(e)
        }
      }}
    />
  )
}

/** What a start did, in one line: "started 1 · stopped 1 · updated 3". */
export function startSummary(r: { started: string[]; stopped: string[]; updated: string[] }): string {
  const parts = [
    r.started.length && `started ${r.started.length}`,
    r.stopped.length && `stopped ${r.stopped.length}`,
    r.updated.length && `updated ${r.updated.length}`,
  ].filter(Boolean)
  return parts.length ? parts.join(' · ') : 'nothing changed'
}
