import type { AttentionClass } from '../lib/api'
import { useI18n } from '../lib/i18n/I18nContext'
import { Flag } from './Flag'

const ORDER: AttentionClass[] = ['critical', 'standard', 'background']

/** The three classes in plain words, so the shapes in the list are never a puzzle. */
export function Legend() {
  const { t } = useI18n()
  return (
    <ul className="legend" aria-label={t('legend.label')}>
      {ORDER.map((kind) => (
        <li key={kind} className="legend-item">
          <Flag kind={kind} decorative />
          <span>
            <strong>{t(`class.${kind}`)}.</strong> {t(`legend.${kind}`)}
          </span>
        </li>
      ))}
    </ul>
  )
}
