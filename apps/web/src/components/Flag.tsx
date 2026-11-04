import type { AttentionClass } from '../lib/api'
import { useI18n } from '../lib/i18n/I18nContext'

export type FlagKind = AttentionClass | 'merged'

interface FlagProps {
  kind: FlagKind
  /** Hide from assistive technology when the label next to the flag already says it. */
  decorative?: boolean
}

/**
 * A signal flag. Colour and shape both carry the class, so it reads without
 * colour vision: a solid pennant is critical, a square flag is standard, a thin
 * outlined flag is background and a swallowtail is a merged event.
 */
export function Flag({ kind, decorative = false }: FlagProps) {
  const { t } = useI18n()
  if (decorative) return <span className="flag" data-kind={kind} aria-hidden="true" />
  return <span className="flag" data-kind={kind} role="img" aria-label={t(`class.${kind}`)} />
}
