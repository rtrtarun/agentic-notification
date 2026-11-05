import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useEvents, type Feed } from '../hooks/useEvents'
import { useInboxSummary, type InboxSummary } from '../hooks/useInboxSummary'
import type { Settings } from '../lib/api'
import { inboxClientFor, type InboxClient } from '../lib/inbox'
import { DEFAULT_FILTERS, type InboxFilters } from '../lib/inboxQuery'
import { migrateLocalRead } from '../lib/read'
import { connectLive, type LiveStatus } from '../lib/live'
import { loadSettings, needsWelcome, saveSettings } from '../lib/settings'

const CLOCK_MS = 30_000
/** Several messages in a burst cause one refresh. */
const LIVE_REFRESH_MS = 300

/** Everything the shell and the screens share: the connection, the inbox client, the unread summary and filters. */
export interface AppState {
  settings: Settings
  demo: boolean
  /** The recent events, used by the lead banner and the connection state. */
  feed: Feed
  reload: () => void
  now: Date
  welcome: boolean
  tryDemo: () => void
  settingsOpen: boolean
  openSettings: () => void
  closeSettings: () => void
  saveConnection: (next: Settings) => void
  client: InboxClient
  /** State of the live connection; 'off' in demo mode. */
  liveStatus: LiveStatus
  /** Changes whenever something was updated; lists and the summary reload on it. */
  version: number
  /** Tell every list that something changed. */
  invalidate: () => void
  summary: InboxSummary
  markEverythingRead: () => Promise<number>
  /** The notification whose detail drawer is open. */
  selectedId: string | null
  selectEvent: (id: string) => void
  closeEvent: () => void
  filters: InboxFilters
  changeFilters: (patch: Partial<InboxFilters>) => void
  setFilters: (next: InboxFilters) => void
}

const AppContext = createContext<AppState | null>(null)

export function AppProvider({ children }: { children: ReactNode }) {
  const [settings, setSettings] = useState<Settings>(loadSettings)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [welcome, setWelcome] = useState(needsWelcome)
  const [filters, setFilters] = useState<InboxFilters>(DEFAULT_FILTERS)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [version, setVersion] = useState(0)
  const [now, setNow] = useState(() => new Date())
  const [liveStatus, setLiveStatus] = useState<LiveStatus>('off')
  const { feed, reload } = useEvents(settings, !welcome)
  const reloadRef = useRef(reload)
  reloadRef.current = reload
  const client = useMemo(() => inboxClientFor(settings), [settings])
  const summary = useInboxSummary(client, !welcome, version)

  // Keep "2 min ago" honest between refreshes.
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), CLOCK_MS)
    return () => window.clearInterval(timer)
  }, [])

  const invalidate = useCallback(() => setVersion((v) => v + 1), [])

  // Live updates: the service announces changes and the lists refresh at once.
  // Polling stays as the fallback, so a dropped connection costs freshness, not correctness.
  const liveEnabled = !welcome && !settings.demo && settings.backendUrl.trim() !== ''
  useEffect(() => {
    if (!liveEnabled) {
      setLiveStatus('off')
      return
    }
    let timer: number | undefined
    const handle = connectLive(settings, {
      onStatus: setLiveStatus,
      onMessage: (m) => {
        if (m.type === 'ready') return
        window.clearTimeout(timer)
        timer = window.setTimeout(() => {
          setVersion((v) => v + 1)
          reloadRef.current()
        }, LIVE_REFRESH_MS)
      },
    })
    return () => {
      window.clearTimeout(timer)
      handle.close()
    }
  }, [liveEnabled, settings])

  // Read state used to be kept in this browser; send it to the service once.
  const connected = summary.status === 'ready' && !settings.demo
  useEffect(() => {
    if (!connected) return
    migrateLocalRead(client)
      .then((sent) => sent > 0 && invalidate())
      .catch(() => undefined) // try again next visit; the ids are only forgotten after they were sent
  }, [connected, client, invalidate])

  const changeFilters = useCallback((patch: Partial<InboxFilters>) => setFilters((f) => ({ ...f, ...patch })), [])

  const markEverythingRead = useCallback(async () => {
    const changed = await client.markAllRead()
    invalidate()
    return changed
  }, [client, invalidate])

  const selectEvent = useCallback(
    (id: string) => {
      setSelectedId(id)
      // Opening a notification reads it. A failure here is not worth interrupting for.
      client
        .act(id, { type: 'read' })
        .then(invalidate)
        .catch(() => undefined)
    },
    [client, invalidate],
  )
  const closeEvent = useCallback(() => setSelectedId(null), [])

  const saveConnection = useCallback((next: Settings) => {
    saveSettings(next)
    setSettings(next)
    setWelcome(false)
    setSettingsOpen(false)
    setSelectedId(null)
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
      client,
      liveStatus,
      version,
      invalidate,
      summary,
      markEverythingRead,
      selectedId,
      selectEvent,
      closeEvent,
      filters,
      changeFilters,
      setFilters,
    }),
    [
      settings, feed, reload, now, welcome, tryDemo, settingsOpen, saveConnection, client, liveStatus, version,
      invalidate, summary, markEverythingRead, selectedId, selectEvent, closeEvent, filters, changeFilters,
    ],
  )

  return <AppContext.Provider value={value}>{children}</AppContext.Provider>
}

export function useApp(): AppState {
  const state = useContext(AppContext)
  if (!state) throw new Error('useApp must be used inside AppProvider')
  return state
}
