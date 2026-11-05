import { lazy } from 'react'
import { HashRouter, Navigate, Route, Routes } from 'react-router-dom'
import { AppProvider, useApp } from './app/AppState'
import { Shell } from './app/Shell'
import { SettingsDialog } from './components/SettingsDialog'
import { Welcome } from './components/Welcome'
import { ToastProvider } from './design/Toast'
import { HOME_PATH, NAV_ITEMS } from './lib/nav'

const InboxPage = lazy(() => import('./pages/InboxPage'))
const AllPage = lazy(() => import('./pages/AllPage'))
const SettingsPage = lazy(() => import('./pages/SettingsPage'))
const PlannedPage = lazy(() => import('./pages/PlannedPage'))

/** Screens that exist today. Everything else in the navigation resolves to a plain "not built yet" page. */
const BUILT: Record<string, React.ReactElement> = {
  inbox: <InboxPage />,
  all: <AllPage />,
  settings: <SettingsPage />,
}

function Screens() {
  const { welcome, tryDemo, openSettings, settingsOpen, closeSettings, saveConnection, settings } = useApp()

  const dialog = <SettingsDialog open={settingsOpen} settings={settings} onClose={closeSettings} onSave={saveConnection} />

  if (welcome) {
    return (
      <>
        <Welcome onTryDemo={tryDemo} onConnect={openSettings} />
        {dialog}
      </>
    )
  }

  return (
    <>
      <Routes>
        <Route element={<Shell />}>
          <Route index element={<Navigate to={HOME_PATH} replace />} />
          {NAV_ITEMS.map((item) => (
            <Route key={item.id} path={item.path} element={BUILT[item.id] ?? <PlannedPage item={item} />} />
          ))}
          <Route path="*" element={<Navigate to={HOME_PATH} replace />} />
        </Route>
      </Routes>
      {dialog}
    </>
  )
}

/**
 * Hash routing keeps deep links working on GitHub Pages and behind any static
 * host, where the server cannot rewrite unknown paths to index.html.
 */
export default function App() {
  return (
    <AppProvider>
      <ToastProvider>
        <HashRouter>
          <Screens />
        </HashRouter>
      </ToastProvider>
    </AppProvider>
  )
}
