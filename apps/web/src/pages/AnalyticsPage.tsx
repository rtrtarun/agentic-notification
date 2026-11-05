import { useState } from 'react'
import { BarList, StackedColumns, StatTile } from '../features/manage/charts'
import { LoadGate, useLoad } from '../features/manage/useLoad'
import { useInsights } from '../features/manage/useInsights'
import { eventLabel } from '../lib/events'
import { useI18n } from '../lib/i18n/I18nContext'
import { ANALYTICS_RANGES, shortDuration, type AnalyticsRange } from '../lib/insights'
import { LEVELS } from '../lib/priority'

/** How much attention the platform protected, and where the notifications came from. */
export default function AnalyticsPage() {
  const { t } = useI18n()
  const insights = useInsights()
  const [range, setRange] = useState<AnalyticsRange>('7d')
  const { state, reload } = useLoad((signal) => insights.analytics(range, signal), [insights, range], 30_000)

  return (
    <section className="mpage" aria-labelledby="ana-title">
      <div className="mpage-head">
        <div>
          <h1 className="page-title" id="ana-title">
            {t('nav.analytics')}
          </h1>
          <p className="mpage-lede">{t('ana.lede')}</p>
        </div>
        <fieldset className="chip-group">
          <legend className="sr-only">{t('ana.range')}</legend>
          {ANALYTICS_RANGES.map((r) => (
            <button key={r} type="button" className="chip-toggle" aria-pressed={range === r} onClick={() => setRange(r)}>
              {t(`ana.range.${r}`)}
            </button>
          ))}
        </fieldset>
      </div>

      <LoadGate state={state} onRetry={reload}>
        {(a) => (
          <>
            <div className="stat-grid">
              <StatTile label={t('ana.events')} value={a.totals.events} />
              <StatTile label={t('ana.interruptions')} value={a.attention.interruptions} hint={t('ana.interruptionsHint')} />
              <StatTile label={t('ana.heldBack')} value={`${Math.round(a.attention.heldBackPercent)}%`} hint={t('ana.heldBackHint', { n: a.attention.heldBack })} />
              <StatTile label={t('ana.openCritical')} value={a.attention.openCritical} hint={t('ana.openCriticalHint')} />
              <StatTile
                label={t('ana.medianRead')}
                value={a.attention.medianReadSeconds === null ? t('ana.none') : shortDuration(a.attention.medianReadSeconds)}
                hint={t('ana.medianReadHint', { n: a.attention.readCount })}
              />
              <StatTile label={t('ana.unread')} value={a.totals.unread} />
            </div>

            <div className="panel">
              <h2 className="panel-title">{t('ana.overTime')}</h2>
              <StackedColumns series={a.series} bucketSeconds={a.bucketSeconds} label={t('ana.overTime')} />
            </div>

            <div className="panel-grid">
              <div className="panel">
                <h2 className="panel-title">{t('ana.byPriority')}</h2>
                <BarList
                  empty={t('ana.empty')}
                  items={LEVELS.map((level) => ({ label: t(`level.${level}`), value: a.totals[level], level })).filter((i) => i.value > 0)}
                />
              </div>
              <div className="panel">
                <h2 className="panel-title">{t('ana.byOutcome')}</h2>
                <BarList
                  empty={t('ana.empty')}
                  items={(['delivered', 'held', 'merged', 'suppressed', 'recorded'] as const)
                    .map((o) => ({ label: t(`outcome.${o}`), value: a.totals[o] }))
                    .filter((i) => i.value > 0)}
                />
              </div>
              <div className="panel">
                <h2 className="panel-title">{t('ana.topSources')}</h2>
                <BarList empty={t('ana.empty')} items={a.topSources.map((s) => ({ label: s.name, value: s.count }))} />
              </div>
              <div className="panel">
                <h2 className="panel-title">{t('ana.topRepos')}</h2>
                <BarList empty={t('ana.empty')} items={a.topRepositories.map((s) => ({ label: s.name, value: s.count }))} />
              </div>
              <div className="panel">
                <h2 className="panel-title">{t('ana.topEvents')}</h2>
                <BarList empty={t('ana.empty')} items={a.topEvents.map((s) => ({ label: eventLabel(s.name, t), value: s.count }))} />
              </div>
              <div className="panel">
                <h2 className="panel-title">{t('ana.response')}</h2>
                <dl className="kv">
                  <dt>{t('ana.acknowledged')}</dt>
                  <dd>{a.attention.acknowledged}</dd>
                  <dt>{t('ana.resolved')}</dt>
                  <dd>{a.attention.resolved}</dd>
                  <dt>{t('ana.merged')}</dt>
                  <dd>{a.totals.merged}</dd>
                </dl>
                <p className="form-help">{t('ana.limit')}</p>
              </div>
            </div>
          </>
        )}
      </LoadGate>
    </section>
  )
}
