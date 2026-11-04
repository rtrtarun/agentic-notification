import type { EventSummary } from '../lib/api'
import { RANGES, type Range } from '../lib/filters'
import { useI18n } from '../lib/i18n/I18nContext'
import { NotificationBell } from './NotificationBell'
import { MenuIcon, SearchIcon } from './icons'

interface TopBarProps {
  query: string
  onQuery: (query: string) => void
  range: Range
  onRange: (range: Range) => void
  unread: EventSummary[]
  now: Date
  onOpenEvent: (id: string) => void
  onMarkAllRead: () => void
  onViewAll: () => void
  onMenu: () => void
  menuOpen: boolean
}

/** Menu button (small screens), search, time range and the notification bell. */
export function TopBar(props: TopBarProps) {
  const { t } = useI18n()
  return (
    <header className="topbar">
      <button
        type="button"
        className="icon-button menu-button"
        aria-label={t('nav.openMenu')}
        aria-expanded={props.menuOpen}
        onClick={props.onMenu}
      >
        <MenuIcon />
      </button>

      <div className="search" role="search">
        <label htmlFor="search" className="visually-hidden">
          {t('top.search')}
        </label>
        <SearchIcon />
        <input
          id="search"
          type="search"
          className="search-input"
          placeholder={t('top.searchPlaceholder')}
          value={props.query}
          autoComplete="off"
          onChange={(e) => props.onQuery(e.target.value)}
        />
      </div>

      <div className="segmented" role="group" aria-label={t('range.label')}>
        {RANGES.map((r) => (
          <button
            key={r}
            type="button"
            className="segment"
            aria-pressed={props.range === r}
            onClick={() => props.onRange(r)}
          >
            {t(`range.${r}`)}
          </button>
        ))}
      </div>

      <NotificationBell
        unread={props.unread}
        now={props.now}
        onOpenEvent={props.onOpenEvent}
        onMarkAllRead={props.onMarkAllRead}
        onViewAll={props.onViewAll}
      />
    </header>
  )
}
