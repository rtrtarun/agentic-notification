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
    backendUrl: import.meta.env.VITE_BACKEND_URL ?? DEFAULT_BACKEND,
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

/** Returns an error message for an invalid backend URL, or null when it is usable. */
export function validateBackendUrl(value: string): string | null {
  try {
    const url = new URL(value)
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return 'Use a URL that starts with http:// or https://.'
    return null
  } catch {
    return 'Enter a full URL, for example http://localhost:8080.'
  }
}
