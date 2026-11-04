import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useEvents, type Feed } from '../hooks/useEvents'
import { useRead } from '../hooks/useRead'
import type { EventSummary, Settings } from '../lib/api'
import { DEFAULT_FILTERS, type Filters } from '../lib/filters'
import { loadSettings, needsWelcome, saveSettings } from '../lib/settings'

const CLOCK_MS = 30_000

/** Everything the shell and the screens share: the connection, the events, read state and filters. */
export interface AppState {
  settings: Settings
  demo: boolean
  feed: Feed
  reload: () => void
  now: Date
  welcome: boolean
  tryDemo: () => void
  settingsOpen: boolean
  openSettings: () => void
  closeSettings: () => void
  saveConnection: (next: Settings) => void
  read: Set<string>
  markRead: (id: string) => void
  /** Unread events, newest first. Repeats merged into an earlier event are not counted. */
  unread: EventSummary[]
  markEverythingRead: () => void
  /** The event whose detail sheet is open. */
  selectedId: string | null
  selectEvent: (id: string) => void
  closeEvent: () => void
  filters: Filters
  changeFilters: (patch: Partial<Filters>) => void
  clearFilters: () => void
}

const AppContext = createContext<AppState | null>(null)

export function AppProvider({ children }: { children: ReactNode }) {
  const [settings, setSettings] = useState<Settings>(loadSettings)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [welcome, setWelcome] = useState(needsWelcome)
  const [filters, setFilters] = useState<Filters>(DEFAULT_FILTERS)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [now, setNow] = useState(() => new Date())
  const { feed, reload } = useEvents(settings, !welcome)
  const { read, markRead, markAllRead } = useRead()

  // Keep "2 min ago" honest between refreshes.
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), CLOCK_MS)
    return () => window.clearInterval(timer)
  }, [])

  const events = feed.events
  const unread = useMemo(() => events.filter((e) => !e.mergedInto && !read.has(e.id)), [events, read])

  const changeFilters = useCallback((patch: Partial<Filters>) => setFilters((f) => ({ ...f, ...patch })), [])
  const clearFilters = useCallback(() => setFilters(DEFAULT_FILTERS), [])
  const markEverythingRead = useCallback(() => markAllRead(events.map((e) => e.id)), [markAllRead, events])

  const selectEvent = useCallback(
    (id: string) => {
      setSelectedId(id)
      markRead(id)
    },
    [markRead],
  )
  const closeEvent = useCallback(() => setSelectedId(null), [])

  const saveConnection = useCallback((next: Settings) => {
    saveSettings(next)
    setSettings(next)
    setWelcome(false)
    setSettingsOpen(false)
  }, [])

  const tryDemo = useCallback(() => saveConnection({ ...settings, demo: true }), [saveConnection, settings])

  const value = useMemo<AppState>(
    () => ({
      settings,
      demo: settings.demo === true,
      feed,
      reload,
      now,
      welcome,
      tryDemo,
      settingsOpen,
      openSettings: () => setSettingsOpen(true),
      closeSettings: () => setSettingsOpen(false),
      saveConnection,
      read,
      markRead,
      unread,
      markEverythingRead,
      selectedId,
      selectEvent,
      closeEvent,
      filters,
      changeFilters,
      clearFilters,
    }),
    [
      settings, feed, reload, now, welcome, tryDemo, settingsOpen, saveConnection,
      read, markRead, unread, markEverythingRead, selectedId, selectEvent, closeEvent, filters,
      changeFilters, clearFilters,
    ],
  )

  return <AppContext.Provider value={value}>{children}</AppContext.Provider>
}

export function useApp(): AppState {
  const state = useContext(AppContext)
  if (!state) throw new Error('useApp must be used inside AppProvider')
  return state
}
