import { afterEach, describe, expect, it, vi } from 'vitest'
import type { EventSummary } from './api'
import { categoryOf } from './categories'
import { DEFAULT_FILTERS, PAGE_SIZE, countByView, filterEvents, isFiltered, paginate } from './filters'
import { makeTranslator } from './i18n'
import { loadRead, saveRead } from './read'

const NOW = new Date('2026-11-04T12:00:00Z')

function ev(id: string, over: Partial<EventSummary> = {}): EventSummary {
  return {
    id,
    source: 'github',
    eventType: 'push',
    repository: 'acme/api',
    attentionClass: 'standard',
    createdAt: '2026-11-04T11:50:00Z',
    decisionState: { status: 'queued', reason: 'default routing' },
    ...over,
  }
}

const events = [
  ev('a', {
    eventType: 'security_advisory',
    attentionClass: 'critical',
    decisionState: { status: 'sent', reason: 'security vulnerability detected' },
  }),
  ev('b', { eventType: 'pull_request', repository: 'acme/web', createdAt: '2026-11-04T08:00:00Z' }),
  ev('c', { eventType: 'star', repository: 'acme/web', attentionClass: 'background', createdAt: '2026-10-30T12:00:00Z' }),
]

const ids = (list: EventSummary[]) => list.map((e) => e.id)

describe('filterEvents', () => {
  it('returns everything with default filters', () => {
    expect(filterEvents(events, DEFAULT_FILTERS, NOW)).toHaveLength(3)
  })

  it('filters by view', () => {
    expect(ids(filterEvents(events, { ...DEFAULT_FILTERS, view: 'critical' }, NOW))).toEqual(['a'])
  })

  it('filters by time range', () => {
    expect(ids(filterEvents(events, { ...DEFAULT_FILTERS, range: '1h' }, NOW))).toEqual(['a'])
    expect(ids(filterEvents(events, { ...DEFAULT_FILTERS, range: '24h' }, NOW))).toEqual(['a', 'b'])
    expect(filterEvents(events, { ...DEFAULT_FILTERS, range: '7d' }, NOW)).toHaveLength(3)
  })

  it('searches title, repository, status and category, ignoring case', () => {
    const q = (query: string) => ids(filterEvents(events, { ...DEFAULT_FILTERS, query }, NOW))
    expect(q('ACME/WEB')).toEqual(['b', 'c'])
    expect(q('advisory')).toEqual(['a'])
    expect(q('held back')).toEqual(['b', 'c'])
    expect(q('security')).toEqual(['a'])
  })

  it('needs every word to match', () => {
    expect(ids(filterEvents(events, { ...DEFAULT_FILTERS, query: 'acme web star' }, NOW))).toEqual(['c'])
    expect(filterEvents(events, { ...DEFAULT_FILTERS, query: 'acme nothing' }, NOW)).toEqual([])
  })

  it('searches in the language shown', () => {
    const es = makeTranslator('es')
    expect(ids(filterEvents(events, { ...DEFAULT_FILTERS, query: 'aviso de seguridad' }, NOW, es))).toEqual(['a'])
  })

  it('combines filters', () => {
    expect(ids(filterEvents(events, { view: 'standard', query: 'web', range: '24h' }, NOW))).toEqual(['b'])
  })
})

describe('isFiltered and countByView', () => {
  it('knows when a filter is active', () => {
    expect(isFiltered(DEFAULT_FILTERS)).toBe(false)
    expect(isFiltered({ ...DEFAULT_FILTERS, query: '  ' })).toBe(false)
    expect(isFiltered({ ...DEFAULT_FILTERS, query: 'x' })).toBe(true)
    expect(isFiltered({ ...DEFAULT_FILTERS, range: '7d' })).toBe(true)
  })

  it('counts per view', () => {
    expect(countByView(events)).toEqual({ all: 3, critical: 1, standard: 1, background: 1 })
  })
})

describe('paginate', () => {
  const items = Array.from({ length: 25 }, (_, i) => i)

  it('slices pages and reports the page count', () => {
    expect(paginate(items, 1).items).toHaveLength(PAGE_SIZE)
    expect(paginate(items, 3).items).toEqual([20, 21, 22, 23, 24])
    expect(paginate(items, 1).pages).toBe(3)
  })

  it('clamps out-of-range and invalid pages', () => {
    expect(paginate(items, 99).page).toBe(3)
    expect(paginate(items, 0).page).toBe(1)
    expect(paginate(items, Number.NaN).page).toBe(1)
  })

  it('has one empty page for no items', () => {
    expect(paginate([], 1)).toEqual({ items: [], page: 1, pages: 1 })
  })
})

describe('categoryOf', () => {
  it('groups event types and defaults to other', () => {
    expect(categoryOf('dependabot_alert')).toBe('security')
    expect(categoryOf('pull_request')).toBe('code')
    expect(categoryOf('workflow_run')).toBe('build')
    expect(categoryOf('star')).toBe('social')
    expect(categoryOf('merge_group')).toBe('other')
  })
})

describe('read state storage', () => {
  function store() {
    const data = new Map<string, string>()
    return { getItem: (k: string) => data.get(k) ?? null, setItem: (k: string, v: string) => void data.set(k, v) }
  }

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('round-trips ids', () => {
    vi.stubGlobal('localStorage', store())
    saveRead(new Set(['a', 'b']))
    expect([...loadRead()]).toEqual(['a', 'b'])
  })

  it('ignores corrupt or wrong-typed storage', () => {
    const s = store()
    vi.stubGlobal('localStorage', s)
    s.setItem('attention.read', '{oops')
    expect(loadRead().size).toBe(0)
    s.setItem('attention.read', JSON.stringify({ a: 1 }))
    expect(loadRead().size).toBe(0)
    s.setItem('attention.read', JSON.stringify(['a', 5, null, 'b']))
    expect([...loadRead()]).toEqual(['a', 'b'])
  })

  it('keeps only the newest ids when the list grows too long', () => {
    vi.stubGlobal('localStorage', store())
    saveRead(new Set(Array.from({ length: 2500 }, (_, i) => `id${i}`)))
    const loaded = loadRead()
    expect(loaded.size).toBe(2000)
    expect(loaded.has('id2499')).toBe(true)
    expect(loaded.has('id0')).toBe(false)
  })
})
