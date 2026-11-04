// Plain-language helpers. The service speaks in rule names and statuses; the
// interface speaks in what happened to the person.

import type { AttentionClass, EventSummary } from './api'

const EVENT_LABELS: Record<string, string> = {
  pull_request: 'Pull request',
  pull_request_review: 'Pull request review',
  push: 'Push',
  issues: 'Issue',
  issue_comment: 'Issue comment',
  security_advisory: 'Security advisory',
  dependabot_alert: 'Dependabot alert',
  workflow_run: 'Workflow run',
  check_run: 'Check run',
  release: 'Release',
  star: 'Star',
  watch: 'Watch',
  fork: 'Fork',
  ping: 'Webhook test',
}

export function eventLabel(eventType: string): string {
  const known = EVENT_LABELS[eventType]
  if (known) return known
  const spaced = eventType.replace(/_/g, ' ')
  return spaced.charAt(0).toUpperCase() + spaced.slice(1)
}

export function eventTitle(e: Pick<EventSummary, 'eventType' | 'repository'>): string {
  const label = eventLabel(e.eventType)
  return e.repository ? `${label} in ${e.repository}` : label
}

const STATUS_LABELS: Record<string, string> = {
  sent: 'Sent to you',
  queued: 'Held back',
  merged: 'Merged',
  digest: 'In a digest',
  delayed: 'Delayed',
  budget_exceeded: 'Over your limit',
}

export function statusLabel(status: string | undefined): string {
  if (!status) return 'No decision yet'
  return STATUS_LABELS[status] ?? status
}

export const CLASS_LABELS: Record<AttentionClass, string> = {
  critical: 'Critical',
  standard: 'Standard',
  background: 'Background',
}

export const CLASS_EXPLANATIONS: Record<AttentionClass, string> = {
  critical: 'Critical events always reach you. They never go through AI and are never merged.',
  standard: 'Standard events are held back instead of interrupting you. Digests are not available yet, so they stay here.',
  background: 'Background events are low priority and never interrupt you. They stay here for reference.',
}

export interface Thread {
  event: EventSummary
  merged: EventSummary[]
}

/**
 * Groups merged events under the event they were merged into. An event whose
 * original is not in the list (it may be older than the page) stays on its own.
 * Input and output are newest first.
 */
export function groupThreads(events: EventSummary[]): Thread[] {
  const byId = new Map(events.map((e) => [e.id, e]))
  const children = new Map<string, EventSummary[]>()
  const roots: EventSummary[] = []

  for (const e of events) {
    if (e.mergedInto && byId.has(e.mergedInto)) {
      const list = children.get(e.mergedInto) ?? []
      list.push(e)
      children.set(e.mergedInto, list)
    } else {
      roots.push(e)
    }
  }
  return roots.map((event) => ({ event, merged: children.get(event.id) ?? [] }))
}

export interface HourSummary {
  sent: number
  heldBack: number
  merged: number
  total: number
}

const HOUR_MS = 60 * 60 * 1000

export function summarizeLastHour(events: EventSummary[], now: Date): HourSummary {
  const summary: HourSummary = { sent: 0, heldBack: 0, merged: 0, total: 0 }
  for (const e of events) {
    if (now.getTime() - new Date(e.createdAt).getTime() > HOUR_MS) continue
    summary.total++
    switch (e.decisionState?.status) {
      case 'sent':
        summary.sent++
        break
      case 'merged':
        summary.merged++
        break
      default:
        summary.heldBack++
    }
  }
  return summary
}

export function describeHour(s: HourSummary): string {
  if (s.total === 0) return 'Nothing arrived in the last hour.'
  const parts: string[] = []
  if (s.sent) parts.push(`${s.sent} sent to you`)
  if (s.heldBack) parts.push(`${s.heldBack} held back`)
  if (s.merged) parts.push(`${s.merged} merged`)
  return `In the last hour: ${parts.join(', ')}.`
}

export function latestCritical(events: EventSummary[]): EventSummary | undefined {
  return events.find((e) => e.attentionClass === 'critical')
}

const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ['day', 86_400_000],
  ['hour', 3_600_000],
  ['minute', 60_000],
]

export function relativeTime(iso: string, now: Date): string {
  const diff = new Date(iso).getTime() - now.getTime()
  if (Math.abs(diff) < 60_000) return 'just now'
  const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'always', style: 'short' })
  for (const [unit, ms] of UNITS) {
    if (Math.abs(diff) >= ms) return rtf.format(Math.round(diff / ms), unit)
  }
  return 'just now'
}

export function pluralize(count: number, one: string, many: string): string {
  return `${count} ${count === 1 ? one : many}`
}
