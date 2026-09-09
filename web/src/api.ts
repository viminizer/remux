import type { Health, NotifySettings, Tree } from './types'

/**
 * There is no server URL to configure and no token to send.
 *
 * The UI is served by the same binary that talks to tmux, so the API is always
 * same-origin, and identity comes from the tailnet connection itself via
 * WhoIs. That is why this file has no auth code in it at all.
 */

export class ApiError extends Error {
  status: number
  body: unknown
  constructor(status: number, message: string, body?: unknown) {
    super(message)
    this.status = status
    this.body = body
  }
}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...(init?.headers ?? {}) },
  })
  const text = await res.text()
  let body: unknown = null
  try {
    body = text ? JSON.parse(text) : null
  } catch {
    body = text
  }
  if (!res.ok) {
    const msg =
      body && typeof body === 'object' && 'error' in body
        ? String((body as { error: unknown }).error)
        : res.statusText
    throw new ApiError(res.status, msg, body)
  }
  return body as T
}

export const api = {
  health: () => call<Health>('/api/health'),
  tree: (preview = true) => call<Tree>(`/api/tree${preview ? '?preview=1' : ''}`),

  capture: (pane: string, lines = 400) =>
    call<{ pane: string; lines: string[] }>(
      `/api/panes/${encodeURIComponent(pane)}/capture?lines=${lines}`,
    ),

  text: (pane: string, text: string, submit: boolean) =>
    call(`/api/panes/${encodeURIComponent(pane)}/text`, {
      method: 'POST',
      body: JSON.stringify({ text, submit }),
    }),

  keys: (pane: string, keys: string[]) =>
    call(`/api/panes/${encodeURIComponent(pane)}/keys`, {
      method: 'POST',
      body: JSON.stringify({ keys }),
    }),

  interrupt: (pane: string) =>
    call(`/api/panes/${encodeURIComponent(pane)}/interrupt`, { method: 'POST' }),

  /** The one call that moves the laptop's own cursor, and the UI says so. */
  focus: (pane: string) =>
    call(`/api/panes/${encodeURIComponent(pane)}/focus`, { method: 'POST' }),

  killPane: (pane: string) =>
    call(`/api/panes/${encodeURIComponent(pane)}`, { method: 'DELETE' }),

  /** Splits an existing pane, agent or not. Halving an agent's pane reflows
      it, so the sheet puts that case behind a hold. */
  newPane: (pane: string, direction: 'right' | 'below') =>
    call<{ id: string }>('/api/panes', {
      method: 'POST',
      body: JSON.stringify({ pane, direction }),
    }),

  newSession: (name: string, path: string) =>
    call<{ id: string }>('/api/sessions', {
      method: 'POST',
      body: JSON.stringify({ name, path }),
    }),

  newWindow: (sessionId: string, name: string, path: string) =>
    call<{ id: string }>('/api/windows', {
      method: 'POST',
      body: JSON.stringify({ sessionId, name, path }),
    }),

  /** Names one pane. An empty name hands it back to the program's own title. */
  renamePane: (id: string, name: string) =>
    call(`/api/panes/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      body: JSON.stringify({ name }),
    }),

  // No renameWindow, renameSession, killWindow or killSession here. The UI
  // exposes panes as the unit and sessions only as grouping, so nothing could
  // call them - they sat unused, implying levels the user cannot reach. The
  // server keeps those routes; adding a client call back is the small half of
  // exposing one deliberately.

  settings: () => call<NotifySettings>('/api/settings'),

  saveSettings: (v: NotifySettings) =>
    call<NotifySettings>('/api/settings', { method: 'PUT', body: JSON.stringify(v) }),

  pushKey: () => call<{ publicKey: string; subscriptions: number }>('/api/push/key'),

  pushSubscribe: (sub: unknown) =>
    call('/api/push/subscribe', { method: 'POST', body: JSON.stringify(sub) }),

  pushUnsubscribe: (endpoint: string) =>
    call('/api/push/unsubscribe', {
      method: 'POST',
      body: JSON.stringify({ endpoint }),
    }),
}
