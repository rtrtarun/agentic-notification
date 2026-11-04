import { beforeEach, describe, expect, it } from 'vitest'
import { demoInbox, resetDemoInbox } from './demoInbox'
import type { InboxQuery } from './inbox'
import { levelOf, outcomeOf } from './priority'

const base: InboxQuery = { text: '', priorities: [], outcomes: [], unread: false, archived: false, sort: 'newest', page: 1, pageSize: 25 }

beforeEach(resetDemoInbox)

describe('level and outcome mapping', () => {
  it('keeps critical critical whatever else is set', () => {
    expect(levelOf({ attentionClass: 'critical' }, 'low')).toBe('critical')
  })

  it('shows a delivered standard event as high and a held one as normal', () => {
    expect(levelOf({ attentionClass: 'standard', decisionState: { status: 'sent', reason: '' } })).toBe('high')
    expect(levelOf({ attentionClass: 'standard', decisionState: { status: 'queued', reason: '' } })).toBe('normal')
    expect(levelOf({ attentionClass: 'background' })).toBe('low')
  })

  it('names the outcome of a decision', () => {
    const d = (status: string) => ({ decisionState: { status, reason: '' } })
    expect(outcomeOf(d('sent'))).toBe('delivered')
    expect(outcomeOf(d('digest'))).toBe('held')
    expect(outcomeOf(d('budget_exceeded'))).toBe('suppressed')
    expect(outcomeOf({ mergedInto: 'x' })).toBe('merged')
    expect(outcomeOf({})).toBe('recorded')
  })
})

describe('demo inbox', () => {
  it('lists newest first, hides merged repeats and counts them on the original', async () => {
    const page = await demoInbox.list(base)
    expect(page.items.some((i) => i.mergedInto)).toBe(false)
    const original = page.items.find((i) => i.id === 'demo-star-1')
    expect(original?.repeatCount).toBeGreaterThan(0)
    expect(page.items[0].createdAt >= page.items[1].createdAt).toBe(true)
  })

  it('filters by priority and unread together', async () => {
    const page = await demoInbox.list({ ...base, priorities: ['critical'], unread: true })
    expect(page.items.length).toBeGreaterThan(0)
    expect(page.items.every((i) => i.priority === 'critical' && !i.read)).toBe(true)
  })

  it('persists read, archive and delete within the session', async () => {
    await demoInbox.act('demo-advisory', { type: 'read' })
    expect((await demoInbox.get('demo-advisory')).item.read).toBe(true)

    await demoInbox.act('demo-advisory', { type: 'archive' })
    expect((await demoInbox.list(base)).items.some((i) => i.id === 'demo-advisory')).toBe(false)
    expect((await demoInbox.list({ ...base, archived: true })).items.map((i) => i.id)).toContain('demo-advisory')

    await demoInbox.act('demo-advisory', { type: 'delete' })
    await expect(demoInbox.get('demo-advisory')).rejects.toThrow('not found')
  })

  it('never lets a critical event be reprioritised', async () => {
    await expect(demoInbox.act('demo-advisory', { type: 'priority', priority: 'low' })).rejects.toThrow('critical')
    await demoInbox.act('demo-pr', { type: 'priority', priority: 'high' })
    expect((await demoInbox.get('demo-pr')).item.priority).toBe('high')
  })

  it('records user actions in the trail after the reconstructed steps', async () => {
    await demoInbox.act('demo-pr', { type: 'acknowledge' })
    const { trail } = await demoInbox.get('demo-pr')
    expect(trail[0].reconstructed).toBe(true)
    expect(trail[trail.length - 1]).toMatchObject({ step: 'acknowledge' })
    expect(trail[trail.length - 1].reconstructed).toBeUndefined()
  })

  it('bounds snooze and reports each id in a bulk request', async () => {
    await expect(demoInbox.act('demo-pr', { type: 'snooze', minutes: 0 })).rejects.toThrow('snooze')
    const results = await demoInbox.bulk(['demo-pr', 'ghost', 'demo-advisory'], { type: 'priority', priority: 'low' })
    expect(results.map((r) => r.ok)).toEqual([true, false, false])
  })

  it('marks everything read and drops the unread count to zero', async () => {
    const changed = await demoInbox.markAllRead()
    expect(changed).toBeGreaterThan(0)
    expect((await demoInbox.list(base)).counts.unread).toBe(0)
  })
})
