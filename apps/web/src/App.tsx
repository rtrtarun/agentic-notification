import { useCallback, useEffect, useMemo, useState } from 'react'
import { EventSheet } from './components/EventSheet'
import { Lead } from './components/Lead'
import { Ledger } from './components/Ledger'
import { EmptyNotice, ErrorNotice } from './components/Notices'
import { Pager } from './components/Pager'
import { SettingsDialog } from './components/SettingsDialog'
import { Sidebar } from './components/Sidebar'
import { TopBar } from './components/TopBar'
import { Welcome } from './components/Welcome'
import { useEvents } from './hooks/useEvents'
import { useMediaQuery } from './hooks/useMediaQuery'
import { useRead } from './hooks/useRead'
import type { Settings } from './lib/api'
import { groupThreads } from './lib/events'
import { DEFAULT_FILTERS, countByView, filterEvents, isFiltered, paginate, type Filters } from './lib/filters'
import { useI18n } from './lib/i18n/I18nContext'
import { loadSettings, needsWelcome, saveSettings } from './lib/settings'

const CLOCK_MS = 30_000

/** Below these widths the detail sheet covers the page and the sidebar becomes a drawer. */
const SHEET_COVERS_PAGE = '(max-width: 79.999rem)'
const SIDEBAR_IS_DRAWER = '(max-width: 61.999rem)'

export default function App() {
  const { t } = useI18n()
  const [settings, setSettings] = useState<Settings>(loadSettings)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [now, setNow] = useState(() => new Date())
  const [welcome, setWelcome] = useState(needsWelcome)
  const [filters, setFilters] = useState<Filters>(DEFAULT_FILTERS)
  const [pageNumber, setPageNumber] = useState(1)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const { feed, reload } = useEvents(settings, !welcome)
  const { read, markRead, markAllRead } = useRead()
  const sheetCovers = useMediaQuery(SHEET_COVERS_PAGE)
  const sidebarIsDrawer = useMediaQuery(SIDEBAR_IS_DRAWER)

  // Keep "2 min ago" honest between refreshes.
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), CLOCK_MS)
    return () => window.clearInterval(timer)
  }, [])

  const events = feed.events
  const selected = useMemo(() => events.find((e) => e.id === selectedId) ?? null, [events, selectedId])
  const original = useMemo(
    () => (selected?.mergedInto ? events.find((e) => e.id === selected.mergedInto) : undefined),
    [events, selected],
  )

  const counts = useMemo(() => countByView(events), [events])
  const threads = useMemo(() => groupThreads(filterEvents(events, filters, now, t)), [events, filters, now, t])
  const page = useMemo(() => paginate(threads, pageNumber), [threads, pageNumber])
  // Repeats merged into an earlier event do not count as separate unread items.
  const unread = useMemo(() => events.filter((e) => !e.mergedInto && !read.has(e.id)), [events, read])

  const changeFilters = useCallback((patch: Partial<Filters>) => {
    setFilters((previous) => ({ ...previous, ...patch }))
    setPageNumber(1)
  }, [])
  const clearFilters = useCallback(() => {
    setFilters(DEFAULT_FILTERS)
    setPageNumber(1)
  }, [])

  const selectEvent = useCallback(
    (id: string) => {
      setSelectedId(id)
      markRead(id)
    },
    [markRead],
  )
  const closeSheet = useCallback(() => setSelectedId(null), [])
  const openSettings = useCallback(() => setSettingsOpen(true), [])
  const closeDrawer = useCallback(() => setDrawerOpen(false), [])
  const markEverythingRead = useCallback(() => markAllRead(events.map((e) => e.id)), [markAllRead, events])

  const tryDemo = useCallback(() => {
    const next: Settings = { ...settings, demo: true }
    saveSettings(next)
    setSettings(next)
    setWelcome(false)
  }, [settings])

  const settingsDialog = (
    <SettingsDialog
      open={settingsOpen}
      settings={settings}
      onClose={() => setSettingsOpen(false)}
      onSave={(next) => {
        saveSettings(next)
        setSettings(next)
        setWelcome(false)
        setSettingsOpen(false)
      }}
    />
  )

  if (welcome) {
    return (
      <>
        <Welcome onTryDemo={tryDemo} onConnect={openSettings} />
        {settingsDialog}
      </>
    )
  }

  const failing = feed.status === 'error'
  const showEmpty = feed.status === 'ready' && events.length === 0
  const hasEvents = events.length > 0
  const demo = settings.demo === true
  const drawerVisible = drawerOpen && sidebarIsDrawer
  // While a layer covers the screen, what is behind it must not be reachable.
  const behindSheet = selected !== null && sheetCovers
  const workspaceInert = behindSheet || drawerVisible

  return (
    <div className="app" data-sheet={selected ? 'open' : 'closed'}>
      <Sidebar
        feed={feed}
        demo={demo}
        counts={counts}
        view={filters.view}
        onView={(view) => changeFilters({ view })}
        onOpenSettings={openSettings}
        open={drawerVisible}
        onClose={closeDrawer}
        inert={behindSheet}
      />

      <div className="workspace" {...(workspaceInert ? { inert: '' } : {})}>
        <TopBar
          query={filters.query}
          onQuery={(query) => changeFilters({ query })}
          range={filters.range}
          onRange={(range) => changeFilters({ range })}
          unread={unread}
          now={now}
          onOpenEvent={selectEvent}
          onMarkAllRead={markEverythingRead}
          onViewAll={clearFilters}
          onMenu={() => setDrawerOpen(true)}
          menuOpen={drawerVisible}
        />

        <main className="main">
          <div className="page-head">
            <h1 className="page-title">
              {t('page.title')}
              {unread.length > 0 && <span className="count-badge">{unread.length}</span>}
            </h1>
            {hasEvents && (
              <button type="button" className="text-button" disabled={unread.length === 0} onClick={markEverythingRead}>
                {t('list.markAll')}
              </button>
            )}
          </div>

          {demo && (
            <p className="demo-banner" role="note">
              {t('demo.banner')}{' '}
              <button type="button" className="text-button" onClick={openSettings}>
                {t('welcome.connect')}
              </button>
            </p>
          )}
          {failing && (
            <ErrorNotice error={feed.error} settings={settings} onOpenSettings={openSettings} onRetry={reload} />
          )}
          {hasEvents && (
            <>
              <Lead events={events} now={now} onOpen={selectEvent} />
              <p className="list-help">{t('list.help')}</p>
              <Ledger
                threads={page.items}
                now={now}
                selectedId={selectedId}
                isRead={(id) => read.has(id)}
                onSelect={selectEvent}
                filtered={isFiltered(filters)}
                onClear={clearFilters}
              />
              <Pager page={page.page} pages={page.pages} onPage={setPageNumber} />
            </>
          )}
          {showEmpty && <EmptyNotice settings={settings} />}
          {feed.status === 'loading' && !hasEvents && (
            <p className="loading" role="status">
              {t('loading')}
            </p>
          )}
        </main>
      </div>

      {selected && (
        <EventSheet
          event={selected}
          original={original}
          settings={settings}
          onOpenOriginal={selectEvent}
          onClose={closeSheet}
        />
      )}

      {settingsDialog}
    </div>
  )
}
