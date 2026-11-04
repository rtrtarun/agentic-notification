import { useI18n } from '../../lib/i18n/I18nContext'
import type { Action } from '../../lib/inbox'
import { LEVELS } from '../../lib/priority'

interface BulkBarProps {
  count: number
  archivedView: boolean
  onAction: (action: Action) => void
  onDelete: () => void
  onClear: () => void
}

const PRIORITY_CHOICES = LEVELS.filter((l) => l !== 'critical')

/** Appears while notifications are selected. Deleting asks first; the rest happen at once. */
export function BulkBar({ count, archivedView, onAction, onDelete, onClear }: BulkBarProps) {
  const { t } = useI18n()
  return (
    <div className="bulk-bar" role="toolbar" aria-label={t('bulk.label')}>
      <span className="bulk-count" role="status">
        {t('inbox.selected', { n: count })}
      </span>
      <button type="button" className="button" onClick={() => onAction({ type: 'read' })}>
        {t('act.markRead')}
      </button>
      <button type="button" className="button" onClick={() => onAction({ type: archivedView ? 'unarchive' : 'archive' })}>
        {archivedView ? t('act.unarchive') : t('act.archive')}
      </button>
      <button type="button" className="button" onClick={() => onAction({ type: 'snooze', minutes: 60 })}>
        {t('act.snooze1h')}
      </button>
      <label className="sort">
        <span className="visually-hidden">{t('act.priority')}</span>
        <select
          className="field select"
          value=""
          onChange={(e) => {
            const level = PRIORITY_CHOICES.find((l) => l === e.target.value)
            if (level) onAction({ type: 'priority', priority: level })
          }}
        >
          <option value="">{t('act.priority')}</option>
          {PRIORITY_CHOICES.map((l) => (
            <option key={l} value={l}>
              {t(`level.${l}`)}
            </option>
          ))}
        </select>
      </label>
      <button type="button" className="button button-danger" onClick={onDelete}>
        {t('act.delete')}
      </button>
      <button type="button" className="text-button" onClick={onClear}>
        {t('inbox.clearSelection')}
      </button>
    </div>
  )
}
