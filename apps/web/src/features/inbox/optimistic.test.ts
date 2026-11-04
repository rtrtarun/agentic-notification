import { describe, expect, it } from 'vitest'
import type { InboxItem, InboxPage } from '../../lib/inbox'
import { DEFAULT_FILTERS } from '../../lib/inboxQuery'
import { applyOptimistic } from './optimistic'

const item = (id: string, over: Partial<InboxItem> = {}): InboxItem => ({
  id, source: 'github', eventType: 'push', attentionClass: 'standard', priority: 'normal', outcome: 'held',
  repeatCount: 0, createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z', read: false, ...over,
})

const counts = { all: 3, unread: 3, critical: 1, high: 0, normal: 2, low: 0, held: 3, delivered: 0, suppressed: 0, archived: 0 }
const page = (): InboxPage => ({
  items: [item('a'), item('b'), item('crit', { attentionClass: 'critical', priority: 'critical' })],
  total: 3, page: 1, pageSize: 25, counts,
})
const now = new Date('2026-01-02T00:00:00Z')

describe('applyOptimistic', () => {
  it('marks the chosen items read and leaves the rest alone', () => {
    const next = applyOptimistic(page(), ['a'], { type: 'read' }, DEFAULT_FILTERS, now)
    expect(next.items.map((i) => i.read)).toEqual([true, false, false])
  })

  it('does not mutate the page it was given, so a rollback has the original', () => {
    const before = page()
    applyOptimistic(before, ['a', 'b'], { type: 'delete' }, DEFAULT_FILTERS, now)
    expect(before.items).toHaveLength(3)
    expect(before.total).toBe(3)
  })

  it('removes archived and deleted items from the inbox view and lowers the total', () => {
    const next = applyOptimistic(page(), ['a', 'b'], { type: 'archive' }, DEFAULT_FILTERS, now)
    expect(next.items.map((i) => i.id)).toEqual(['crit'])
    expect(next.total).toBe(1)
    expect(applyOptimistic(page(), ['a'], { type: 'delete' }, DEFAULT_FILTERS, now).items).toHaveLength(2)
  })

  it('keeps archived items in the archive view and removes unarchived ones', () => {
    const archive = { ...DEFAULT_FILTERS, archived: true }
    expect(applyOptimistic(page(), ['a'], { type: 'archive' }, archive, now).items).toHaveLength(3)
    expect(applyOptimistic(page(), ['a'], { type: 'unarchive' }, archive, now).items).toHaveLength(2)
  })

  it('drops an item from the unread view once it is read', () => {
    const unread = { ...DEFAULT_FILTERS, unread: true }
    expect(applyOptimistic(page(), ['a'], { type: 'read' }, unread, now).items.map((i) => i.id)).toEqual(['b', 'crit'])
    expect(applyOptimistic(page(), ['a'], { type: 'unread' }, unread, now).items).toHaveLength(3)
  })

  it('acknowledging reads the item and stamps it', () => {
    const [a] = applyOptimistic(page(), ['a'], { type: 'acknowledge' }, DEFAULT_FILTERS, now).items
    expect(a.read).toBe(true)
    expect(a.acknowledgedAt).toBe(now.toISOString())
  })

  it('snoozes for the chosen minutes', () => {
    const [a] = applyOptimistic(page(), ['a'], { type: 'snooze', minutes: 60 }, DEFAULT_FILTERS, now).items
    expect(a.snoozedUntil).toBe('2026-01-02T01:00:00.000Z')
  })

  it('never changes the priority of a critical event', () => {
    const next = applyOptimistic(page(), ['a', 'crit'], { type: 'priority', priority: 'low' }, DEFAULT_FILTERS, now)
    expect(next.items.map((i) => i.priority)).toEqual(['low', 'normal', 'critical'])
  })
})
