// The inbox contract: types shared with the service, and one client interface
// with two implementations (HTTP for a real service, in memory for demo mode),
// so screens never branch on where the data comes from.

import { request, type AttentionClass, type DecisionState, type Settings } from './api'
import { demoInbox } from './demoInbox'

export type Level = 'critical' | 'high' | 'normal' | 'low'
export type Outcome = 'delivered' | 'held' | 'suppressed' | 'merged' | 'recorded'
export type SortOrder = 'newest' | 'oldest' | 'priority' | 'lowest' | 'updated'

export interface InboxItem {
  id: string
  source: string
  eventType: string
  repository?: string
  attentionClass: AttentionClass
  priority: Level
  outcome: Outcome
  decisionState?: DecisionState
  threadId?: string
  mergedInto?: string
  repeatCount: number
  createdAt: string
  updatedAt: string
  read: boolean
  readAt?: string
  archivedAt?: string
  acknowledgedAt?: string
  resolvedAt?: string
  snoozedUntil?: string
  priorityOverride?: Level
}

export interface TrailEntry {
  step: string
  detail?: string
  at: string
  /** Derived from stored fields, not recorded when it happened. */
  reconstructed?: boolean
}

export interface InboxDetail {
  item: InboxItem
  trail: TrailEntry[]
  related: InboxItem[]
}

export interface Counts {
  all: number
  unread: number
  critical: number
  high: number
  normal: number
  low: number
  held: number
  delivered: number
  suppressed: number
  archived: number
}

export interface InboxPage {
  items: InboxItem[]
  total: number
  page: number
  pageSize: number
  counts: Counts
}

/** What the list asks the service for. */
export interface InboxQuery {
  text: string
  priorities: Level[]
  outcomes: Outcome[]
  unread: boolean
  archived: boolean
  sort: SortOrder
  /** ISO time; only events created at or after it. */
  from?: string
  /** ISO time; only events created before it. */
  to?: string
  /** Exact source, for example "github". */
  source?: string
  /** Exact repository. */
  repository?: string
  page: number
  pageSize: number
}

export type ActionName =
  | 'read'
  | 'unread'
  | 'archive'
  | 'unarchive'
  | 'delete'
  | 'acknowledge'
  | 'resolve'
  | 'snooze'
  | 'priority'

export interface Action {
  type: ActionName
  /** snooze: how long. */
  minutes?: number
  /** priority: the new level, or '' to clear the override. */
  priority?: Level | ''
}

export interface BulkResult {
  id: string
  ok: boolean
  error?: string
}

export interface InboxClient {
  list(query: InboxQuery, signal?: AbortSignal): Promise<InboxPage>
  get(id: string, signal?: AbortSignal): Promise<InboxDetail>
  act(id: string, action: Action): Promise<void>
  bulk(ids: string[], action: Action): Promise<BulkResult[]>
  markAllRead(): Promise<number>
}

const BASE = '/api/notifications'

export function queryString(q: InboxQuery): string {
  const p = new URLSearchParams()
  if (q.text.trim()) p.set('q', q.text.trim())
  if (q.priorities.length) p.set('priority', q.priorities.join(','))
  if (q.outcomes.length) p.set('outcome', q.outcomes.join(','))
  if (q.unread) p.set('read', 'false')
  if (q.archived) p.set('archived', 'true')
  if (q.sort !== 'newest') p.set('sort', q.sort)
  if (q.from) p.set('from', q.from)
  if (q.to) p.set('to', q.to)
  if (q.source) p.set('source', q.source)
  if (q.repository) p.set('repo', q.repository)
  p.set('page', String(q.page))
  p.set('pageSize', String(q.pageSize))
  return p.toString()
}

async function json<T>(res: Response): Promise<T> {
  return (await res.json()) as T
}

function send(settings: Settings, path: string, method: string, body?: unknown): Promise<Response> {
  return request(settings, path, { method, ...(body === undefined ? {} : { body: JSON.stringify(body) }) })
}

/** Talks to the notification service. */
export function httpInbox(settings: Settings): InboxClient {
  const item = (id: string) => `${BASE}/${encodeURIComponent(id)}`
  return {
    async list(query, signal) {
      return json<InboxPage>(await request(settings, `${BASE}?${queryString(query)}`, { signal }))
    },
    async get(id, signal) {
      return json<InboxDetail>(await request(settings, item(id), { signal }))
    },
    async act(id, a) {
      switch (a.type) {
        case 'read':
        case 'unread':
          await send(settings, item(id), 'PATCH', { read: a.type === 'read' })
          return
        case 'archive':
        case 'unarchive':
          await send(settings, item(id), 'PATCH', { archived: a.type === 'archive' })
          return
        case 'priority':
          await send(settings, item(id), 'PATCH', { priority: a.priority ?? '' })
          return
        case 'delete':
          await send(settings, item(id), 'DELETE')
          return
        case 'acknowledge':
        case 'resolve':
          await send(settings, `${item(id)}/${a.type}`, 'POST')
          return
        case 'snooze':
          await send(settings, `${item(id)}/snooze`, 'POST', { minutes: a.minutes })
      }
    },
    async bulk(ids, a) {
      const res = await send(settings, `${BASE}/bulk-actions`, 'POST', {
        ids,
        action: a.type,
        minutes: a.minutes,
        priority: a.priority,
      })
      return (await json<{ results: BulkResult[] }>(res)).results
    },
    async markAllRead() {
      return (await json<{ changed: number }>(await send(settings, `${BASE}/mark-all-read`, 'POST'))).changed
    },
  }
}

/** Chooses the client for the current connection. */
export function inboxClientFor(settings: Settings): InboxClient {
  return settings.demo ? demoInbox : httpInbox(settings)
}
