// The All Notifications table: its own filter state (separate from the inbox
// tabs), the service query it becomes, and the CSV export. Pure functions.

import type { InboxItem, InboxQuery, Level, Outcome, SortOrder } from './inbox'

export const ALL_PAGE_SIZES = [25, 50, 100] as const

export interface AllFilters {
  text: string
  priorities: Level[]
  outcomes: Outcome[]
  source: string
  repository: string
  /** yyyy-mm-dd from a date input, local time. */
  from: string
  to: string
  sort: SortOrder
  pageSize: number
  includeArchived: boolean
}

export const DEFAULT_ALL_FILTERS: AllFilters = {
  text: '',
  priorities: [],
  outcomes: [],
  source: '',
  repository: '',
  from: '',
  to: '',
  sort: 'newest',
  pageSize: 25,
  includeArchived: false,
}

const DAY = /^\d{4}-\d{2}-\d{2}$/

function localDay(value: string, offsetDays: number): string | undefined {
  if (!DAY.test(value)) return undefined
  const [y, m, d] = value.split('-').map(Number)
  const date = new Date(y, m - 1, d + offsetDays)
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString()
}

/** Start of the given local day as an ISO time, or undefined for an empty or invalid value. */
export function dayStart(value: string): string | undefined {
  return localDay(value, 0)
}

/** The service treats `to` as exclusive, so "to 5 June" means before the start of 6 June. */
export function dayAfter(value: string): string | undefined {
  return localDay(value, 1)
}

/** Whether the range is invalid: an empty end is fine, an end before the start is not. */
export function rangeError(f: Pick<AllFilters, 'from' | 'to'>): boolean {
  return f.from !== '' && f.to !== '' && f.to < f.from
}

export function toAllQuery(f: AllFilters, page: number, text = f.text): InboxQuery {
  return {
    text,
    priorities: f.priorities,
    outcomes: f.outcomes,
    unread: false,
    archived: f.includeArchived,
    sort: f.sort,
    source: f.source.trim() || undefined,
    repository: f.repository.trim() || undefined,
    from: dayStart(f.from),
    to: dayAfter(f.to),
    page,
    pageSize: f.pageSize,
  }
}

/** Clicking a column header: the same column flips direction, a new one starts with its natural order. */
export function nextSort(current: SortOrder, column: 'time' | 'priority'): SortOrder {
  if (column === 'time') return current === 'newest' ? 'oldest' : 'newest'
  return current === 'priority' ? 'lowest' : 'priority'
}

export function sortDirection(current: SortOrder, column: 'time' | 'priority'): 'ascending' | 'descending' | 'none' {
  if (column === 'time') return current === 'newest' ? 'descending' : current === 'oldest' ? 'ascending' : 'none'
  return current === 'priority' ? 'descending' : current === 'lowest' ? 'ascending' : 'none'
}

/** A cell that starts with = + - or @ would run as a formula in a spreadsheet, so it is prefixed. */
function csvCell(value: string): string {
  const safe = /^[=+\-@\t\r]/.test(value) ? `'${value}` : value
  return /[",\r\n]/.test(safe) ? `"${safe.replace(/"/g, '""')}"` : safe
}

export function toCsv(items: InboxItem[]): string {
  const header = ['id', 'received', 'priority', 'outcome', 'source', 'event', 'repository', 'reason', 'read']
  const rows = items.map((i) =>
    [
      i.id,
      i.createdAt,
      i.priority,
      i.outcome,
      i.source,
      i.eventType,
      i.repository ?? '',
      i.decisionState?.reason ?? '',
      i.read ? 'yes' : 'no',
    ].map(csvCell),
  )
  return [header, ...rows].map((r) => r.join(',')).join('\r\n') + '\r\n'
}
