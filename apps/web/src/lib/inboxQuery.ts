// The inbox's filter state and how it becomes a service query. Pure functions,
// so the rules for combining filters are easy to test.

import type { InboxQuery, Level, Outcome, SortOrder } from './inbox'

export type Range = '1h' | '24h' | '7d' | '30d' | 'all'
export const RANGES: Range[] = ['1h', '24h', '7d', '30d', 'all']

export const SORTS: SortOrder[] = ['newest', 'oldest', 'priority', 'lowest', 'updated']

export const PAGE_SIZE = 25

const RANGE_MS: Record<Exclude<Range, 'all'>, number> = {
  '1h': 3_600_000,
  '24h': 86_400_000,
  '7d': 7 * 86_400_000,
  '30d': 30 * 86_400_000,
}

/** The tabs above the list. Priority and status tabs combine; "all" clears them. */
export type TabId = 'all' | 'unread' | Level | 'held' | 'delivered' | 'suppressed' | 'archived'

export const TABS: TabId[] = ['all', 'unread', 'critical', 'high', 'normal', 'low', 'held', 'delivered', 'suppressed', 'archived']

const LEVEL_TABS: Level[] = ['critical', 'high', 'normal', 'low']
const OUTCOME_TABS: Outcome[] = ['held', 'delivered', 'suppressed']

export interface InboxFilters {
  query: string
  range: Range
  priorities: Level[]
  outcomes: Outcome[]
  unread: boolean
  archived: boolean
  sort: SortOrder
}

export const DEFAULT_FILTERS: InboxFilters = {
  query: '',
  range: 'all',
  priorities: [],
  outcomes: [],
  unread: false,
  archived: false,
  sort: 'newest',
}

function toggle<T>(list: T[], value: T): T[] {
  return list.includes(value) ? list.filter((v) => v !== value) : [...list, value]
}

/** Whether a tab is switched on. "All" is on when no other tab is. */
export function isTabActive(f: InboxFilters, tab: TabId): boolean {
  if (tab === 'all') return !f.unread && !f.archived && f.priorities.length === 0 && f.outcomes.length === 0
  if (tab === 'unread') return f.unread
  if (tab === 'archived') return f.archived
  if ((LEVEL_TABS as string[]).includes(tab)) return f.priorities.includes(tab as Level)
  return f.outcomes.includes(tab as Outcome)
}

/** Switches a tab on or off. Several tabs can be on at once; "all" turns the rest off. */
export function toggleTab(f: InboxFilters, tab: TabId): InboxFilters {
  if (tab === 'all') return { ...f, unread: false, archived: false, priorities: [], outcomes: [] }
  if (tab === 'unread') return { ...f, unread: !f.unread }
  if (tab === 'archived') return { ...f, archived: !f.archived }
  if ((LEVEL_TABS as string[]).includes(tab)) return { ...f, priorities: toggle(f.priorities, tab as Level) }
  if ((OUTCOME_TABS as string[]).includes(tab)) return { ...f, outcomes: toggle(f.outcomes, tab as Outcome) }
  return f
}

/** Whether anything narrows the list, which changes what an empty list says. */
export function isFiltered(f: InboxFilters): boolean {
  return f.query.trim() !== '' || f.range !== 'all' || !isTabActive(f, 'all')
}

/** Clears search, range and tabs but keeps the chosen sort order. */
export function clearFilters(f: InboxFilters): InboxFilters {
  return { ...DEFAULT_FILTERS, sort: f.sort }
}

export function toQuery(f: InboxFilters, now: Date, page: number, text = f.query): InboxQuery {
  return {
    text,
    priorities: f.priorities,
    outcomes: f.outcomes,
    unread: f.unread,
    archived: f.archived,
    sort: f.sort,
    from: f.range === 'all' ? undefined : new Date(now.getTime() - RANGE_MS[f.range]).toISOString(),
    page,
    pageSize: PAGE_SIZE,
  }
}
