import { categoryOf, type Category } from '../lib/categories'

const base = {
  width: 20,
  height: 20,
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 2,
  strokeLinecap: 'round',
  strokeLinejoin: 'round',
  'aria-hidden': true,
  focusable: false,
} as const

const SHAPES: Record<string, string> = {
  security_advisory: 'M12 3 5 6v5c0 4.5 3 8 7 10 4-2 7-5.5 7-10V6Zm0 5v4m0 3h.01',
  dependabot_alert: 'M12 3 5 6v5c0 4.5 3 8 7 10 4-2 7-5.5 7-10V6Zm0 5v4m0 3h.01',
  pull_request: 'M6 3v12M6 15a3 3 0 1 0 0 6 3 3 0 0 0 0-6ZM6 3a3 3 0 1 0 0 6 3 3 0 0 0 0-6ZM18 15V9a3 3 0 0 0-3-3h-3m6 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6Z',
  pull_request_review: 'M6 3v12M6 15a3 3 0 1 0 0 6 3 3 0 0 0 0-6ZM6 3a3 3 0 1 0 0 6 3 3 0 0 0 0-6ZM18 15V9a3 3 0 0 0-3-3h-3m6 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6Z',
  push: 'm8 8-4 4 4 4m8-8 4 4-4 4m-3-11-2 14',
  issues: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Zm0-13v5m0 3h.01',
  issue_comment: 'M4 5h16v11H9l-5 4Z',
  workflow_run: 'M8 5v14l11-7Z',
  check_run: 'M8 5v14l11-7Z',
  release: 'M5 15c-1 3-1 5-1 5s2 0 5-1m-4-4c2-6 6-10 13-11 0 7-5 11-11 13Zm6-3h.01',
  star: 'm12 3 2.7 5.6 6.1.9-4.4 4.3 1 6.1L12 17l-5.4 2.9 1-6.1L3.2 9.5l6.1-.9Z',
  watch: 'M2 12s4-7 10-7 10 7 10 7-4 7-10 7S2 12 2 12Zm10 3a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z',
  fork: 'M7 4v6a4 4 0 0 0 4 4h2a4 4 0 0 0 4-4V4M12 14v6',
  merged: 'M20 12a8 8 0 1 1-2.3-5.7M20 4v5h-5',
}

const FALLBACK = 'M6 9a6 6 0 1 1 12 0c0 6 2 7.5 2 7.5H4S6 15 6 9Zm4 11a2 2 0 0 0 4 0'

/** A round, category-coloured icon for an event type. Decorative: the title next to it names the event. */
export function EventIcon({ eventType, merged = false }: { eventType: string; merged?: boolean }) {
  const category: Category = categoryOf(eventType)
  return (
    <span className="event-icon" data-category={category} aria-hidden="true">
      <svg {...base}>
        <path d={merged ? SHAPES.merged : (SHAPES[eventType] ?? FALLBACK)} />
      </svg>
    </span>
  )
}
