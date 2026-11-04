import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import { I18nProvider } from './lib/i18n/I18nContext'
import { applyTheme, detectTheme } from './lib/theme'
import './styles.css'
import './design/tokens.css'
import './app/shell.css'
import './features/inbox/inbox.css'
import './features/dashboard/dashboard.css'

// Apply the saved theme before the first paint so there is no flash of the wrong colours.
applyTheme(detectTheme())

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <I18nProvider>
      <App />
    </I18nProvider>
  </React.StrictMode>,
)
