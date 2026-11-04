import { useCallback, useEffect, useMemo, useState } from 'react'
import { EventSheet } from './components/EventSheet'
import { Header } from './components/Header'
import { Lead } from './components/Lead'
import { Ledger } from './components/Ledger'
import { EmptyNotice, ErrorNotice } from './components/Notices'
import { SettingsDialog } from './components/SettingsDialog'
import { useEvents } from './hooks/useEvents'
import type { Settings } from './lib/api'
import { loadSettings, saveSettings } from './lib/settings'

const CLOCK_MS = 30_000

export default function App() {
  const [settings, setSettings] = useState<Settings>(loadSettings)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [now, setNow] = useState(() => new Date())
  const { feed, reload } = useEvents(settings)

  // Keep "2 min ago" honest between refreshes.
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), CLOCK_MS)
    return () => window.clearInterval(timer)
  }, [])

  const selected = useMemo(() => feed.events.find((e) => e.id === selectedId) ?? null, [feed.events, selectedId])
  const original = useMemo(
    () => (selected?.mergedInto ? feed.events.find((e) => e.id === selected.mergedInto) : undefined),
    [feed.events, selected],
  )

  const closeSheet = useCallback(() => setSelectedId(null), [])
  const openSettings = useCallback(() => setSettingsOpen(true), [])

  const failing = feed.status === 'error'
  const showEmpty = feed.status === 'ready' && feed.events.length === 0
  const hasEvents = feed.events.length > 0

  return (
    <div className="page" data-sheet={selected ? 'open' : 'closed'}>
      <Header feed={feed} onOpenSettings={openSettings} />

      <main className="main">
        {failing && (
          <ErrorNotice error={feed.error} settings={settings} onOpenSettings={openSettings} onRetry={reload} />
        )}
        {hasEvents && (
          <>
            <Lead events={feed.events} now={now} onOpen={setSelectedId} />
            <Ledger events={feed.events} now={now} selectedId={selectedId} onSelect={setSelectedId} />
          </>
        )}
        {showEmpty && <EmptyNotice settings={settings} />}
        {feed.status === 'loading' && !hasEvents && (
          <p className="loading" role="status">
            Loading events.
          </p>
        )}
      </main>

      {selected && (
        <EventSheet
          event={selected}
          original={original}
          settings={settings}
          onOpenOriginal={setSelectedId}
          onClose={closeSheet}
        />
      )}

      <SettingsDialog
        open={settingsOpen}
        settings={settings}
        onClose={() => setSettingsOpen(false)}
        onSave={(next) => {
          saveSettings(next)
          setSettings(next)
          setSettingsOpen(false)
        }}
      />
    </div>
  )
}
