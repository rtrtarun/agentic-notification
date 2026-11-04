// Search, view and time-range filtering plus paging, all on the events already
// loaded. Pure functions so they are easy to test.

import type { AttentionClass, EventSummary } from './api'
import { categoryLabel, categoryOf } from './categories'
import { eventTitle, reasonText, statusLabel } from './events'
import { tEn, type Translator } from './i18n'

export type View = 'all' | AttentionClass
export type Range = '1h' | '24h' | '7d' | 'all'

export interface Filters {
  view: View
  query: string
  range: Range
}

export const DEFAULT_FILTERS: Filters = { view: 'all', query: '', range: 'all' }
export const RANGES: Range[] = ['1h', '24h', '7d', 'all']
export const PAGE_SIZE = 10

const RANGE_MS: Record<Exclude<Range, 'all'>, number> = {
  '1h': 3_600_000,
  '24h': 86_400_000,
  '7d': 7 * 86_400_000,
}

export function isFiltered(f: Filters): boolean {
  return f.view !== 'all' || f.query.trim() !== '' || f.range !== 'all'
}

function searchText(e: EventSummary, t: Translator): string {
  return [
    eventTitle(e, t),
    e.eventType,
    e.repository ?? '',
    statusLabel(e.decisionState?.status, t),
    reasonText(e.decisionState?.reason, t),
    categoryLabel(categoryOf(e.eventType), t),
  ]
    .join(' ')
    .toLowerCase()
}

/** Keeps events that match the view, the time range and every word of the search. */
export function filterEvents(events: EventSummary[], f: Filters, now: Date, t: Translator = tEn): EventSummary[] {
  const terms = f.query.toLowerCase().split(/\s+/).filter(Boolean)
  const since = f.range === 'all' ? null : now.getTime() - RANGE_MS[f.range]
  return events.filter((e) => {
    if (f.view !== 'all' && e.attentionClass !== f.view) return false
    if (since !== null && new Date(e.createdAt).getTime() < since) return false
    if (terms.length === 0) return true
    const text = searchText(e, t)
    return terms.every((term) => text.includes(term))
  })
}

export function countByView(events: EventSummary[]): Record<View, number> {
  const counts: Record<View, number> = { all: events.length, critical: 0, standard: 0, background: 0 }
  for (const e of events) counts[e.attentionClass]++
  return counts
}

export interface Page<T> {
  items: T[]
  page: number
  pages: number
}

/** Returns one page; an out-of-range page number is clamped to the nearest valid one. */
export function paginate<T>(items: T[], page: number, size = PAGE_SIZE): Page<T> {
  const pages = Math.max(1, Math.ceil(items.length / size))
  const current = Math.min(Math.max(1, Math.floor(page) || 1), pages)
  return { items: items.slice((current - 1) * size, current * size), page: current, pages }
}
