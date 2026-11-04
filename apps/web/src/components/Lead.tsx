import type { EventSummary } from '../lib/api'
import { describeHour, eventTitle, latestCritical, reasonText, relativeTime, summarizeLastHour } from '../lib/events'
import { useI18n } from '../lib/i18n/I18nContext'
import { EventIcon } from './EventIcon'

interface LeadProps {
  events: EventSummary[]
  now: Date
  onOpen: (id: string) => void
}

/**
 * The most recent critical event, pinned above the list with the reason it
 * interrupted someone and one sentence about the last hour. Absent when there
 * is nothing critical to show.
 */
export function Lead({ events, now, onOpen }: LeadProps) {
  const { t, tag } = useI18n()
  const critical = latestCritical(events)
  if (!critical) return null
  const reason = reasonText(critical.decisionState?.reason, t)
  const hour = describeHour(summarizeLastHour(events, now), t)
  return (
    <button type="button" className="lead" onClick={() => onOpen(critical.id)}>
      <EventIcon eventType={critical.eventType} />
      <span className="lead-text">
        <span className="lead-title">{eventTitle(critical, t)}</span>
        <span className="lead-detail">
          {t('lead.latest', { time: relativeTime(critical.createdAt, now, tag, t) })}
          {reason ? ` ${reason}.` : ''} {hour}
        </span>
      </span>
      <span className="badge-pill" data-level="critical">
        {t('level.critical')}
      </span>
    </button>
  )
}
