// An in-memory inbox for demo mode. It follows the same rules as the service
// (go-server pkg/inbox) over the sample events, so the hosted demo behaves like
// the real thing. Nothing is stored: reloading the page resets it.

import type { EventSummary } from './api'
import { demoEvents } from './demo'
import type {
  Action,
  BulkResult,
  Counts,
  InboxClient,
  InboxDetail,
  InboxItem,
  InboxPage,
  InboxQuery,
  Level,
  TrailEntry,
} from './inbox'
import { levelOf, levelRank, outcomeOf } from './priority'

interface State {
  readAt?: string
  archivedAt?: string
  deletedAt?: string
  acknowledgedAt?: string
  resolvedAt?: string
  snoozedUntil?: string
  override?: Level
  updatedAt?: string
  trail: TrailEntry[]
}

const MAX_BULK = 100

let events: EventSummary[] | null = null
const states = new Map<string, State>()

function all(): EventSummary[] {
  events ??= demoEvents()
  return events
}

function stateOf(id: string): State {
  let s = states.get(id)
  if (!s) {
    s = { trail: [] }
    states.set(id, s)
  }
  return s
}

function toItem(e: EventSummary): InboxItem {
  const s = stateOf(e.id)
  const repeats = all().filter((x) => x.mergedInto === e.id).length
  return {
    ...e,
    priority: levelOf(e, s.override),
    outcome: outcomeOf(e),
    repeatCount: repeats,
    updatedAt: s.updatedAt && s.updatedAt > e.createdAt ? s.updatedAt : e.createdAt,
    read: s.readAt !== undefined,
    readAt: s.readAt,
    archivedAt: s.archivedAt,
    acknowledgedAt: s.acknowledgedAt,
    resolvedAt: s.resolvedAt,
    snoozedUntil: s.snoozedUntil,
    priorityOverride: s.override,
  }
}

function visible(): EventSummary[] {
  return all().filter((e) => !stateOf(e.id).deletedAt)
}

function matches(it: InboxItem, q: InboxQuery): boolean {
  if (q.priorities.length && !q.priorities.includes(it.priority)) return false
  if (q.outcomes.length && !q.outcomes.includes(it.outcome)) return false
  if (q.unread && it.read) return false
  if (q.from && it.createdAt < q.from) return false
  const terms = q.text.toLowerCase().split(/\s+/).filter(Boolean)
  if (terms.length === 0) return true
  const text = [
    it.id,
    it.source,
    it.eventType.replace(/_/g, ' '),
    it.repository ?? '',
    it.priority,
    it.outcome,
    it.decisionState?.status ?? '',
    it.decisionState?.reason ?? '',
    it.decisionState?.matchedRule ?? '',
  ]
    .join(' ')
    .toLowerCase()
  return terms.every((t) => text.includes(t))
}

function sorted(items: InboxItem[], order: InboxQuery['sort']): InboxItem[] {
  const newest = (a: InboxItem, b: InboxItem) => b.createdAt.localeCompare(a.createdAt)
  return [...items].sort((a, b) => {
    switch (order) {
      case 'oldest':
        return a.createdAt.localeCompare(b.createdAt)
      case 'priority':
        return levelRank(a.priority) - levelRank(b.priority) || newest(a, b)
      case 'lowest':
        return levelRank(b.priority) - levelRank(a.priority) || newest(a, b)
      case 'updated':
        return b.updatedAt.localeCompare(a.updatedAt) || newest(a, b)
      default:
        return newest(a, b)
    }
  })
}

function counts(now: number): Counts {
  const c: Counts = { all: 0, unread: 0, critical: 0, high: 0, normal: 0, low: 0, held: 0, delivered: 0, suppressed: 0, archived: 0 }
  for (const e of visible()) {
    const it = toItem(e)
    if (it.archivedAt) {
      c.archived++
      continue
    }
    if (e.mergedInto) continue
    c.all++
    const snoozed = it.snoozedUntil !== undefined && Date.parse(it.snoozedUntil) > now
    if (!it.read && !snoozed) c.unread++
    c[it.priority]++
    if (it.outcome === 'held' || it.outcome === 'delivered' || it.outcome === 'suppressed') c[it.outcome]++
  }
  return c
}

