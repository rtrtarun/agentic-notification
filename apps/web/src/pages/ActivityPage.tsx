import { useState } from 'react'
import { useApp } from '../app/AppState'
import { LoadGate, useLoad } from '../features/manage/useLoad'
import { useInsights } from '../features/manage/useInsights'
import { eventTitle, relativeTime } from '../lib/events'
import type { MessageKey } from '../lib/i18n'
import { useI18n } from '../lib/i18n/I18nContext'
import type { ActivityEntry } from '../lib/insights'

type Filter = 'all' | 'received' | 'action'
const STEP = 50
const MAX = 200

/** What arrived and what people did about it, newest first. */
export default function ActivityPage() {
  const { t, tag } = useI18n()
  const { now, selectEvent } = useApp()
  const insights = useInsights()
  const [limit, setLimit] = useState(50)
  const [filter, setFilter] = useState<Filter>('all')
  const { state, reload } = useLoad((signal) => insights.activity(limit, signal), [insights, limit], 15_000)

  const describe = (e: ActivityEntry): string => {
    if (e.kind === 'received') return t('act.page.received', { outcome: e.outcome ? t(`outcome.${e.outcome}`) : t('status.none') })
    const key = `trail.${e.step}` as MessageKey
    return t(key) === key ? (e.step ?? '') : t(key)
  }

  return (
    <section className="mpage" aria-labelledby="act-title">
      <div className="mpage-head">
        <div>
          <h1 className="page-title" id="act-title">
            {t('nav.activity')}
          </h1>
          <p className="mpage-lede">{t('act.page.lede')}</p>
        </div>
        <fieldset className="chip-group">
          <legend className="sr-only">{t('act.page.filter')}</legend>
          {(['all', 'received', 'action'] as const).map((f) => (
            <button key={f} type="button" className="chip-toggle" aria-pressed={filter === f} onClick={() => setFilter(f)}>
              {t(`act.page.filter.${f}`)}
            </button>
          ))}
        </fieldset>
      </div>

      <LoadGate state={state} onRetry={reload}>
        {(entries) => {
          const shown = entries.filter((e) => filter === 'all' || e.kind === filter)
          return (
            <div className="panel">
              {shown.length === 0 ? (
                <p className="panel-note">{t('act.page.empty')}</p>
              ) : (
                <ol className="feed" aria-label={t('nav.activity')}>
                  {shown.map((e, i) => (
                    <li key={`${e.eventId}-${e.kind}-${e.at}-${i}`} className="feed-item">
                      <span className="feed-dot" data-level={e.priority} aria-hidden="true" />
                      <div>
                        <p className="feed-title">
                          {describe(e)}
                          {' · '}
                          <button type="button" className="row-link" onClick={() => selectEvent(e.eventId)}>
                            {eventTitle(e, t)}
                          </button>
                        </p>
                        <p className="feed-meta">
                          <time dateTime={e.at} title={new Date(e.at).toLocaleString(tag)}>
                            {relativeTime(e.at, now, tag, t)}
                          </time>
                          {' · '}
                          {t(`level.${e.priority}`)}
                          {e.kind === 'received' && e.reason ? ` · ${e.reason}` : ''}
                          {e.kind === 'action' && e.detail ? ` · ${e.detail}` : ''}
                        </p>
                      </div>
                    </li>
                  ))}
                </ol>
              )}
              {entries.length >= limit && limit < MAX && (
                <div className="form-actions">
                  <button type="button" className="button" onClick={() => setLimit(Math.min(MAX, limit + STEP))}>
                    {t('act.page.more')}
                  </button>
                </div>
              )}
            </div>
          )
        }}
      </LoadGate>
    </section>
  )
}
