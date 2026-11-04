import { useI18n } from '../lib/i18n/I18nContext'
import { Appearance } from './Appearance'
import { Legend } from './Legend'

interface WelcomeProps {
  onTryDemo: () => void
  onConnect: () => void
}

/** Shown on a first visit, before anything is connected. Two clear ways forward. */
export function Welcome({ onTryDemo, onConnect }: WelcomeProps) {
  const { t } = useI18n()
  return (
    <div className="welcome-page">
      <header className="welcome-top">
        <p className="wordmark">{t('app.name')}</p>
        <Appearance />
      </header>
      <main className="welcome" aria-labelledby="welcome-heading">
        <h1 id="welcome-heading" className="welcome-title">
          {t('welcome.title')}
        </h1>
        <p className="lead-detail">{t('welcome.body')}</p>

        <h2 className="sheet-subtitle">{t('welcome.how')}</h2>
        <Legend />

        <div className="actions welcome-actions">
          <button type="button" className="button button-primary" onClick={onTryDemo}>
            {t('welcome.demo')}
          </button>
          <button type="button" className="button" onClick={onConnect}>
            {t('welcome.connect')}
          </button>
        </div>
        <p className="hint">{t('welcome.demoHint')}</p>
      </main>
    </div>
  )
}
