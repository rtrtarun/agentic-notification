import type { EventSummary } from '../lib/api'
import { describeHour, eventTitle, latestCritical, relativeTime, summarizeLastHour } from '../lib/events'

interface LeadProps {
  events: EventSummary[]
  now: Date
  onOpen: (id: string) => void
}

/**
 * The first thing on the page: the most recent critical event, in the largest
 * type, with the reason it interrupted someone. Below it, one sentence about the
 * last hour instead of a row of counters.
 */
export function Lead({ events, now, onOpen }: LeadProps) {
  const critical = latestCritical(events)
  const hour = describeHour(summarizeLastHour(events, now))

  if (!critical) {
    return (
      <section className="lead" aria-labelledby="lead-heading">
        <h1 id="lead-heading" className="lead-title lead-title-calm">
          Nothing critical has arrived.
        </h1>
        <p className="lead-detail">{hour}</p>
      </section>
    )
  }

  const reason = critical.decisionState?.reason
  return (
    <section className="lead" aria-labelledby="lead-heading">
      <h1 id="lead-heading" className="lead-title">
        <button type="button" className="lead-link" onClick={() => onOpen(critical.id)}>
          {eventTitle(critical)}
        </button>
      </h1>
      <p className="lead-detail">
        Latest critical event, {relativeTime(critical.createdAt, now)}.
        {reason ? ` ${reason.charAt(0).toUpperCase()}${reason.slice(1)}.` : ''}
      </p>
      <p className="lead-detail">{hour}</p>
    </section>
  )
}
