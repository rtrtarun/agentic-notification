import { describe, expect, it } from 'vitest'
import type { EventSummary } from './api'
import {
  describeHour,
  eventLabel,
  eventTitle,
  groupThreads,
  latestCritical,
  pluralize,
  relativeTime,
  statusLabel,
  summarizeLastHour,
} from './events'

const NOW = new Date('2026-11-04T12:00:00Z')

function ev(id: string, over: Partial<EventSummary> = {}): EventSummary {
  return {
    id,
    source: 'github',
    eventType: 'star',
    attentionClass: 'standard',
    createdAt: '2026-11-04T11:59:00Z',
    decisionState: { status: 'queued', reason: 'default routing' },
    ...over,
  }
}

describe('eventLabel and eventTitle', () => {
  it('uses plain names for known GitHub events', () => {
    expect(eventLabel('pull_request')).toBe('Pull request')
    expect(eventLabel('dependabot_alert')).toBe('Dependabot alert')
  })

  it('makes unknown events readable', () => {
    expect(eventLabel('merge_group')).toBe('Merge group')
    expect(eventLabel('')).toBe('')
  })

  it('adds the repository when there is one', () => {
    expect(eventTitle({ eventType: 'push', repository: 'octo/api' })).toBe('Push in octo/api')
    expect(eventTitle({ eventType: 'push' })).toBe('Push')
  })
})

describe('statusLabel', () => {
  it('translates statuses into what happened to the person', () => {
    expect(statusLabel('sent')).toBe('Sent to you')
    expect(statusLabel('queued')).toBe('Held back')
    expect(statusLabel('merged')).toBe('Merged')
  })

  it('handles missing and unknown statuses', () => {
    expect(statusLabel(undefined)).toBe('No decision yet')
    expect(statusLabel('something_new')).toBe('something_new')
  })
})

describe('groupThreads', () => {
  it('nests merged events under their original', () => {
    const events = [
      ev('c', { mergedInto: 'a' }),
      ev('b', { mergedInto: 'a' }),
      ev('x'),
      ev('a'),
    ]
    const threads = groupThreads(events)
    expect(threads.map((t) => t.event.id)).toEqual(['x', 'a'])
    expect(threads[1].merged.map((m) => m.id)).toEqual(['c', 'b'])
    expect(threads[0].merged).toEqual([])
  })

  it('keeps an event on its own when its original is not loaded', () => {
    const threads = groupThreads([ev('c', { mergedInto: 'older-than-the-page' })])
    expect(threads).toHaveLength(1)
    expect(threads[0].event.id).toBe('c')
  })

  it('handles an empty list', () => {
    expect(groupThreads([])).toEqual([])
  })
})

describe('summarizeLastHour and describeHour', () => {
  const events = [
    ev('1', { decisionState: { status: 'sent', reason: '' }, attentionClass: 'critical' }),
    ev('2', { decisionState: { status: 'queued', reason: '' } }),
    ev('3', { decisionState: { status: 'queued', reason: '' } }),
    ev('4', { decisionState: { status: 'merged', reason: '' } }),
    ev('5', { decisionState: undefined }),
    ev('old', { createdAt: '2026-11-04T10:59:59Z', decisionState: { status: 'sent', reason: '' } }),
  ]

  it('counts only the last hour and groups statuses', () => {
    expect(summarizeLastHour(events, NOW)).toEqual({ sent: 1, heldBack: 3, merged: 1, total: 5 })
  })

  it('includes an event exactly one hour old', () => {
    const edge = ev('edge', { createdAt: '2026-11-04T11:00:00Z' })
    expect(summarizeLastHour([edge], NOW).total).toBe(1)
  })

  it('describes the hour in a sentence', () => {
    expect(describeHour(summarizeLastHour(events, NOW))).toBe('In the last hour: 1 sent to you, 3 held back, 1 merged.')
  })

  it('says so when nothing arrived', () => {
    expect(describeHour(summarizeLastHour([], NOW))).toBe('Nothing arrived in the last hour.')
  })

  it('leaves out zero counts', () => {
    expect(describeHour({ sent: 2, heldBack: 0, merged: 0, total: 2 })).toBe('In the last hour: 2 sent to you.')
  })
})

describe('latestCritical', () => {
  it('returns the first critical event in a newest-first list', () => {
    const events = [ev('a'), ev('b', { attentionClass: 'critical' }), ev('c', { attentionClass: 'critical' })]
    expect(latestCritical(events)?.id).toBe('b')
  })

  it('returns undefined when there is none', () => {
    expect(latestCritical([ev('a')])).toBeUndefined()
  })
})

describe('relativeTime', () => {
  it('says just now inside a minute', () => {
    expect(relativeTime('2026-11-04T11:59:30Z', NOW)).toBe('just now')
  })

  it('uses minutes, hours and days', () => {
    expect(relativeTime('2026-11-04T11:55:00Z', NOW)).toMatch(/5 min/)
    expect(relativeTime('2026-11-04T09:00:00Z', NOW)).toMatch(/3 hr/)
    expect(relativeTime('2026-11-02T12:00:00Z', NOW)).toMatch(/2 days/)
  })
})

describe('pluralize', () => {
  it('chooses the singular for one', () => {
    expect(pluralize(1, 'event', 'events')).toBe('1 event')
    expect(pluralize(0, 'event', 'events')).toBe('0 events')
    expect(pluralize(2, 'event', 'events')).toBe('2 events')
  })
})
