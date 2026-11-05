import { describe, expect, it } from 'vitest'
import type { InboxItem } from './inbox'
import { analyze, buildActivity, demoInsights, shortDuration } from './insights'

const NOW = new Date('2026-06-10T12:00:00Z')
const ago = (minutes: number) => new Date(NOW.getTime() - minutes * 60_000).toISOString()

const item = (id: string, minutes: number, priority: InboxItem['priority'], outcome: InboxItem['outcome'], over: Partial<InboxItem> = {}): InboxItem => ({
  id,
  source: 'github',
  eventType: 'push',
  repository: 'acme/api',
  attentionClass: 'standard',
  priority,
  outcome,
  repeatCount: 0,
  createdAt: ago(minutes),
  updatedAt: ago(minutes),
  read: false,
  ...over,
})

describe('analyze', () => {
  const items = [
    item('a', 30, 'critical', 'delivered'),
    item('b', 90, 'normal', 'held'),
    item('c', 90, 'low', 'merged'),
    item('d', 300, 'high', 'delivered'),
    item('old', 48 * 60, 'normal', 'held'),
    item('e', 600, 'normal', 'suppressed'),
  ]

  it('counts levels and outcomes inside the range only', () => {
    const a = analyze(items, NOW, '24h')
    expect(a.totals).toMatchObject({ events: 5, critical: 1, high: 1, normal: 2, low: 1, delivered: 2, held: 1, merged: 1, suppressed: 1 })
  })

  it('matches the service on held-back figures', () => {
    const a = analyze(items, NOW, '24h')
    expect(a.attention).toMatchObject({ interruptions: 2, heldBack: 3, heldBackPercent: 60 })
  })

  it('builds hourly buckets that add up to the total', () => {
    const a = analyze(items, NOW, '24h')
    expect(a.series).toHaveLength(24)
    expect(a.bucketSeconds).toBe(3600)
    expect(a.series.reduce((n, b) => n + b.critical + b.high + b.normal + b.low, 0)).toBe(5)
    expect(a.series[23].critical).toBe(1)
  })

  it('uses daily buckets for 7 and 30 days', () => {
    expect(analyze(items, NOW, '7d').series).toHaveLength(7)
    expect(analyze(items, NOW, '30d').series).toHaveLength(30)
    expect(analyze(items, NOW, '30d').totals.events).toBe(6)
  })

  it('handles an empty history', () => {
    const a = analyze([], NOW, '7d')
    expect(a.totals.events).toBe(0)
    expect(a.attention.heldBackPercent).toBe(0)
    expect(a.attention.medianReadSeconds).toBeNull()
    expect(a.topSources).toEqual([])
  })

  it('finds the median read time and open critical', () => {
    const read = (id: string, seconds: number) =>
      item(id, 60, 'normal', 'held', { read: true, readAt: new Date(NOW.getTime() - 60 * 60_000 + seconds * 1000).toISOString() })
    const acknowledged = item('ack', 60, 'critical', 'delivered', { acknowledgedAt: ago(10) })
    const a = analyze([read('r1', 10), read('r2', 60), read('r3', 20), item('open', 60, 'critical', 'delivered'), acknowledged], NOW, '24h')
    expect(a.attention.medianReadSeconds).toBe(20)
    expect(a.attention.openCritical).toBe(1)
    expect(a.attention.acknowledged).toBe(1)
    expect(a.totals.unread).toBe(2)
  })

  it('orders top lists by count then name and keeps five', () => {
    const many = ['g', 'f', 'e', 'd', 'c', 'b', 'a'].flatMap((repo, i) =>
      Array.from({ length: (i % 3) + 1 }, (_, n) => item(`${repo}${n}`, 30, 'normal', 'held', { repository: repo })),
    )
    const a = analyze(many, NOW, '24h')
    expect(a.topRepositories).toHaveLength(5)
    for (let i = 1; i < 5; i++) {
      const [prev, cur] = [a.topRepositories[i - 1], a.topRepositories[i]]
      expect(prev.count > cur.count || (prev.count === cur.count && prev.name < cur.name)).toBe(true)
    }
  })
})

describe('buildActivity', () => {
  const items = [item('a', 180, 'critical', 'delivered'), item('b', 60, 'normal', 'held')]
  const actions = [
    { eventId: 'a', step: 'acknowledge', at: ago(120) },
    { eventId: 'gone', step: 'archive', at: ago(1) },
  ]

  it('merges arrivals and actions newest first and drops actions on missing notifications', () => {
    const entries = buildActivity(items, actions, 10)
    expect(entries.map((e) => `${e.eventId}:${e.kind}`)).toEqual(['b:received', 'a:action', 'a:received'])
  })

  it('applies the limit', () => {
    expect(buildActivity(items, actions, 2)).toHaveLength(2)
  })
})

describe('demo client', () => {
  it('answers analytics and activity from the sample data', async () => {
    const a = await demoInsights.analytics('30d')
    expect(a.series).toHaveLength(30)
    expect(a.totals.events).toBeGreaterThan(0)
    expect((await demoInsights.activity(5)).length).toBeLessThanOrEqual(5)
  })
})

describe('shortDuration', () => {
  it('picks the largest sensible unit', () => {
    expect(shortDuration(45)).toBe('45 s')
    expect(shortDuration(600)).toBe('10 min')
    expect(shortDuration(7200)).toBe('2 h')
    expect(shortDuration(172_800)).toBe('2 d')
  })
})
