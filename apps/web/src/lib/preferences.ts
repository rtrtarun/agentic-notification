// Workspace preferences: quiet hours and muted sources. The service stores them
// and applies them to standard and background events only; critical events are
// never delayed or muted (go-server pkg/preferences). Demo mode keeps an
// in-memory copy.

import { request, type Settings } from './api'

export interface QuietHours {
  enabled: boolean
  /** 24-hour "HH:MM" in `timezone`. */
  start: string
  end: string
  timezone: string
}

export interface Preferences {
  quietHours: QuietHours
  mutedSources: string[]
  mutedRepositories: string[]
  updatedAt?: string
}

export interface PreferencesClient {
  get(signal?: AbortSignal): Promise<Preferences>
  save(prefs: Preferences): Promise<Preferences>
}

export const DEFAULT_PREFERENCES: Preferences = {
  quietHours: { enabled: false, start: '22:00', end: '07:00', timezone: 'UTC' },
  mutedSources: [],
  mutedRepositories: [],
}

const CLOCK = /^([01]\d|2[0-3]):[0-5]\d$/

export type PreferenceProblem = 'start' | 'end' | 'same' | null

/** Checks quiet hours the way the service does, so the form can answer before sending. */
export function quietHoursProblem(q: QuietHours): PreferenceProblem {
  if (!CLOCK.test(q.start)) return 'start'
  if (!CLOCK.test(q.end)) return 'end'
  if (q.enabled && q.start === q.end) return 'same'
  return null
}

/** Adds a value to a list: trimmed, de-duplicated, ignoring blanks. Sources are lower-cased. */
export function addToList(list: string[], raw: string, lower: boolean): string[] {
  const value = lower ? raw.trim().toLowerCase() : raw.trim()
  if (value === '' || list.includes(value)) return list
  return [...list, value]
}

export function removeFromList(list: string[], value: string): string[] {
  return list.filter((v) => v !== value)
}

/** The browser's own time zone, falling back to UTC. */
export function localTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

/** Time zones the browser knows, with the current one and UTC always present. */
export function timezones(current: string): string[] {
  let list: string[] = []
  try {
    list = (Intl as unknown as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf?.('timeZone') ?? []
  } catch {
    list = []
  }
  const set = new Set(['UTC', ...list])
  if (current) set.add(current)
  return [...set].sort((a, b) => (a === 'UTC' ? -1 : b === 'UTC' ? 1 : a.localeCompare(b)))
}

/** Keeps a draft in demo mode between visits to the screen. */
let demoPrefs: Preferences = structuredClone(DEFAULT_PREFERENCES)

export const demoPreferences: PreferencesClient = {
  async get() {
    return structuredClone(demoPrefs)
  },
  async save(prefs) {
    const problem = quietHoursProblem(prefs.quietHours)
    if (problem) throw new Error('invalid preferences: check the quiet hours times')
    demoPrefs = { ...structuredClone(prefs), updatedAt: new Date().toISOString() }
    return structuredClone(demoPrefs)
  },
}

export function httpPreferences(settings: Settings): PreferencesClient {
  return {
    async get(signal) {
      return (await (await request(settings, '/api/preferences', { signal })).json()) as Preferences
    },
    async save(prefs) {
      return (await (await request(settings, '/api/preferences', { method: 'PUT', body: JSON.stringify(prefs) })).json()) as Preferences
    },
  }
}

export function preferencesClientFor(settings: Settings): PreferencesClient {
  return settings.demo ? demoPreferences : httpPreferences(settings)
}