function trailFor(e: EventSummary): TrailEntry[] {
  const at = e.createdAt
  const d = e.decisionState
  const steps: TrailEntry[] = [
    { step: 'received', detail: `${e.source} ${e.eventType}`, at, reconstructed: true },
    { step: 'classified', detail: e.attentionClass, at, reconstructed: true },
    { step: 'rules', detail: d?.matchedRule || 'no rule matched', at, reconstructed: true },
    { step: 'dedup', detail: e.mergedInto ? `merged into ${e.mergedInto}` : 'not a repeat', at, reconstructed: true },
  ]
  if (d) steps.push({ step: 'outcome', detail: d.status, at, reconstructed: true })
  return steps
}

function apply(id: string, a: Action): void {
  const e = visible().find((x) => x.id === id)
  if (!e) throw new Error('notification not found')
  const s = stateOf(id)
  const now = new Date().toISOString()
  let detail = ''
  switch (a.type) {
    case 'read':
      s.readAt ??= now
      break
    case 'unread':
      s.readAt = undefined
      break
    case 'archive':
      s.archivedAt = now
      break
    case 'unarchive':
      s.archivedAt = undefined
      break
    case 'delete':
      s.deletedAt = now
      break
    case 'acknowledge':
      s.acknowledgedAt = now
      s.readAt ??= now
      break
    case 'resolve':
      s.resolvedAt = now
      s.readAt ??= now
      break
    case 'snooze': {
      const minutes = a.minutes ?? 0
      if (minutes < 1 || minutes > 30 * 24 * 60) throw new Error('snooze must be between 1 minute and 30 days')
      s.snoozedUntil = new Date(Date.now() + minutes * 60_000).toISOString()
      detail = `until ${s.snoozedUntil}`
      break
    }
    case 'priority':
      if (e.attentionClass === 'critical') throw new Error('critical events cannot be reprioritised')
      if (a.priority === 'critical') throw new Error('priority must be high, normal or low')
      s.override = a.priority || undefined
      detail = a.priority || 'cleared'
      break
  }
  s.updatedAt = now
  if (a.type !== 'read' && a.type !== 'unread') s.trail.push({ step: a.type, detail, at: now })
}

export const demoInbox: InboxClient = {
  async list(q) {
    const now = Date.now()
    const items = visible()
      .filter((e) => (stateOf(e.id).archivedAt !== undefined) === q.archived)
      .filter((e) => !e.mergedInto || q.outcomes.includes('merged'))
      .map(toItem)
      .filter((it) => matches(it, q))
    const ordered = sorted(items, q.sort)
    const size = Math.min(Math.max(q.pageSize, 1), 100)
    const pages = Math.max(1, Math.ceil(ordered.length / size))
    const page = Math.min(Math.max(q.page, 1), pages)
    const result: InboxPage = {
      items: ordered.slice((page - 1) * size, page * size),
      total: ordered.length,
      page,
      pageSize: size,
      counts: counts(now),
    }
    return result
  },

  async get(id) {
    const e = visible().find((x) => x.id === id)
    if (!e) throw new Error('notification not found')
    const related = visible()
      .filter((x) => x.id !== id && ((e.threadId && x.threadId === e.threadId) || (e.repository && x.repository === e.repository)))
      .map(toItem)
      .sort((a, b) => b.createdAt.localeCompare(a.createdAt))
      .slice(0, 10)
    const detail: InboxDetail = { item: toItem(e), trail: [...trailFor(e), ...stateOf(id).trail], related }
    return detail
  },

  async act(id, a) {
    apply(id, a)
  },

  async bulk(ids, a) {
    if (ids.length === 0 || ids.length > MAX_BULK) throw new Error(`ids must hold 1 to ${MAX_BULK} entries`)
    return ids.map<BulkResult>((id) => {
      try {
        apply(id, a)
        return { id, ok: true }
      } catch (err) {
        return { id, ok: false, error: err instanceof Error ? err.message : 'could not apply the action' }
      }
    })
  },

  async markAllRead() {
    let changed = 0
    for (const e of visible()) {
      const s = stateOf(e.id)
      if (s.readAt || s.archivedAt) continue
      apply(e.id, { type: 'read' })
      changed++
    }
    return changed
  },
}

/** Forgets every change; for tests. */
export function resetDemoInbox(): void {
  states.clear()
  events = null
}
