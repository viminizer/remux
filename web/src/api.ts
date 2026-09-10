import type { Health, NotifySettings, Tree } from './types'
import type {
  GitHubSnapshot,
  Issue,
  IssuePage,
  PickerRepo,
  PR,
} from './github/types'

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
  let parsed = true
  try {
    body = text ? JSON.parse(text) : null
  } catch {
    body = text
    parsed = false
  }
  // A 200 that is not JSON means the request fell through to the SPA
  // handler, which answers any unknown path with index.html. Treating that
  // as a successful empty result is how a missing route turns into a screen
  // that silently shows nothing.
  if (res.ok && !parsed) {
    throw new ApiError(res.status, `${path} did not return JSON`, text.slice(0, 200))
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

  // ── GitHub ──────────────────────────────────────────────────────────
  //
  // The snapshot is served from the server's shared poller, so this call is
  // a cached read and never waits on GitHub. Everything below it is fetched
  // on demand, because polling 186 issues nobody has opened would spend the
  // API budget on nothing.

  github: () => call<GitHubSnapshot>('/api/github'),

  githubRefresh: () => call('/api/github/refresh', { method: 'POST' }),

  githubIssues: (repo: string, filter: 'mine' | 'all', after?: string) =>
    call<IssuePage>(
      `/api/github/repos/${repo}/issues?filter=${filter}` +
        (after ? `&after=${encodeURIComponent(after)}` : ''),
    ),

  githubPRs: (repo: string) => call<{ prs: PR[] }>(`/api/github/repos/${repo}/prs`),

  githubIssue: (repo: string, number: number) =>
    call<Issue>(`/api/github/repos/${repo}/issues/${number}`),

  githubPR: (repo: string, number: number) =>
    call<PR>(`/api/github/repos/${repo}/prs/${number}`),

  githubPicker: (q: string) =>
    call<{ repos: PickerRepo[] }>(`/api/github/picker${q ? `?q=${encodeURIComponent(q)}` : ''}`),

  githubWatch: (repo: string) =>
    call<{ repos: string[] }>('/api/github/watch', {
      method: 'POST',
      body: JSON.stringify({ repo }),
    }),

  githubUnwatch: (repo: string) =>
    call<{ repos: string[] }>(`/api/github/watch/${repo}`, { method: 'DELETE' }),

  // Both answer with the whole snapshot, so the screen redraws from the
  // server's own view rather than guessing what the change did.
  githubMute: (repo: string, number: number) =>
    call<GitHubSnapshot>('/api/github/mute', {
      method: 'POST',
      body: JSON.stringify({ repo, number }),
    }),

  githubUnmute: (repo: string, number: number) =>
    call<GitHubSnapshot>(`/api/github/mute/${repo}/${number}`, { method: 'DELETE' }),

  pushKey: () => call<{ publicKey: string; subscriptions: number }>('/api/push/key'),

  pushSubscribe: (sub: unknown) =>
    call('/api/push/subscribe', { method: 'POST', body: JSON.stringify(sub) }),

  pushUnsubscribe: (endpoint: string) =>
    call('/api/push/unsubscribe', {
      method: 'POST',
      body: JSON.stringify({ endpoint }),
    }),
}
