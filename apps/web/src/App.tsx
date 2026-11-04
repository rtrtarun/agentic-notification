import { useCallback, useEffect, useMemo, useState } from 'react'
import { EventSheet } from './components/EventSheet'
import { Header } from './components/Header'
import { Lead } from './components/Lead'
import { Ledger } from './components/Ledger'
import { Welcome } from './components/Welcome'
import { EmptyNotice, ErrorNotice } from './components/Notices'
import { SettingsDialog } from './components/SettingsDialog'
import { useEvents } from './hooks/useEvents'
import { useMediaQuery } from './hooks/useMediaQuery'
import type { Settings } from './lib/api'
import { loadSettings, needsWelcome, saveSettings } from './lib/settings'

const CLOCK_MS = 30_000

/** Below this width the detail sheet covers the whole screen instead of docking beside the list. */
const SHEET_COVERS_PAGE = '(max-width: 61.999rem)'

export default function App() {
  const [settings, setSettings] = useState<Settings>(loadSettings)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [now, setNow] = useState(() => new Date())
  const [welcome, setWelcome] = useState(needsWelcome)
  const { feed, reload } = useEvents(settings, !welcome)
  const sheetCovers = useMediaQuery(SHEET_COVERS_PAGE)

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

  const tryDemo = useCallback(() => {
    const next: Settings = { ...settings, demo: true }
    saveSettings(next)
    setSettings(next)
    setWelcome(false)
  }, [settings])
  const closeSheet = useCallback(() => setSelectedId(null), [])
  const openSettings = useCallback(() => setSettingsOpen(true), [])

  const failing = feed.status === 'error'
  const showEmpty = feed.status === 'ready' && feed.events.length === 0
  const hasEvents = feed.events.length > 0
  // When the sheet covers the screen, the page behind it must not be reachable by keyboard or screen reader.
  const behindSheet = selected !== null && sheetCovers

  return (
    <div className="page" data-sheet={selected ? 'open' : 'closed'}>
      <Header feed={feed} onOpenSettings={openSettings} inert={behindSheet} demo={settings.demo === true && !welcome} hideStatus={welcome} />

      <main className="main" {...(behindSheet ? { inert: '' } : {})}>
        {welcome && <Welcome onTryDemo={tryDemo} onConnect={openSettings} />}
        {!welcome && settings.demo && (
          <p className="demo-banner" role="note">
            You are looking at sample data. Nothing is sent or saved.{' '}
            <button type="button" className="text-button" onClick={openSettings}>
              Connect your service
            </button>
          </p>
        )}
        {!welcome && failing && (
          <ErrorNotice error={feed.error} settings={settings} onOpenSettings={openSettings} onRetry={reload} />
        )}
        {!welcome && hasEvents && (
          <>
            <Lead events={feed.events} now={now} onOpen={setSelectedId} />
            <Ledger events={feed.events} now={now} selectedId={selectedId} onSelect={setSelectedId} />
          </>
        )}
        {!welcome && showEmpty && <EmptyNotice settings={settings} />}
        {!welcome && feed.status === 'loading' && !hasEvents && (
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
          setWelcome(false)
          setSettingsOpen(false)
        }}
      />
    </div>
  )
}
