import { useEffect, useRef } from 'react'
import type { Feed } from '../hooks/useEvents'
import { useI18n } from '../lib/i18n/I18nContext'
import type { MessageKey } from '../lib/i18n'
import type { View } from '../lib/filters'
import { Appearance } from './Appearance'
import { Flag } from './Flag'
import { CloseIcon } from './icons'

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

const VIEWS: View[] = ['all', 'critical', 'standard', 'background']

interface SidebarProps {
  feed: Feed
  demo: boolean
  counts: Record<View, number>
  view: View
  onView: (view: View) => void
  onOpenSettings: () => void
  /** Small screens show the sidebar as a drawer. */
  open: boolean
  onClose: () => void
  /** True while the detail sheet covers the page. */
  inert?: boolean
}

/** Views (all, critical, standard, background) with counts, connection, language and theme. */
export function Sidebar({ feed, demo, counts, view, onView, onOpenSettings, open, onClose, inert }: SidebarProps) {
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
      <aside className="sidebar" data-open={open} aria-label={t('nav.main')} {...(inert ? { inert: '' } : {})}>
        <div className="sidebar-head">
          <p className="wordmark">{t('app.name')}</p>
          <button type="button" className="icon-button sidebar-close" ref={closeRef} onClick={onClose} aria-label={t('nav.closeMenu')}>
            <CloseIcon />
          </button>
        </div>

        <nav aria-label={t('nav.views')}>
          <ul className="views">
            {VIEWS.map((v) => (
              <li key={v}>
                <button
                  type="button"
                  className="view"
                  aria-current={view === v ? 'page' : undefined}
                  onClick={() => {
                    onView(v)
                    onClose()
                  }}
                >
                  {v === 'all' ? <span className="all-mark" aria-hidden="true" /> : <Flag kind={v} decorative />}
                  <span className="view-name">{t(`view.${v}`)}</span>
                  <span className="view-count">{counts[v]}</span>
                </button>
              </li>
            ))}
          </ul>
        </nav>

        <div className="sidebar-foot">
          <p className="connection" data-state={state} role="status">
            <span className="connection-dot" aria-hidden="true" />
            {t(key)}
          </p>
          <button type="button" className="text-button" onClick={() => { onOpenSettings(); onClose() }}>
            {t('conn.settings')}
          </button>
          <Appearance />
        </div>
      </aside>
      {open && <button type="button" className="scrim" aria-label={t('nav.closeMenu')} tabIndex={-1} onClick={onClose} />}
    </>
  )
}
