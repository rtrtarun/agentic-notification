import type { ReactNode } from 'react'
import { useI18n } from '../../lib/i18n/I18nContext'
import type { Bucket } from '../../lib/insights'
import { LEVELS } from '../../lib/priority'
import type { Level } from '../../lib/inbox'

/** One number with a label. */
export function StatTile({ label, value, hint }: { label: string; value: ReactNode; hint?: string }) {
  return (
    <div className="stat">
      <p className="stat-label">{label}</p>
      <p className="stat-value">{value}</p>
      {hint && <p className="stat-hint">{hint}</p>}
    </div>
  )
}

export interface BarItem {
  label: string
  value: number
  level?: Level
}

/**
 * Horizontal bars. Every bar carries its number as text, so the chart never
 * depends on colour or sight to be read.
 */
export function BarList({ items, empty }: { items: BarItem[]; empty: string }) {
  if (items.length === 0) return <p className="panel-note">{empty}</p>
  const max = Math.max(...items.map((i) => i.value), 1)
  return (
    <ul className="bars">
      {items.map((item) => (
        <li key={item.label} className="bar-row">
          <span className="bar-label" title={item.label}>
            {item.label}
          </span>
          <span className="bar-track" aria-hidden="true">
            <span className="bar-fill" data-level={item.level} style={{ inlineSize: `${(item.value / max) * 100}%` }} />
          </span>
          <span className="bar-value">{item.value}</span>
        </li>
      ))}
    </ul>
  )
}

/** Stacked columns by level over time, with a text summary for screen readers. */
export function StackedColumns({ series, bucketSeconds, label }: { series: Bucket[]; bucketSeconds: number; label: string }) {
  const { t, tag } = useI18n()
  const totals = series.map((b) => b.critical + b.high + b.normal + b.low)
  const max = Math.max(...totals, 1)
  const daily = bucketSeconds >= 86_400
  const format = (iso: string) =>
    new Date(iso).toLocaleString(tag, daily ? { month: 'short', day: 'numeric' } : { hour: 'numeric' })
  return (
    <figure style={{ margin: 0 }}>
      <ul className="columns" role="img" aria-label={`${label}. ${t('ana.chartSummary', { total: totals.reduce((a, b) => a + b, 0), buckets: series.length })}`}>
        {series.map((b, i) => (
          <li key={b.start} className="column" title={`${format(b.start)}: ${totals[i]}`}>
            <span className="column-stack" style={{ blockSize: `${(totals[i] / max) * 100}%`, minBlockSize: totals[i] > 0 ? 3 : 0 }}>
              {LEVELS.map((level) => (
                <span key={level} data-level={level} style={{ blockSize: totals[i] ? `${(b[level] / totals[i]) * 100}%` : 0 }} />
              ))}
            </span>
          </li>
        ))}
      </ul>
      {series.length > 0 && (
        <div className="column-axis" aria-hidden="true">
          <span>{format(series[0].start)}</span>
          <span>{format(series[series.length - 1].start)}</span>
        </div>
      )}
      <ul className="chart-legend" aria-label={t('ana.legend')}>
        {LEVELS.map((level) => (
          <li key={level}>
            <span className="legend-swatch" data-level={level} />
            {t(`level.${level}`)}
          </li>
        ))}
      </ul>
    </figure>
  )
}
