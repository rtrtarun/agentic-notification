import { Suspense, useCallback, useEffect, useState } from 'react'
import { NavLink, Outlet } from 'react-router-dom'
import { NavIcon } from '../components/icons'
import { NotificationDrawer } from '../features/inbox/NotificationDrawer'
import { useMediaQuery } from '../hooks/useMediaQuery'
import { useI18n } from '../lib/i18n/I18nContext'
import { NAV_ITEMS } from '../lib/nav'
import { useApp } from './AppState'
import { AppSidebar } from './AppSidebar'
import { AppTopBar } from './AppTopBar'
import { CommandPalette } from './CommandPalette'

const COLLAPSE_KEY = 'attention.sidebar.collapsed'
/** Below this width the detail sheet covers the page instead of docking beside it. */
const SHEET_COVERS_PAGE = '(max-width: 79.999rem)'
/** Below this width the sidebar is a drawer; on phones a bottom bar carries the main navigation. */
const SIDEBAR_IS_DRAWER = '(max-width: 61.999rem)'

function loadCollapsed(): boolean {
  try {
    return localStorage.getItem(COLLAPSE_KEY) === '1'
  } catch {
    return false
  }
}

/** The frame every screen sits in: sidebar, top bar, command palette and the mobile bottom bar. */
export function Shell() {
  const { t } = useI18n()
  const { feed, demo, summary, selectedId } = useApp()
  const [collapsed, setCollapsed] = useState(loadCollapsed)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const isDrawer = useMediaQuery(SIDEBAR_IS_DRAWER)
  const sheetCovers = useMediaQuery(SHEET_COVERS_PAGE)

  const toggleCollapsed = useCallback(() => {
    setCollapsed((v) => {
      try {
        localStorage.setItem(COLLAPSE_KEY, v ? '0' : '1')
      } catch {
        // the choice just will not persist
      }
      return !v
    })
  }, [])
  const closeDrawer = useCallback(() => setDrawerOpen(false), [])
  const closePalette = useCallback(() => setPaletteOpen(false), [])

  // Ctrl/Cmd+K opens the palette from anywhere.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setPaletteOpen((v) => !v)
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  const drawerVisible = drawerOpen && isDrawer
  // While the drawer or the palette covers the screen, what is behind must not be reachable.
  const behindSheet = selectedId !== null && sheetCovers
  const behind = drawerVisible || paletteOpen || behindSheet

  return (
    <div className="app" data-sidebar={collapsed ? 'collapsed' : 'full'} data-sheet={selectedId ? 'open' : 'closed'}>
      <a className="skip-link" href="#main">
        {t('skip.main')}
      </a>
      <AppSidebar
        feed={feed}
        demo={demo}
        unread={summary.unread}
        collapsed={collapsed && !isDrawer}
        onToggleCollapsed={toggleCollapsed}
        open={drawerVisible}
        onClose={closeDrawer}
        inert={paletteOpen || behindSheet}
      />

      <div className="workspace" {...(behind ? { inert: '' } : {})}>
        <AppTopBar onMenu={() => setDrawerOpen(true)} menuOpen={drawerVisible} onPalette={() => setPaletteOpen(true)} />
        <main className="main" id="main" tabIndex={-1}>
          <Suspense fallback={<p className="loading" role="status">{t('loading')}</p>}>
            <Outlet />
          </Suspense>
        </main>
        <BottomNav />
      </div>

      {selectedId && <NotificationDrawer id={selectedId} />}

      <CommandPalette open={paletteOpen} onClose={closePalette} />
    </div>
  )
}

/** Phone navigation: the few screens people open most, within thumb reach. */
function BottomNav() {
  const { t } = useI18n()
  const { summary } = useApp()
  return (
    <nav className="bottom-nav" aria-label={t('nav.bottom')}>
      {NAV_ITEMS.filter((i) => i.primary).map((item) => (
        <NavLink key={item.id} to={item.path} className="bottom-link">
          <NavIcon name={item.icon} />
          <span>{t(item.label)}</span>
          {item.id === 'inbox' && summary.unread > 0 && <span className="nav-badge">{summary.unread > 99 ? '99+' : summary.unread}</span>}
        </NavLink>
      ))}
    </nav>
  )
}
