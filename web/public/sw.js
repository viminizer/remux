/* remux service worker.
 *
 * It earns its place twice: it caches the app shell so the app opens without a
 * server, and it handles push, which is what turns "an agent needs an answer"
 * into a lock-screen notification.
 *
 * API responses are deliberately never cached. Pane content that looked live
 * but came from a cache would break the one rule the offline design has, so
 * snapshots are kept in localStorage by the app, where they are explicitly
 * labelled stale.
 */

const SHELL = 'remux-shell-v1'

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(SHELL).then((c) => c.addAll(['/', '/index.html', '/manifest.webmanifest', '/icon.svg'])),
  )
  self.skipWaiting()
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) => Promise.all(keys.filter((k) => k !== SHELL).map((k) => caches.delete(k)))),
  )
  self.clients.claim()
})

self.addEventListener('fetch', (event) => {
  const req = event.request
  const url = new URL(req.url)

  if (req.method !== 'GET' || url.origin !== self.location.origin) return
  // Never cache the API or the socket.
  if (url.pathname.startsWith('/api/') || url.pathname === '/ws') return

  // Network first, so an upgraded shell is picked up as soon as the Mac is
  // reachable, with the cache as the fallback.
  event.respondWith(
    fetch(req)
      .then((res) => {
        const copy = res.clone()
        caches.open(SHELL).then((c) => c.put(req, copy)).catch(() => {})
        return res
      })
      .catch(() =>
        caches.match(req).then((hit) => hit || caches.match('/index.html')),
      ),
  )
})

self.addEventListener('push', (event) => {
  let data = {}
  try {
    data = event.data ? event.data.json() : {}
  } catch (e) {
    data = {}
  }
  const title = data.title || 'remux'
  event.waitUntil(
    self.registration.showNotification(title, {
      body: data.body || 'An agent needs an answer.',
      tag: data.tag || data.pane || 'remux',
      renotify: false,
      icon: '/icon.svg',
      badge: '/icon.svg',
      data: { pane: data.pane || '' },
    }),
  )
})

/* One tap from the lock screen to the pane that needs an answer. */
self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const pane = (event.notification.data && event.notification.data.pane) || ''
  const target = pane ? `/#/p/${encodeURIComponent(pane)}` : '/'

  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((list) => {
      for (const client of list) {
        if (client.url.includes(self.location.origin)) {
          client.navigate(target)
          return client.focus()
        }
      }
      return self.clients.openWindow(target)
    }),
  )
})
