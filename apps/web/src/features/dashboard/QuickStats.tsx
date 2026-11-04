import { useI18n } from '../../lib/i18n/I18nContext'
import type { Counts, Level } from '../../lib/inbox'
import { LEVELS } from '../../lib/priority'

const R = 52
const C = 2 * Math.PI * R
const GAP = 3

/** Share of each level as a donut, with the total in the middle and a legend of counts and percentages. */
export function QuickStats({ counts }: { counts: Counts | undefined }) {
  const { t } = useI18n()
  const total = counts ? counts.critical + counts.high + counts.normal + counts.low : 0
  const share = (level: Level) => (total > 0 && counts ? counts[level] / total : 0)

  let offset = 0
  const arcs = LEVELS.map((level) => {
    const length = Math.max(0, share(level) * C - (share(level) > 0 ? GAP : 0))
    const arc = { level, length, offset }
    offset += share(level) * C
    return arc
  })

  return (
    <section className="rail-card" aria-labelledby="quick-title">
      <h2 className="rail-title" id="quick-title">
        {t('quick.title')}
      </h2>
      <div className="quick">
        <svg className="donut" viewBox="0 0 140 140" role="img" aria-label={t('quick.total', { n: total })}>
          <circle cx="70" cy="70" r={R} className="donut-track" />
          {arcs.map((a) => (
            <circle
              key={a.level}
              cx="70"
              cy="70"
              r={R}
              className="donut-arc"
              data-level={a.level}
              strokeDasharray={`${a.length} ${C}`}
              strokeDashoffset={-a.offset}
              transform="rotate(-90 70 70)"
            />
          ))}
          <text x="70" y="68" textAnchor="middle" className="donut-total">
            {total}
          </text>
          <text x="70" y="86" textAnchor="middle" className="donut-caption">
            {t('quick.totalLabel')}
          </text>
        </svg>
        <ul className="donut-legend">
          {LEVELS.map((level) => (
            <li key={level}>
              <span className="legend-dot" data-level={level} aria-hidden="true" />
              <span className="legend-name">{t(`level.${level}`)}</span>
              <span className="legend-value">
                {counts ? counts[level] : 0} <span className="legend-pct">({Math.round(share(level) * 100)}%)</span>
              </span>
            </li>
          ))}
        </ul>
      </div>
    </section>
  )
}
