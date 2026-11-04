import { describe, expect, it } from 'vitest'
import type { EventSummary } from './api'
import { bucketCounts, sparklinePoints } from './sparkline'

const now = new Date('2026-01-10T12:00:00Z')
const ev = (minutesAgo: number, attentionClass: EventSummary['attentionClass'] = 'standard'): EventSummary => ({
  id: String(minutesAgo), source: 'github', eventType: 'push', attentionClass,
  createdAt: new Date(now.getTime() - minutesAgo * 60_000).toISOString(),
})

describe('bucketCounts', () => {
  it('puts events into slices, oldest first', () => {
    const counts = bucketCounts([ev(1), ev(2), ev(59)], now, '1h')
    expect(counts).toHaveLength(12)
    expect(counts[11]).toBe(2)
    expect(counts[0]).toBe(1)
    expect(counts.reduce((a, b) => a + b, 0)).toBe(3)
  })

  it('ignores events outside the range', () => {
    expect(bucketCounts([ev(120)], now, '1h').every((c) => c === 0)).toBe(true)
  })

  it('counts only the chosen level', () => {
    const events = [ev(1, 'critical'), ev(2, 'standard'), ev(3, 'background')]
    const total = (level?: 'critical' | 'normal' | 'low') => bucketCounts(events, now, '1h', level).reduce((a, b) => a + b, 0)
    expect(total()).toBe(3)
    expect(total('critical')).toBe(1)
    expect(total('low')).toBe(1)
  })

  it('spans from the oldest event for "all"', () => {
    const counts = bucketCounts([ev(300), ev(1)], now, 'all')
    expect(counts[0]).toBe(1)
    expect(counts[11]).toBe(1)
  })

  it('returns zeros for no events', () => {
    expect(bucketCounts([], now, 'all')).toEqual(new Array(12).fill(0))
  })
})

describe('sparklinePoints', () => {
  it('is empty for no values and scales the peak to the top', () => {
    expect(sparklinePoints([], 100, 30)).toBe('')
    const pts = sparklinePoints([0, 5], 100, 30).split(' ')
    expect(pts[0]).toBe('3.0,27.0')
    expect(pts[1]).toBe('97.0,3.0')
  })

  it('draws a flat line at the bottom when everything is zero', () => {
    expect(sparklinePoints([0, 0], 100, 30)).toBe('3.0,27.0 97.0,27.0')
  })
})
