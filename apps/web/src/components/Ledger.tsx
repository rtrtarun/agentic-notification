import { useMemo, useState } from 'react'
import type { AttentionClass, EventSummary } from '../lib/api'
import { CLASS_LABELS, eventTitle, groupThreads, relativeTime, statusLabel } from '../lib/events'
import { Flag } from './Flag'
import { Legend } from './Legend'

type Filter = 'all' | AttentionClass

const FILTERS: { value: Filter; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'critical', label: CLASS_LABELS.critical },
  { value: 'standard', label: CLASS_LABELS.standard },
  { value: 'background', label: CLASS_LABELS.background },
]

interface LedgerProps {
  events: EventSummary[]
  now: Date
  selectedId: string | null
  onSelect: (id: string) => void
}

/**
 * The event list. Row size follows importance: critical events are tall and set
 * in heavy type, standard events are medium, background events are slim and
 * light. Merged events hang under the event they were merged into.
 */
export function Ledger({ events, now, selectedId, onSelect }: LedgerProps) {
  const [filter, setFilter] = useState<Filter>('all')

  const counts = useMemo(() => {
    const c: Record<Filter, number> = { all: events.length, critical: 0, standard: 0, background: 0 }
    for (const e of events) c[e.attentionClass]++
    return c
  }, [events])

  const threads = useMemo(() => groupThreads(events), [events])
  const visible = filter === 'all' ? threads : threads.filter((t) => t.event.attentionClass === filter)

  return (
    <section className="ledger" aria-label="Events">
      <p className="ledger-help">Select an event to see why it was handled this way.</p>
      <Legend />
      <div className="filters" role="group" aria-label="Show events">
        {FILTERS.map((f) => (
          <button
            key={f.value}
            type="button"
            className="filter"
            aria-pressed={filter === f.value}
            onClick={() => setFilter(f.value)}
          >
            {f.label} <span className="filter-count">{counts[f.value]}</span>
          </button>
        ))}
      </div>

      {visible.length === 0 ? (
        <p className="ledger-empty">
          No {filter === 'all' ? '' : CLASS_LABELS[filter as AttentionClass].toLowerCase() + ' '}events in the latest
          page.
        </p>
      ) : (
        <ul className="threads">
          {visible.map(({ event, merged }) => (
            <li key={event.id} className="thread">
              <button
                type="button"
                className="row"
                data-class={event.attentionClass}
                aria-current={selectedId === event.id ? 'true' : undefined}
                onClick={() => onSelect(event.id)}
              >
                <Flag kind={event.attentionClass} />
                <span className="row-title">{eventTitle(event)}</span>
                <span className="row-status">{statusLabel(event.decisionState?.status)}</span>
                <time className="row-time" dateTime={event.createdAt} title={new Date(event.createdAt).toLocaleString()}>
                  {relativeTime(event.createdAt, now)}
                </time>
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
                        <span>Repeat, merged into the event above</span>
                        <time dateTime={m.createdAt} title={new Date(m.createdAt).toLocaleString()}>
                          {relativeTime(m.createdAt, now)}
                        </time>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
