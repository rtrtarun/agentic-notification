import type { EventSummary } from '../lib/api'
import { describeHour, eventTitle, latestCritical, reasonText, relativeTime, summarizeLastHour } from '../lib/events'
import { useI18n } from '../lib/i18n/I18nContext'

interface LeadProps {
  events: EventSummary[]
  now: Date
  onOpen: (id: string) => void
}

/**
 * A banner above the list: the most recent critical event with the reason it
 * interrupted someone, then one sentence about the last hour instead of a row
 * of counters.
 */
export function Lead({ events, now, onOpen }: LeadProps) {
  const { t, tag } = useI18n()
  const critical = latestCritical(events)
  const hour = describeHour(summarizeLastHour(events, now), t)

  if (!critical) {
    return (
      <section className="lead lead-calm" aria-labelledby="lead-heading">
        <h2 id="lead-heading" className="lead-title">
          {t('lead.calm')}
        </h2>
        <p className="lead-detail">{hour}</p>
      </section>
    )
  }

  const reason = reasonText(critical.decisionState?.reason, t)
  return (
    <section className="lead" aria-labelledby="lead-heading">
      <h2 id="lead-heading" className="lead-title">
        <button type="button" className="lead-link" onClick={() => onOpen(critical.id)}>
          {eventTitle(critical, t)}
        </button>
      </h2>
      <p className="lead-detail">
        {t('lead.latest', { time: relativeTime(critical.createdAt, now, tag, t) })}
        {reason ? ` ${reason}.` : ''}
      </p>
      <p className="lead-detail">{hour}</p>
    </section>
  )
}
