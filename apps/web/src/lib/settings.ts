import type { Settings } from './api'

const STORAGE_KEY = 'attention.settings'
const DEFAULT_BACKEND = 'http://localhost:8080'

/**
 * Settings live in sessionStorage, so the API key is forgotten when the tab
 * closes. VITE_API_KEY seeds local development only: anything in a built
 * bundle is public.
 */
export function loadSettings(): Settings {
  const defaults: Settings = {
    backendUrl: import.meta.env.VITE_BACKEND_URL || DEFAULT_BACKEND,
    apiKey: import.meta.env.VITE_API_KEY ?? '',
    ...(import.meta.env.VITE_DEFAULT_DEMO === 'true' ? { demo: true } : {}),
  }
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY)
    if (!raw) return defaults
    const stored = JSON.parse(raw) as Partial<Settings>
    return {
      backendUrl: typeof stored.backendUrl === 'string' && stored.backendUrl ? stored.backendUrl : defaults.backendUrl,
      apiKey: typeof stored.apiKey === 'string' ? stored.apiKey : defaults.apiKey,
      ...(stored.demo === true ? { demo: true } : {}),
    }
  } catch {
    return defaults
  }
}

export function saveSettings(settings: Settings): void {
  sessionStorage.setItem(STORAGE_KEY, JSON.stringify(settings))
}

/**
 * True on a first visit with nothing chosen yet, when the console should ask
 * whether to try the demo or connect a service instead of showing an error.
 */
export function needsWelcome(): boolean {
  if (import.meta.env.VITE_API_KEY || import.meta.env.VITE_DEFAULT_DEMO === 'true') return false
  try {
    return sessionStorage.getItem(STORAGE_KEY) === null
  } catch {
    return true
  }
}

/** Returns the message key for what is wrong with a backend URL, or null when it is usable. */
export function validateBackendUrl(value: string): 'settings.urlScheme' | 'settings.urlInvalid' | null {
  try {
    const url = new URL(value)
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return 'settings.urlScheme'
    return null
  } catch {
    return 'settings.urlInvalid'
  }
}

/**
 * What "Connect your service" saves from the welcome screen: the service address
 * as it stands, no API key and no demo mode, so the sign-in page comes next.
 * The API key stays available under "Advanced" for services.
 */
export function connectSettings(s: Settings): Settings {
  return { backendUrl: s.backendUrl, apiKey: '' }
}

/**
 * The settings after signing out. The address stays; an API key does not, so
 * signing out really ends access and a reload does not walk straight back in.
 */
export function signedOutSettings(s: Settings): Settings {
  return { backendUrl: s.backendUrl, apiKey: '' }
}
