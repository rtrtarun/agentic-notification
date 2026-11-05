import { describe, expect, it } from 'vitest'
import { DEFAULT_ALL_FILTERS, dayAfter, dayStart, nextSort, rangeError, sortDirection, toAllQuery, toCsv } from './allQuery'
import type { InboxItem } from './inbox'

const item = (over: Partial<InboxItem> = {}): InboxItem => ({
  id: 'e1',
  source: 'github',
  eventType: 'push',
  repository: 'acme/api',
  attentionClass: 'standard',
  priority: 'normal',
  outcome: 'held',
  repeatCount: 0,
  createdAt: '2026-06-01T10:00:00Z',
  updatedAt: '2026-06-01T10:00:00Z',
  read: false,
  ...over,
})

describe('dates', () => {
  it('turns a day into local day boundaries', () => {
    const from = new Date(dayStart('2026-06-05')!)
    const to = new Date(dayAfter('2026-06-05')!)
    expect([from.getFullYear(), from.getMonth(), from.getDate(), from.getHours()]).toEqual([2026, 5, 5, 0])
    expect([to.getDate(), to.getHours()]).toEqual([6, 0])
  })
  it('rolls over month ends', () => {
    expect(new Date(dayAfter('2026-06-30')!).getMonth()).toBe(6)
  })
  it('ignores empty and malformed values', () => {
    expect(dayStart('')).toBeUndefined()
    expect(dayStart('5 June')).toBeUndefined()
    expect(dayAfter('nope')).toBeUndefined()
  })
  it('flags an end before the start', () => {
    expect(rangeError({ from: '2026-06-05', to: '2026-06-01' })).toBe(true)
    expect(rangeError({ from: '2026-06-01', to: '' })).toBe(false)
    expect(rangeError({ from: '2026-06-01', to: '2026-06-01' })).toBe(false)
  })
})

describe('toAllQuery', () => {
  it('maps the filters and trims free text fields', () => {
    const q = toAllQuery({ ...DEFAULT_ALL_FILTERS, source: ' github ', repository: '', priorities: ['critical'], pageSize: 50, from: '2026-06-01' }, 3)
    expect(q).toMatchObject({ source: 'github', repository: undefined, priorities: ['critical'], pageSize: 50, page: 3, archived: false })
    expect(q.from).toBeDefined()
    expect(q.to).toBeUndefined()
  })
  it('lets a debounced search text replace the typed one', () => {
    expect(toAllQuery({ ...DEFAULT_ALL_FILTERS, text: 'typed' }, 1, 'settled').text).toBe('settled')
  })
})

describe('sorting', () => {
  it('flips a column and starts a new one in its natural order', () => {
    expect(nextSort('newest', 'time')).toBe('oldest')
    expect(nextSort('oldest', 'time')).toBe('newest')
    expect(nextSort('newest', 'priority')).toBe('priority')
    expect(nextSort('priority', 'priority')).toBe('lowest')
  })
  it('reports the direction for assistive technology', () => {
    expect(sortDirection('newest', 'time')).toBe('descending')
    expect(sortDirection('lowest', 'priority')).toBe('ascending')
    expect(sortDirection('newest', 'priority')).toBe('none')
  })
})

describe('toCsv', () => {
  it('writes a header and one row per item', () => {
    const lines = toCsv([item(), item({ id: 'e2' })]).trim().split('\r\n')
    expect(lines).toHaveLength(3)
    expect(lines[1]).toBe('e1,2026-06-01T10:00:00Z,normal,held,github,push,acme/api,,no')
  })
  it('quotes commas, quotes and line breaks', () => {
    const csv = toCsv([item({ decisionState: { status: 'queued', reason: 'a, "b"\nc' } })])
    expect(csv).toContain('"a, ""b""\nc"')
  })
  it('defuses spreadsheet formulas', () => {
    const csv = toCsv([item({ repository: '=HYPERLINK("x")' })])
    expect(csv).toContain(`"'=HYPERLINK(""x"")"`)
  })
})
