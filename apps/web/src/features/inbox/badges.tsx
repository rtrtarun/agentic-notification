import { useI18n } from '../../lib/i18n/I18nContext'
import type { Level, Outcome } from '../../lib/inbox'

/** The four levels. Colour and the word both carry the level. */
export function PriorityBadge({ level }: { level: Level }) {
  const { t } = useI18n()
  return (
    <span className="badge-pill" data-level={level}>
      {t(`level.${level}`)}
    </span>
  )
}

/** What the pipeline did with the event. */
export function StatusBadge({ outcome }: { outcome: Outcome }) {
  const { t } = useI18n()
  return (
    <span className="badge-pill badge-quiet" data-outcome={outcome}>
      {t(`outcome.${outcome}`)}
    </span>
  )
}

const SOURCE_NAMES: Record<string, string> = { github: 'GitHub', gitlab: 'GitLab', bitbucket: 'Bitbucket' }

/** Where the event came from. */
export function SourceBadge({ source }: { source: string }) {
  const name = SOURCE_NAMES[source.toLowerCase()] ?? source
  return (
    <span className="source-badge">
      <span className="source-mark" aria-hidden="true">
        {name.charAt(0).toUpperCase()}
      </span>
      {name}
    </span>
  )
}
