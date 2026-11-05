import { useCallback, useRef, useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { CommandIcon, MenuIcon, SearchIcon, UserIcon } from '../components/icons'
import { NotificationBell } from '../components/NotificationBell'
import { useToast } from '../design/Toast'
import { useDismiss } from '../hooks/useDismiss'
import { clearFilters as resetFilters, RANGES } from '../lib/inboxQuery'
import { useI18n } from '../lib/i18n/I18nContext'
import { useApp } from './AppState'

interface AppTopBarProps {
  onMenu: () => void
  menuOpen: boolean
  onPalette: () => void
}

export const SEARCH_ID = 'global-search'

/** The key that opens the palette, as shown to the user. */
export function paletteShortcut(): string {
  return typeof navigator !== 'undefined' && /mac|iphone|ipad/i.test(navigator.platform) ? '⌘ K' : 'Ctrl K'
}

/** Menu (small screens), search, time range, palette, bell and account menu. */
export function AppTopBar({ onMenu, menuOpen, onPalette }: AppTopBarProps) {
  const { t } = useI18n()
  const { filters, changeFilters, setFilters, summary, now, selectEvent, markEverythingRead, liveStatus } = useApp()
  const toast = useToast()
  const navigate = useNavigate()
  const { pathname } = useLocation()

  // Searching only makes sense on the inbox, so typing anywhere takes you there.
  const onQuery = (query: string) => {
    changeFilters({ query })
    if (pathname !== '/inbox') navigate('/inbox')
  }

  return (
    <header className="topbar">
      <button
        type="button"
        className="icon-button menu-button"
        aria-label={t('nav.openMenu')}
        aria-expanded={menuOpen}
        onClick={onMenu}
      >
        <MenuIcon />
      </button>

      <div className="search" role="search">
        <label htmlFor={SEARCH_ID} className="visually-hidden">
          {t('top.search')}
        </label>
        <SearchIcon />
        <input
          id={SEARCH_ID}
          type="search"
          className="search-input"
          placeholder={t('top.searchPlaceholder')}
          value={filters.query}
          autoComplete="off"
          onChange={(e) => onQuery(e.target.value)}
        />
      </div>

      <button type="button" className="palette-button" onClick={onPalette} aria-label={t('top.palette')} aria-keyshortcuts="Control+K Meta+K">
        <CommandIcon />
        <kbd>{paletteShortcut()}</kbd>
      </button>

      <div className="segmented range-picker" role="group" aria-label={t('range.label')}>
        {RANGES.map((r) => (
          <button key={r} type="button" className="segment" aria-pressed={filters.range === r} onClick={() => changeFilters({ range: r })}>
            {t(`range.${r}`)}
          </button>
        ))}
      </div>

      {liveStatus !== 'off' && (
        <span className="live-dot" data-live={liveStatus === 'live'} role="status">
          {t(`live.${liveStatus}`)}
        </span>
      )}

      <NotificationBell
        unread={summary.items}
        count={summary.unread}
        now={now}
        onOpenEvent={(id) => {
          selectEvent(id)
          navigate('/inbox')
        }}
        onMarkAllRead={() => {
          markEverythingRead()
            .then(() => toast.push(t('toast.allRead'), 'success'))
            .catch(() => toast.push(t('inbox.failed', { message: '' }), 'error'))
        }}
        onViewAll={() => {
          setFilters(resetFilters(filters))
          navigate('/inbox')
        }}
      />

      <AccountMenu />
    </header>
  )
}

function AccountMenu() {
  const { t } = useI18n()
  const { openSettings } = useApp()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const close = useCallback(() => setOpen(false), [])
  useDismiss(ref, open, close)

  return (
    <div className="account" ref={ref}>
      <button
        type="button"
        className="avatar-button"
        aria-label={t('top.account')}
        aria-expanded={open}
        aria-haspopup="true"
        onClick={() => setOpen((v) => !v)}
      >
        <UserIcon />
      </button>
      {open && (
        <div className="menu menu-right">
          <Link className="menu-item" to="/settings" onClick={close}>
            {t('nav.settings')}
          </Link>
          <Link className="menu-item" to="/preferences" onClick={close}>
            {t('nav.preferences')}
          </Link>
          <button
            type="button"
            className="menu-item"
            onClick={() => {
              close()
              openSettings()
            }}
          >
            {t('conn.settings')}
          </button>
          <p className="menu-note">{t('top.accountNote')}</p>
        </div>
      )}
    </div>
  )
}
