// What support needs to know, gathered without secrets: the API key is never
// read here, and the service address is reduced to its origin.

import type { Settings } from './api'
import type { LiveStatus } from './live'

export type Health = 'ok' | 'unreachable' | 'unhealthy' | 'demo' | 'unset'

export interface HealthResult {
  health: Health
  /** Round trip in milliseconds, when the service answered. */
  ms?: number
}

/** Calls the unauthenticated /health endpoint. Never throws. */
export async function checkHealth(settings: Settings, fetchImpl: typeof fetch = fetch, signal?: AbortSignal): Promise<HealthResult> {
  if (settings.demo) return { health: 'demo' }
  const base = settings.backendUrl.trim().replace(/\/+$/, '')
  if (base === '') return { health: 'unset' }
  const started = performance.now()
  try {
    const res = await fetchImpl(`${base}/health`, { signal })
    const ms = Math.round(performance.now() - started)
    if (!res.ok) return { health: 'unhealthy', ms }
    const body = (await res.json().catch(() => ({}))) as { status?: string }
    return { health: body.status === 'ok' ? 'ok' : 'unhealthy', ms }
  } catch {
    return { health: 'unreachable' }
  }
}

/** The origin of a service address, or the text "not set". Paths, credentials and queries are dropped. */
export function safeOrigin(address: string): string {
  const trimmed = address.trim()
  if (trimmed === '') return 'not set'
  try {
    return new URL(trimmed).origin
  } catch {
    return 'invalid address'
  }
}

export interface DiagnosticsInput {
  appVersion: string
  settings: Settings
  health: HealthResult
  live: LiveStatus
  language: string
  unread: number
  userAgent: string
  now: Date
}

/** A plain-text block to paste into a support request. */
export function formatDiagnostics(d: DiagnosticsInput): string {
  const lines = [
    `Attention console ${d.appVersion}`,
    `Time: ${d.now.toISOString()}`,
    `Mode: ${d.settings.demo ? 'demo' : 'connected'}`,
    `Service: ${d.settings.demo ? 'not used' : safeOrigin(d.settings.backendUrl)}`,
    `API key: ${d.settings.apiKey ? 'entered' : 'not entered'}`,
    `Health: ${d.health.health}${d.health.ms !== undefined ? ` (${d.health.ms} ms)` : ''}`,
    `Live updates: ${d.live}`,
    `Unread: ${d.unread}`,
    `Language: ${d.language}`,
    `Browser: ${d.userAgent}`,
  ]
  return lines.join('\n') + '\n'
}
