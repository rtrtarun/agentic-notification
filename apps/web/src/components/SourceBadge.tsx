import { useI18n } from '../lib/i18n/I18nContext'
import type { AuthorSource } from '../lib/digests'

/** Says whether a digest or explanation was written by the built-in rules or by a language model. */
export function SourceBadge({ source, model }: { source: AuthorSource; model: string }) {
  const { t } = useI18n()
  return (
    <span className="chip" data-source={source}>
      {source === 'model' ? t('dg.source.model', { model }) : t('dg.source.heuristic')}
    </span>
  )
}
