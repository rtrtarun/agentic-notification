import { describe, expect, it, vi, afterEach } from 'vitest'
import { fetchEvents, sendFeedback } from './api'
import { demoEvents } from './demo'
import { groupThreads } from './events'

const now = new Date('2026-01-01T12:00:00Z')

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('demoEvents', () => {
  const events = demoEvents(now)

  it('is newest first and in the past', () => {
    const times = events.map((e) => Date.parse(e.createdAt))
    expect(times).toEqual([...times].sort((a, b) => b - a))
    expect(times.every((t) => t < now.getTime())).toBe(true)
  })

  it('covers every attention class', () => {
    expect(new Set(events.map((e) => e.attentionClass))).toEqual(new Set(['critical', 'standard', 'background']))
  })

  it('never merges or holds back a critical event', () => {
    for (const e of events.filter((e) => e.attentionClass === 'critical')) {
      expect(e.mergedInto).toBeUndefined()
      expect(e.decisionState?.status).toBe('sent')
    }
  })

  it('has unique ids and merged events that point at a real original', () => {
    const ids = events.map((e) => e.id)
    expect(new Set(ids).size).toBe(ids.length)
    for (const e of events.filter((e) => e.mergedInto)) expect(ids).toContain(e.mergedInto)
  })

  it('shows one merged thread', () => {
    const merged = groupThreads(events).filter((t) => t.merged.length > 0)
    expect(merged).toHaveLength(1)
  })
})

describe('demo mode in the API client', () => {
  it('returns sample events without calling the network', async () => {
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    const events = await fetchEvents({ backendUrl: 'http://x', apiKey: '', demo: true })
    expect(events.length).toBeGreaterThan(0)
    expect(fetchSpy).not.toHaveBeenCalled()
  })

  it('accepts feedback without calling the network', async () => {
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    await sendFeedback({ backendUrl: 'http://x', apiKey: '', demo: true }, { eventId: 'demo-pr', action: 'dismissed' })
    expect(fetchSpy).not.toHaveBeenCalled()
  })
})
