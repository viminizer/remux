import type { Conn, GhBadge, Session, SnapMeta } from './types'

type Handlers = {
  onTree: (sessions: Session[]) => void
  onSnap: (pane: string, lines: string[], meta: SnapMeta) => void
  onGone: (pane: string) => void
  onConn: (state: Conn) => void
  /** The GitHub drawer badge, sent only when its numbers change. */
  onGh: (badge: GhBadge) => void
}

/**
 * One socket per client, carrying both the tree and the focused pane.
 *
 * All polling is server-side: the phone never polls. It sends `unsub` when the
 * screen goes off, which stops the poller entirely - that is the whole battery
 * story, and it is why visibilitychange is wired up here rather than left to
 * the components.
 */
export class Socket {
  private ws: WebSocket | null = null
  private h: Handlers
  private pane: string | null = null
  private lines = 400
  private backoff = 500
  private timer: number | null = null
  private closed = false
  private hidden = false

  constructor(h: Handlers) {
    this.h = h
    document.addEventListener('visibilitychange', this.onVisibility)
    window.addEventListener('online', this.onOnline)
    window.addEventListener('offline', this.onOffline)
  }

  connect() {
    if (this.closed) return
    this.clearTimer()
    this.h.onConn('connecting')

    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    let ws: WebSocket
    try {
      ws = new WebSocket(`${proto}://${location.host}/ws`)
    } catch {
      this.scheduleReconnect()
      return
    }
    this.ws = ws

    ws.onopen = () => {
      // A fresh socket knows nothing, so restate what we are looking at.
      this.backoff = 500
      this.h.onConn('live')
      if (this.pane && !this.hidden) this.send({ t: 'sub', pane: this.pane, lines: this.lines })
    }

    ws.onmessage = (ev) => {
      let msg: Record<string, unknown>
      try {
        msg = JSON.parse(ev.data as string)
      } catch {
        return
      }
      switch (msg.t) {
        case 'tree':
          this.h.onTree((msg.sessions as Session[]) ?? [])
          break
        case 'snap':
          this.h.onSnap(msg.pane as string, (msg.lines as string[]) ?? [], msg.meta as SnapMeta)
          break
        case 'gone':
          this.h.onGone(msg.pane as string)
          break
        case 'gh':
          this.h.onGh(msg as unknown as GhBadge)
          break
      }
    }

    ws.onclose = () => {
      this.ws = null
      if (!this.closed) {
        this.h.onConn('offline')
        this.scheduleReconnect()
      }
    }
    ws.onerror = () => ws.close()
  }

  private scheduleReconnect() {
    if (this.closed || this.timer !== null) return
    const wait = this.backoff
    this.backoff = Math.min(8000, this.backoff * 2)
    this.timer = window.setTimeout(() => {
      this.timer = null
      this.connect()
    }, wait)
  }

  private clearTimer() {
    if (this.timer !== null) {
      clearTimeout(this.timer)
      this.timer = null
    }
  }

  private send(v: unknown) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(v))
  }

  subscribe(pane: string | null, lines = 400) {
    this.pane = pane
    this.lines = lines
    if (pane) {
      this.send({ t: 'sub', pane, lines })
      // Tell the server what is on screen so a push about this very pane is
      // suppressed - Kevin is already looking at the question.
      this.send({ t: 'focus', pane })
    } else {
      this.send({ t: 'unsub' })
      this.send({ t: 'focus', pane: '' })
    }
  }

  /** Ask for a fresh frame even if the screen hash has not changed. */
  resume() {
    this.send({ t: 'resume' })
  }

  private onVisibility = () => {
    this.hidden = document.visibilityState === 'hidden'
    if (this.hidden) {
      // Screen off: stop the poller entirely.
      this.send({ t: 'unsub' })
      return
    }
    // Back from a locked phone: reconnect at once rather than waiting out
    // the backoff, and refetch, so we land on the same work, current.
    this.backoff = 500
    if (!this.ws || this.ws.readyState > WebSocket.OPEN) {
      this.clearTimer()
      this.connect()
    } else if (this.pane) {
      this.send({ t: 'sub', pane: this.pane, lines: this.lines })
      this.resume()
    }
  }

  private onOnline = () => {
    this.backoff = 500
    this.clearTimer()
    this.connect()
  }

  // A bound field rather than an inline arrow, for the same reason as the two
  // above: close() can only remove a listener it still has a reference to. An
  // inline one outlives the socket and keeps reporting on a dead handler set.
  private onOffline = () => this.h.onConn('offline')

  close() {
    this.closed = true
    this.clearTimer()
    document.removeEventListener('visibilitychange', this.onVisibility)
    window.removeEventListener('online', this.onOnline)
    window.removeEventListener('offline', this.onOffline)
    this.ws?.close()
  }
}
