import { useCallback, useEffect, useRef, useState } from 'react'
import { NavLink } from 'react-router-dom'
import { ThemeToggle } from '../components/ThemeToggle'
import { ChevronIcon, CloseIcon, NavIcon } from '../components/icons'
import { useDismiss } from '../hooks/useDismiss'
import { useI18n } from '../lib/i18n/I18nContext'
import { NAV_GROUPS, NAV_ITEMS } from '../lib/nav'
import type { Feed } from '../hooks/useEvents'
import type { MessageKey } from '../lib/i18n'

type ConnectionState = 'ok' | 'wait' | 'bad'

function connection(feed: Feed, demo: boolean): { key: MessageKey; state: ConnectionState } {
  if (demo) return { key: 'conn.sample', state: 'wait' }
  if (feed.status === 'loading' && feed.events.length === 0) return { key: 'conn.connecting', state: 'wait' }
  if (feed.status === 'error') {
    return feed.error.kind === 'unreachable'
      ? { key: 'conn.notConnected', state: 'bad' }
      : { key: 'conn.notSignedIn', state: 'bad' }
  }
  return { key: 'conn.connected', state: 'ok' }
}

interface AppSidebarProps {
  feed: Feed
  demo: boolean
  unread: number
  collapsed: boolean
  onToggleCollapsed: () => void
  /** Below the desktop width the sidebar is a drawer. */
  open: boolean
  onClose: () => void
  inert?: boolean
}

/** Brand, workspace switcher, grouped navigation with badges, connection state and appearance. */
export function AppSidebar({ feed, demo, unread, collapsed, onToggleCollapsed, open, onClose, inert }: AppSidebarProps) {
  const { t } = useI18n()
  const { key, state } = connection(feed, demo)
  const closeRef = useRef<HTMLButtonElement>(null)

  // The drawer is a layer on small screens: move focus in, close on Escape.
  useEffect(() => {
    if (!open) return
    closeRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onClose])

  return (
    <>
      <aside
        className="sidebar"
        data-open={open}
        data-collapsed={collapsed}
        aria-label={t('nav.main')}
        {...(inert ? { inert: '' } : {})}
      >
        <div className="sidebar-head">
          <p className="wordmark">
            <span className="brand-mark" aria-hidden="true" />
            <span className="nav-text">{t('app.name')}</span>
          </p>
          <button type="button" className="icon-button sidebar-close" ref={closeRef} onClick={onClose} aria-label={t('nav.closeMenu')}>
            <CloseIcon />
          </button>
        </div>

        <WorkspaceSwitcher collapsed={collapsed} />

        <nav className="nav" aria-label={t('nav.main')}>
          {NAV_GROUPS.map((group) => (
            <div key={group.id} className="nav-group">
              <p className="nav-group-title nav-text">{t(group.label)}</p>
              <ul className="nav-list">
                {NAV_ITEMS.filter((item) => item.group === group.id).map((item) => {
                  const label = t(item.label)
                  return (
                    <li key={item.id}>
                      <NavLink
                        to={item.path}
                        className="nav-link"
                        data-tip={collapsed ? label : undefined}
                        aria-label={collapsed ? label : undefined}
                        onClick={onClose}
                      >
                        <NavIcon name={item.icon} />
                        <span className="nav-text">{label}</span>
                        {item.id === 'inbox' && unread > 0 && (
                          <span className="nav-badge" aria-label={`${unread}`}>
                            {unread > 99 ? '99+' : unread}
                          </span>
                        )}
                      </NavLink>
                    </li>
                  )
                })}
              </ul>
            </div>
          ))}
        </nav>

        <div className="sidebar-foot">
          <div className="foot-row">
          <p className="connection" data-state={state} role="status">
            <span className="connection-dot" aria-hidden="true" />
            <span className="nav-text">{t(key)}</span>
          </p>
          <ThemeToggle />
          </div>
          <button
            type="button"
            className="icon-button collapse-toggle"
            onClick={onToggleCollapsed}
            aria-label={collapsed ? t('nav.expand') : t('nav.collapse')}
            aria-pressed={collapsed}
          >
            <ChevronIcon direction={collapsed ? 'right' : 'left'} />
          </button>
        </div>
      </aside>
      {open && <button type="button" className="scrim" aria-label={t('nav.closeMenu')} tabIndex={-1} onClick={onClose} />}
    </>
  )
}

/** One workspace for now; the switcher says so instead of pretending there are more. */
function WorkspaceSwitcher({ collapsed }: { collapsed: boolean }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const close = useCallback(() => setOpen(false), [])
  useDismiss(ref, open, close)

  return (
    <div className="workspace-switcher" ref={ref}>
      <button
        type="button"
        className="workspace-button"
        aria-expanded={open}
        aria-haspopup="true"
        data-tip={collapsed ? t('shell.workspaceName') : undefined}
        aria-label={`${t('shell.workspace')}: ${t('shell.workspaceName')}`}
        onClick={() => setOpen((v) => !v)}
      >
        <span className="workspace-avatar" aria-hidden="true">
          P
        </span>
        <span className="nav-text workspace-name">{t('shell.workspaceName')}</span>
        <span className="nav-text">
          <ChevronIcon />
        </span>
      </button>
      {open && (
        <div className="menu" role="group" aria-label={t('shell.workspace')}>
          <p className="menu-item" aria-current="true">
            {t('shell.workspaceName')}
          </p>
          <p className="menu-note">{t('shell.workspaceNote')}</p>
        </div>
      )}
    </div>
  )
}
