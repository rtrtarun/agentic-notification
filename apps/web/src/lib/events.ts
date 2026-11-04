// Plain-language helpers. The service speaks in rule names and statuses; the
// interface speaks in what happened to the person.

import type { AttentionClass, EventSummary } from './api'
import { en, type MessageKey } from './i18n/en'
import { tEn, type Translator } from './i18n'

function hasKey(key: string): key is MessageKey {
  return key in en
}

/** Plain name for a GitHub event type; unknown types are made readable. */
export function eventLabel(eventType: string, t: Translator = tEn): string {
  const key = `ev.${eventType}`
  if (eventType !== 'titleIn' && hasKey(key)) return t(key)
  const spaced = eventType.replace(/_/g, ' ')
  return spaced.charAt(0).toUpperCase() + spaced.slice(1)
}

export function eventTitle(e: Pick<EventSummary, 'eventType' | 'repository'>, t: Translator = tEn): string {
  const label = eventLabel(e.eventType, t)
  return e.repository ? t('ev.titleIn', { event: label, repo: e.repository }) : label
}

export function statusLabel(status: string | undefined, t: Translator = tEn): string {
  if (!status) return t('status.none')
  const key = `status.${status}`
  return hasKey(key) ? t(key) : status
}

export function classLabel(kind: AttentionClass, t: Translator = tEn): string {
  return t(`class.${kind}`)
}

export function classExplanation(kind: AttentionClass, t: Translator = tEn): string {
  return t(`class.${kind}.explain`)
}

// The service sends decision reasons in English. Known ones are translated; any
// other reason is shown as the service wrote it.
const KNOWN_REASONS: [string, MessageKey][] = [
  ['security vulnerability detected', 'reason.security'],
  ['dependency vulnerability detected', 'reason.dependency'],
  ['low-priority social signal', 'reason.social'],
  ['standard events are held back instead of interrupting', 'reason.standard'],
]

export function reasonText(reason: string | undefined, t: Translator = tEn): string {
  if (!reason) return ''
  const trimmed = reason.trim()
  const lower = trimmed.toLowerCase()
  for (const [english, key] of KNOWN_REASONS) {
    if (lower === english) return t(key)
  }
  const duplicate = /^duplicate of (.+)$/i.exec(trimmed)
  if (duplicate) return t('reason.duplicate', { id: duplicate[1] })
  return trimmed.charAt(0).toUpperCase() + trimmed.slice(1)
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

export function describeHour(s: HourSummary, t: Translator = tEn): string {
  if (s.total === 0) return t('hour.none')
  const parts: string[] = []
  if (s.sent) parts.push(t('hour.sent', { n: s.sent }))
  if (s.heldBack) parts.push(t('hour.held', { n: s.heldBack }))
  if (s.merged) parts.push(t('hour.merged', { n: s.merged }))
  return t('hour.prefix', { parts: parts.join(', ') })
}

export function latestCritical(events: EventSummary[]): EventSummary | undefined {
  return events.find((e) => e.attentionClass === 'critical')
}

const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ['day', 86_400_000],
  ['hour', 3_600_000],
  ['minute', 60_000],
]

export function relativeTime(iso: string, now: Date, tag = 'en', t: Translator = tEn): string {
  const diff = new Date(iso).getTime() - now.getTime()
  if (Math.abs(diff) < 60_000) return t('time.now')
  const rtf = new Intl.RelativeTimeFormat(tag, { numeric: 'always', style: 'short' })
  for (const [unit, ms] of UNITS) {
    if (Math.abs(diff) >= ms) return rtf.format(Math.round(diff / ms), unit)
  }
  return t('time.now')
}

export function pluralize(count: number, one: string, many: string): string {
  return `${count} ${count === 1 ? one : many}`
}
