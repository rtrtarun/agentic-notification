import { statusLabel } from '../../lib/events'
import { useI18n } from '../../lib/i18n/I18nContext'
import type { MessageKey } from '../../lib/i18n'
import type { TrailEntry } from '../../lib/inbox'

/** Plain text for the detail of a step; the service stores some details as raw ids and statuses. */
function stepDetail(entry: TrailEntry, t: ReturnType<typeof useI18n>['t']): string {
  if (!entry.detail) return ''
  if (entry.step === 'outcome') return statusLabel(entry.detail, t)
  if (entry.step === 'classified') return t(`class.${entry.detail}` as MessageKey)
  return entry.detail
}

/**
 * What happened to the event, in order. Steps worked out from stored fields are
 * marked "derived" so they are never mistaken for measured history.
 */
export function EventTimeline({ trail }: { trail: TrailEntry[] }) {
  const { t, tag } = useI18n()
  const hasDerived = trail.some((e) => e.reconstructed)
  return (
    <>
      <ol className="timeline">
        {trail.map((entry, i) => {
          const key = `trail.${entry.step}` as MessageKey
          const detail = stepDetail(entry, t)
          return (
            <li key={`${entry.step}-${i}`} className="timeline-step" data-derived={entry.reconstructed ? 'true' : undefined}>
              <span className="timeline-dot" aria-hidden="true" />
              <span className="timeline-body">
                <span className="timeline-title">
                  {t(key) === key ? entry.step : t(key)}
                  {entry.reconstructed && <span className="timeline-tag">{t('drawer.derived')}</span>}
                </span>
                {detail && <span className="timeline-detail">{detail}</span>}
                <time className="timeline-time" dateTime={entry.at}>
                  {new Date(entry.at).toLocaleString(tag)}
                </time>
              </span>
            </li>
          )
        })}
      </ol>
      {hasDerived && <p className="hint">{t('drawer.derivedNote')}</p>}
    </>
  )
}
