// HTTP client for the notification service. Every failure is mapped to a kind
// the interface can explain, so no screen shows a raw status code.

import { demoEvents } from './demo'

export type AttentionClass = 'critical' | 'standard' | 'background'

export interface DecisionState {
  status: string
  reason: string
  matchedRule?: string
  modelVersion?: string
  confidence?: number
}

export interface EventSummary {
  id: string
  source: string
  eventType: string
  repository?: string
  attentionClass: AttentionClass
  decisionState?: DecisionState
  threadId?: string
  mergedInto?: string
  createdAt: string
}

export type FeedbackAction = 'dismissed' | 'always' | 'never' | 'undo' | 'reply'

export interface Settings {
  backendUrl: string
  apiKey: string
  /** Use built-in sample data instead of calling a service. */
  demo?: boolean
}

export type ApiErrorKind = 'unauthorized' | 'unconfigured' | 'unreachable' | 'rejected' | 'server'

export class ApiError extends Error {
  constructor(
    readonly kind: ApiErrorKind,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

function endpoint(settings: Settings, path: string): string {
  return settings.backendUrl.replace(/\/+$/, '') + path
}

export async function request(settings: Settings, path: string, init: RequestInit = {}): Promise<Response> {
  let res: Response
  try {
    res = await fetch(endpoint(settings, path), {
      ...init,
      headers: {
        ...(init.body ? { 'Content-Type': 'application/json' } : {}),
        ...(settings.apiKey ? { 'X-API-Key': settings.apiKey } : {}),
      },
    })
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') throw err
    throw new ApiError('unreachable', 'The service could not be reached.')
  }
  if (res.ok) return res
  if (res.status === 401) throw new ApiError('unauthorized', 'The API key was rejected.')
  if (res.status === 503) throw new ApiError('unconfigured', 'The service has no API key configured.')
  if (res.status >= 400 && res.status < 500) {
    const body = (await res.json().catch(() => ({}))) as { error?: string }
    throw new ApiError('rejected', body.error ?? 'The service rejected the request.')
  }
  throw new ApiError('server', 'The service hit an error.')
}

export async function fetchEvents(settings: Settings, signal?: AbortSignal, limit = 200): Promise<EventSummary[]> {
  if (settings.demo) return demoEvents()
  const res = await request(settings, `/events?limit=${limit}`, { signal })
  const body = (await res.json()) as { events: EventSummary[] }
  return body.events
}

export async function sendFeedback(
  settings: Settings,
  feedback: { eventId: string; action: FeedbackAction; reason?: string },
): Promise<void> {
  if (settings.demo) return // nothing is stored in demo mode
  await request(settings, '/feedback', { method: 'POST', body: JSON.stringify(feedback) })
}
