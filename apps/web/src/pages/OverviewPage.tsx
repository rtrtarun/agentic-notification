import { Link } from 'react-router-dom'
import { useApp } from '../app/AppState'
import { PriorityBadge } from '../features/inbox/badges'
import { StackedColumns, StatTile } from '../features/manage/charts'
import { LoadGate, useLoad } from '../features/manage/useLoad'
import { useInsights } from '../features/manage/useInsights'
import { eventTitle, relativeTime } from '../lib/events'
import { useI18n } from '../lib/i18n/I18nContext'
import { shortDuration } from '../lib/insights'

const OPEN_CRITICAL_SHOWN = 5

/** The first screen: what needs a person right now, and how the last day went. */
export default function OverviewPage() {
  const { t, tag } = useI18n()
  const { client, now, selectEvent } = useApp()
  const insights = useInsights()
  const analytics = useLoad((signal) => insights.analytics('24h', signal), [insights], 30_000)
  const critical = useLoad(
    (signal) =>
      client.list({ text: '', priorities: ['critical'], outcomes: [], unread: false, archived: false, sort: 'newest', page: 1, pageSize: 50 }, signal),
    [client],
    15_000,
  )

  return (
    <section className="mpage" aria-labelledby="ov-title">
      <div className="mpage-head">
        <div>
          <h1 className="page-title" id="ov-title">
            {t('nav.overview')}
          </h1>
          <p className="mpage-lede">{t('ov.lede')}</p>
        </div>
        <div className="mpage-actions">
          <Link className="button" to="/analytics">
            {t('nav.analytics')}
          </Link>
          <Link className="button" to="/activity">
            {t('nav.activity')}
          </Link>
        </div>
      </div>

      <LoadGate state={analytics.state} onRetry={analytics.reload}>
        {(a) => (
          <>
            <p className="panel" role="status">
              {a.totals.events === 0
                ? t('ov.quiet')
                : t('ov.summary', { events: a.totals.events, delivered: a.attention.interruptions, percent: Math.round(a.attention.heldBackPercent) })}
            </p>
            <div className="stat-grid">
              <StatTile label={t('ana.events')} value={a.totals.events} hint={t('ov.last24')} />
              <StatTile label={t('ana.interruptions')} value={a.attention.interruptions} hint={t('ana.interruptionsHint')} />
              <StatTile label={t('ana.heldBack')} value={`${Math.round(a.attention.heldBackPercent)}%`} hint={t('ana.heldBackHint', { n: a.attention.heldBack })} />
              <StatTile
                label={t('ana.medianRead')}
                value={a.attention.medianReadSeconds === null ? t('ana.none') : shortDuration(a.attention.medianReadSeconds)}
                hint={t('ana.medianReadHint', { n: a.attention.readCount })}
              />
            </div>
            <div className="panel">
              <h2 className="panel-title">{t('ov.trend')}</h2>
              <StackedColumns series={a.series} bucketSeconds={a.bucketSeconds} label={t('ov.trend')} />
            </div>
          </>
        )}
      </LoadGate>

      <div className="panel">
        <h2 className="panel-title">{t('ov.critical')}</h2>
        <p className="panel-note">{t('ov.criticalNote')}</p>
        <LoadGate state={critical.state} onRetry={critical.reload}>
          {(page) => {
            const open = page.items.filter((i) => !i.acknowledgedAt && !i.resolvedAt)
            if (open.length === 0) return <p>{t('ov.noCritical')}</p>
            return (
              <>
                <ul className="feed">
                  {open.slice(0, OPEN_CRITICAL_SHOWN).map((item) => (
                    <li key={item.id} className="feed-item">
                      <span className="feed-dot" data-level="critical" aria-hidden="true" />
                      <div>
                        <p className="feed-title">
                          <button type="button" className="row-link" onClick={() => selectEvent(item.id)}>
                            {eventTitle(item, t)}
                          </button>{' '}
                          <PriorityBadge level={item.priority} />
                        </p>
                        <p className="feed-meta">
                          {relativeTime(item.createdAt, now, tag, t)}
                          {item.decisionState?.reason ? ` · ${item.decisionState.reason}` : ''}
                        </p>
                      </div>
                    </li>
                  ))}
                </ul>
                {open.length > OPEN_CRITICAL_SHOWN && <p className="form-help">{t('ov.moreCritical', { n: open.length - OPEN_CRITICAL_SHOWN })}</p>}
              </>
            )
          }}
        </LoadGate>
      </div>
    </section>
  )
}
