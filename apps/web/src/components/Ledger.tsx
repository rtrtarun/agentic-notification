import type { Thread } from '../lib/events'
import { eventTitle, reasonText, relativeTime, statusLabel } from '../lib/events'
import { useI18n } from '../lib/i18n/I18nContext'
import { Chip } from './Chip'
import { Flag } from './Flag'

interface LedgerProps {
  threads: Thread[]
  now: Date
  selectedId: string | null
  isRead: (id: string) => boolean
  onSelect: (id: string) => void
  /** Whether a search or filter is active, which changes what an empty list says. */
  filtered: boolean
  onClear: () => void
}

/**
 * The event list. Row size follows importance: critical events are tall and set
 * in heavy type, standard events are medium, background events are slim and
 * light. A coloured chip says what kind of event it is; an orange dot says it is
 * unread. Merged events hang under the event they were merged into.
 */
export function Ledger({ threads, now, selectedId, isRead, onSelect, filtered, onClear }: LedgerProps) {
  const { t, tag } = useI18n()

  if (threads.length === 0) {
    return (
      <div className="ledger-empty">
        <p>{t('list.empty')}</p>
        {filtered && (
          <button type="button" className="button" onClick={onClear}>
            {t('list.clear')}
          </button>
        )}
      </div>
    )
  }

  return (
    <ul className="threads">
      {threads.map(({ event, merged }) => {
        const unread = !isRead(event.id)
        const reason = reasonText(event.decisionState?.reason, t)
        return (
          <li key={event.id} className="thread">
            <button
              type="button"
              className="row"
              data-class={event.attentionClass}
              data-unread={unread}
              aria-current={selectedId === event.id ? 'true' : undefined}
              onClick={() => onSelect(event.id)}
            >
              <Flag kind={event.attentionClass} />
              <span className="row-main">
                <span className="row-title">{eventTitle(event, t)}</span>
                {reason && <span className="row-reason">{reason}</span>}
              </span>
              <span className="row-meta">
                <Chip eventType={event.eventType} />
                <span className="row-status">{statusLabel(event.decisionState?.status, t)}</span>
                <time className="row-time" dateTime={event.createdAt} title={new Date(event.createdAt).toLocaleString(tag)}>
                  {relativeTime(event.createdAt, now, tag, t)}
                </time>
              </span>
              {unread && <span className="unread-dot" role="img" aria-label={t('list.unread')} />}
            </button>
            {merged.length > 0 && (
              <ul className="merged">
                {merged.map((m) => (
                  <li key={m.id}>
                    <button
                      type="button"
                      className="merged-row"
                      aria-current={selectedId === m.id ? 'true' : undefined}
                      onClick={() => onSelect(m.id)}
                    >
                      <Flag kind="merged" />
                      <span>{t('list.merged')}</span>
                      <time dateTime={m.createdAt} title={new Date(m.createdAt).toLocaleString(tag)}>
                        {relativeTime(m.createdAt, now, tag, t)}
                      </time>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </li>
        )
      })}
    </ul>
  )
}
