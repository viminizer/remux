import { api } from './api'

/**
 * Web Push registration.
 *
 * Notification.requestPermission() needs a user gesture and a denial is
 * painful to reverse on Android, so this is only ever called from an explicit
 * tap - never on launch.
 */

export function pushSupported(): boolean {
  return 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window
}

export function notificationState(): NotificationPermission | 'unsupported' {
  if (!pushSupported()) return 'unsupported'
  return Notification.permission
}

export async function registerServiceWorker(): Promise<ServiceWorkerRegistration | null> {
  if (!('serviceWorker' in navigator)) return null
  try {
    return await navigator.serviceWorker.register('/sw.js')
  } catch {
    return null
  }
}

function urlBase64ToUint8Array(b64: string): Uint8Array {
  const padding = '='.repeat((4 - (b64.length % 4)) % 4)
  const base64 = (b64 + padding).replace(/-/g, '+').replace(/_/g, '/')
  const raw = atob(base64)
  const out = new Uint8Array(raw.length)
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

/** Ask for permission and register with the server. Returns what happened. */
export async function enablePush(): Promise<'ok' | 'denied' | 'unsupported' | 'error'> {
  if (!pushSupported()) return 'unsupported'

  const permission = await Notification.requestPermission()
  if (permission !== 'granted') return 'denied'

  const reg = await registerServiceWorker()
  if (!reg) return 'error'

  try {
    const { publicKey } = await api.pushKey()
    const sub = await reg.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: urlBase64ToUint8Array(publicKey) as BufferSource,
    })
    await api.pushSubscribe(sub.toJSON())
    return 'ok'
  } catch {
    return 'error'
  }
}

/**
 * Pulls a new UI build.
 *
 * From a home-screen PWA there is no address bar and so no reload affordance,
 * and the app is a non-scrolling layout, so pull-to-refresh is not available
 * either. Closing the app entirely is the only other way to get a new build,
 * which is not something to ask of anyone.
 *
 * registration.update() is what forces the browser to re-check sw.js. The
 * worker already calls skipWaiting and clients.claim, so a new one takes over
 * immediately and the reload lands on it.
 *
 * The update check is best-effort: no service worker support, or storage
 * blocked, must not cost you the reload.
 */
export async function pullNewBuild(): Promise<void> {
  try {
    const reg = await navigator.serviceWorker?.getRegistration()
    await reg?.update()
  } catch {
    // Falling through to the reload is the whole point.
  }
  location.reload()
}
