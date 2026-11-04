import { BUCKETS, sparklinePoints } from '../../lib/sparkline'
import { useI18n } from '../../lib/i18n/I18nContext'
import type { Counts, Level } from '../../lib/inbox'
import { LEVELS } from '../../lib/priority'

const W = 120
const H = 36

interface StatCardsProps {
  counts: Counts | undefined
  /** Per level, events received per period, from the loaded events. */
  series: Record<Level, number[]>
  active: Level[]
  onToggle: (level: Level) => void
}

const ICONS: Record<Level, string> = {
  critical: 'M12 3 2.5 20h19ZM12 10v4m0 3h.01',
  high: 'M13 2 4 14h7l-1 8 9-12h-7Z',
  normal: 'M4 5h16v11H9l-5 4Z',
  low: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Zm0-13v4l3 2',
}

/** One card per priority level: the count, a real trend, and a click to filter the list by it. */
export function StatCards({ counts, series, active, onToggle }: StatCardsProps) {
  const { t } = useI18n()
  return (
    <ul className="stat-cards">
      {LEVELS.map((level) => (
        <li key={level}>
          <button
            type="button"
            className="stat-card"
            data-level={level}
            aria-pressed={active.includes(level)}
            aria-label={t('stat.filter', { level: t(`level.${level}`) })}
            onClick={() => onToggle(level)}
          >
            <span className="stat-icon" aria-hidden="true">
              <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d={ICONS[level]} />
              </svg>
            </span>
            <span className="stat-text">
              <span className="stat-value">{counts ? counts[level] : '–'}</span>
              <span className="stat-label">{t(`level.${level}`)}</span>
            </span>
            <svg className="stat-spark" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" aria-hidden="true" focusable="false">
              <polyline
                points={sparklinePoints(series[level] ?? new Array<number>(BUCKETS).fill(0), W, H)}
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinejoin="round"
                strokeLinecap="round"
                vectorEffect="non-scaling-stroke"
              />
            </svg>
          </button>
        </li>
      ))}
    </ul>
  )
}
