import { EventIcon } from '../../components/EventIcon'
import type { EventSummary } from '../../lib/api'
import { eventTitle, relativeTime } from '../../lib/events'
import { useI18n } from '../../lib/i18n/I18nContext'

const SHOWN = 5

interface RecentActivityProps {
  events: EventSummary[]
  now: Date
  onOpen: (id: string) => void
  onViewAll: () => void
}

/** The newest events, whatever filters the list has. Opens the same drawer as the list. */
export function RecentActivity({ events, now, onOpen, onViewAll }: RecentActivityProps) {
  const { t, tag } = useI18n()
  const recent = events.filter((e) => !e.mergedInto).slice(0, SHOWN)
  return (
    <section className="rail-card" aria-labelledby="recent-title">
      <div className="rail-head">
        <h2 className="rail-title" id="recent-title">
          {t('recent.title')}
        </h2>
        <button type="button" className="text-button" onClick={onViewAll}>
          {t('recent.viewAll')} <span className="arrow" aria-hidden="true">→</span>
        </button>
      </div>
      {recent.length === 0 ? (
        <p className="prose">{t('recent.empty')}</p>
      ) : (
        <ul className="recent">
          {recent.map((e) => (
            <li key={e.id}>
              <button type="button" className="recent-item" onClick={() => onOpen(e.id)}>
                <EventIcon eventType={e.eventType} />
                <span className="recent-text">
                  <span className="recent-title">{eventTitle(e, t)}</span>
                  <span className="recent-time">{relativeTime(e.createdAt, now, tag, t)}</span>
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
