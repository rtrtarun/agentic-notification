// Analytics and the activity timeline. The service computes both for a real
// connection (go-server pkg/insights); demo mode computes the same numbers here
// from the sample data, with the same rules, so the screens never branch.

import { request, type Settings } from './api'
import { demoActions, demoAllItems } from './demoInbox'
import type { InboxItem, Level, Outcome } from './inbox'

export type AnalyticsRange = '24h' | '7d' | '30d'
export const ANALYTICS_RANGES: AnalyticsRange[] = ['24h', '7d', '30d']

export interface Totals {
  events: number
  critical: number
  high: number
  normal: number
  low: number
  delivered: number
  held: number
  suppressed: number
  merged: number
  recorded: number
  unread: number
}

export interface Bucket {
  start: string
  critical: number
  high: number
  normal: number
  low: number
}

export interface Ranked {
  name: string
  count: number
}

export interface Attention {
  interruptions: number
  heldBack: number
  heldBackPercent: number
  readCount: number
  medianReadSeconds: number | null
  acknowledged: number
  resolved: number
  openCritical: number
}

export interface Analytics {
  range: AnalyticsRange
  from: string
  to: string
  bucketSeconds: number
  totals: Totals
  attention: Attention
  series: Bucket[]
  topSources: Ranked[]
  topRepositories: Ranked[]
  topEvents: Ranked[]
}

export interface ActivityEntry {
  at: string
  kind: 'received' | 'action'
  step?: string
  detail?: string
  eventId: string
  source: string
  eventType: string
  repository?: string
  priority: Level
  outcome?: Outcome
  reason?: string
}

export interface InsightsClient {
  analytics(range: AnalyticsRange, signal?: AbortSignal): Promise<Analytics>
  activity(limit: number, signal?: AbortSignal): Promise<ActivityEntry[]>
}

const SPECS: Record<AnalyticsRange, { spanMs: number; buckets: number }> = {
  '24h': { spanMs: 24 * 3_600_000, buckets: 24 },
  '7d': { spanMs: 7 * 86_400_000, buckets: 7 },
  '30d': { spanMs: 30 * 86_400_000, buckets: 30 },
}

const TOP_N = 5

function top(counts: Map<string, number>): Ranked[] {
  return [...counts.entries()]
    .map(([name, count]) => ({ name, count }))
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name))
    .slice(0, TOP_N)
}

/** Mirrors go-server pkg/insights Analyze. Used for demo mode and to test the screens against known numbers. */
export function analyze(items: InboxItem[], now: Date, range: AnalyticsRange): Analytics {
  const { spanMs, buckets } = SPECS[range]
  const to = now.getTime()
  const from = to - spanMs
  const width = spanMs / buckets
  const totals: Totals = { events: 0, critical: 0, high: 0, normal: 0, low: 0, delivered: 0, held: 0, suppressed: 0, merged: 0, recorded: 0, unread: 0 }
  const series: Bucket[] = Array.from({ length: buckets }, (_, i) => ({
    start: new Date(from + i * width).toISOString(),
    critical: 0,
    high: 0,
    normal: 0,
    low: 0,
  }))
  const sources = new Map<string, number>()
  const repos = new Map<string, number>()
  const events = new Map<string, number>()
  const readSeconds: number[] = []
  let acknowledged = 0
  let resolved = 0
  let openCritical = 0

  for (const it of items) {
    const at = Date.parse(it.createdAt)
    if (at < from || at > to) continue
    totals.events++
    totals[it.priority]++
    totals[it.outcome]++
    if (!it.read) totals.unread++
    series[Math.min(buckets - 1, Math.floor((at - from) / width))][it.priority]++
    sources.set(it.source, (sources.get(it.source) ?? 0) + 1)
    if (it.repository) repos.set(it.repository, (repos.get(it.repository) ?? 0) + 1)
    events.set(it.eventType, (events.get(it.eventType) ?? 0) + 1)
    if (it.readAt && Date.parse(it.readAt) >= at) readSeconds.push(Math.floor((Date.parse(it.readAt) - at) / 1000))
    if (it.acknowledgedAt) acknowledged++
    if (it.resolvedAt) resolved++
    if (it.priority === 'critical' && !it.acknowledgedAt && !it.resolvedAt) openCritical++
  }

  readSeconds.sort((a, b) => a - b)
  const heldBack = totals.events - totals.delivered
  return {
    range,
    from: new Date(from).toISOString(),
    to: new Date(to).toISOString(),
    bucketSeconds: Math.floor(width / 1000),
    totals,
    attention: {
      interruptions: totals.delivered,
      heldBack,
      heldBackPercent: totals.events === 0 ? 0 : (heldBack * 100) / totals.events,
      readCount: readSeconds.length,
      medianReadSeconds: readSeconds.length === 0 ? null : readSeconds[Math.floor(readSeconds.length / 2)],
      acknowledged,
      resolved,
      openCritical,
    },
    series,
    topSources: top(sources),
    topRepositories: top(repos),
    topEvents: top(events),
  }
}

/** Mirrors go-server pkg/insights BuildActivity. */
export function buildActivity(
  items: InboxItem[],
  actions: { eventId: string; step: string; detail?: string; at: string }[],
  limit: number,
): ActivityEntry[] {
  const byId = new Map(items.map((i) => [i.id, i]))
  const base = (i: InboxItem) => ({ eventId: i.id, source: i.source, eventType: i.eventType, repository: i.repository, priority: i.priority })
  const entries: ActivityEntry[] = items.map((i) => ({
    ...base(i),
    at: i.createdAt,
    kind: 'received' as const,
    outcome: i.outcome,
    reason: i.decisionState?.reason,
  }))
  for (const a of actions) {
    const it = byId.get(a.eventId)
    if (it) entries.push({ ...base(it), at: a.at, kind: 'action', step: a.step, detail: a.detail })
  }
  return entries.sort((a, b) => b.at.localeCompare(a.at)).slice(0, limit)
}

export const demoInsights: InsightsClient = {
  async analytics(range) {
    return analyze(demoAllItems(), new Date(), range)
  },
  async activity(limit) {
    return buildActivity(demoAllItems(), demoActions(), limit)
  },
}

export function httpInsights(settings: Settings): InsightsClient {
  return {
    async analytics(range, signal) {
      return (await (await request(settings, `/api/analytics?range=${range}`, { signal })).json()) as Analytics
    },
    async activity(limit, signal) {
      const body = (await (await request(settings, `/api/activity?limit=${limit}`, { signal })).json()) as { entries: ActivityEntry[] }
      return body.entries
    },
  }
}

export function insightsClientFor(settings: Settings): InsightsClient {
  return settings.demo ? demoInsights : httpInsights(settings)
}

/** "45 s", "12 min", "3 h" or "2 d": a duration in the largest sensible unit. */
export function shortDuration(seconds: number): string {
  if (seconds < 60) return `${seconds} s`
  if (seconds < 3600) return `${Math.round(seconds / 60)} min`
  if (seconds < 86_400) return `${Math.round(seconds / 3600)} h`
  return `${Math.round(seconds / 86_400)} d`
}
