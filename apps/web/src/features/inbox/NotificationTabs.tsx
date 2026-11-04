import { useI18n } from '../../lib/i18n/I18nContext'
import type { Counts } from '../../lib/inbox'
import { SORTS, TABS, isFiltered, isTabActive, toggleTab, type InboxFilters, type TabId } from '../../lib/inboxQuery'

interface NotificationTabsProps {
  filters: InboxFilters
  counts: Counts | undefined
  onChange: (next: InboxFilters) => void
  onClear: () => void
}

const COUNT_KEY: Record<TabId, keyof Counts> = {
  all: 'all',
  unread: 'unread',
  critical: 'critical',
  high: 'high',
  normal: 'normal',
  low: 'low',
  held: 'held',
  delivered: 'delivered',
  suppressed: 'suppressed',
  archived: 'archived',
}

/**
 * The filter tabs. They are toggles, so priority, status and unread can be
 * combined; "All" switches the others off. Sort order sits beside them.
 */
export function NotificationTabs({ filters, counts, onChange, onClear }: NotificationTabsProps) {
  const { t } = useI18n()
  return (
    <div className="tabs-bar">
      <div className="view-tabs" role="group" aria-label={t('tabs.label')}>
        {TABS.map((tab) => (
          <button
            key={tab}
            type="button"
            className="view-tab"
            aria-pressed={isTabActive(filters, tab)}
            onClick={() => onChange(toggleTab(filters, tab))}
          >
            {t(`tab.${tab}`)}
            {counts && <span className="view-tab-count">{counts[COUNT_KEY[tab]]}</span>}
          </button>
        ))}
      </div>
      <div className="tabs-tools">
        {isFiltered(filters) && (
          <button type="button" className="text-button" onClick={onClear}>
            {t('list.clear')}
          </button>
        )}
        <label className="sort">
          <span className="sort-label">{t('sort.label')}</span>
          <select
            className="field select"
            value={filters.sort}
            onChange={(e) => onChange({ ...filters, sort: SORTS.find((s) => s === e.target.value) ?? 'newest' })}
          >
            {SORTS.map((s) => (
              <option key={s} value={s}>
                {t(`sort.${s}`)}
              </option>
            ))}
          </select>
        </label>
      </div>
    </div>
  )
}
