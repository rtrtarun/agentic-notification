import { describe, expect, it } from 'vitest'
import { queryString } from './inbox'
import { DEFAULT_FILTERS, clearFilters, isFiltered, isTabActive, toQuery, toggleTab } from './inboxQuery'

describe('tabs combine', () => {
  it('starts on All', () => {
    expect(isTabActive(DEFAULT_FILTERS, 'all')).toBe(true)
    expect(isFiltered(DEFAULT_FILTERS)).toBe(false)
  })

  it('lets priority, status and unread tabs be on together', () => {
    let f = toggleTab(DEFAULT_FILTERS, 'critical')
    f = toggleTab(f, 'high')
    f = toggleTab(f, 'held')
    f = toggleTab(f, 'unread')
    expect(f.priorities).toEqual(['critical', 'high'])
    expect(f.outcomes).toEqual(['held'])
    expect(f.unread).toBe(true)
    expect(isTabActive(f, 'all')).toBe(false)
    expect(isTabActive(f, 'high')).toBe(true)
    expect(isTabActive(f, 'low')).toBe(false)
  })

  it('switches a tab off when pressed again', () => {
    const on = toggleTab(DEFAULT_FILTERS, 'low')
    expect(toggleTab(on, 'low')).toEqual(DEFAULT_FILTERS)
  })

  it('All clears every tab but keeps search, range and sort', () => {
    const busy = { ...toggleTab(toggleTab(DEFAULT_FILTERS, 'critical'), 'archived'), query: 'acme', range: '7d' as const, sort: 'oldest' as const }
    const cleared = toggleTab(busy, 'all')
    expect(cleared.priorities).toEqual([])
    expect(cleared.archived).toBe(false)
    expect(cleared.query).toBe('acme')
    expect(cleared.sort).toBe('oldest')
  })

  it('clearFilters resets everything except the sort order', () => {
    const busy = { ...toggleTab(DEFAULT_FILTERS, 'low'), query: 'x', range: '1h' as const, sort: 'priority' as const }
    expect(clearFilters(busy)).toEqual({ ...DEFAULT_FILTERS, sort: 'priority' })
  })

  it('counts a search or a range as filtering', () => {
    expect(isFiltered({ ...DEFAULT_FILTERS, query: ' a ' })).toBe(true)
    expect(isFiltered({ ...DEFAULT_FILTERS, range: '24h' })).toBe(true)
    expect(isFiltered({ ...DEFAULT_FILTERS, query: '   ' })).toBe(false)
  })
})

describe('toQuery', () => {
  const now = new Date('2026-01-10T12:00:00Z')

  it('turns a range into a from time', () => {
    expect(toQuery({ ...DEFAULT_FILTERS, range: '24h' }, now, 1).from).toBe('2026-01-09T12:00:00.000Z')
    expect(toQuery({ ...DEFAULT_FILTERS, range: '30d' }, now, 1).from).toBe('2025-12-11T12:00:00.000Z')
    expect(toQuery(DEFAULT_FILTERS, now, 1).from).toBeUndefined()
  })

  it('uses the debounced text, not the live field', () => {
    expect(toQuery({ ...DEFAULT_FILTERS, query: 'typing' }, now, 2, 'settled').text).toBe('settled')
  })
})

describe('queryString', () => {
  const now = new Date('2026-01-10T12:00:00Z')

  it('sends nothing for the default view except paging', () => {
    expect(queryString(toQuery(DEFAULT_FILTERS, now, 1))).toBe('page=1&pageSize=25')
  })

  it('sends every active filter', () => {
    const f = { ...toggleTab(toggleTab(toggleTab(DEFAULT_FILTERS, 'critical'), 'held'), 'unread'), query: ' acme ', sort: 'priority' as const }
    const p = new URLSearchParams(queryString(toQuery(f, now, 3)))
    expect(p.get('q')).toBe('acme')
    expect(p.get('priority')).toBe('critical')
    expect(p.get('outcome')).toBe('held')
    expect(p.get('read')).toBe('false')
    expect(p.get('sort')).toBe('priority')
    expect(p.get('page')).toBe('3')
  })
})
