import { useApp } from '../app/AppState'
import { Appearance } from '../components/Appearance'
import { useI18n } from '../lib/i18n/I18nContext'

/** Connection and appearance. Workspace and notification settings arrive in milestone 20. */
export default function SettingsPage() {
  const { t } = useI18n()
  const { settings, demo, openSettings } = useApp()
  const status = demo
    ? t('settings.page.modeSample')
    : settings.backendUrl
      ? t('settings.page.modeLive', { url: settings.backendUrl })
      : t('settings.page.modeNone')

  return (
    <>
      <h1 className="page-title">{t('nav.settings')}</h1>

      <section className="card" aria-labelledby="connection-title">
        <h2 className="card-title" id="connection-title">
          {t('settings.page.connection')}
        </h2>
        <p className="prose">{t('settings.page.connectionBody')}</p>
        <p className="prose">{status}</p>
        <button type="button" className="button" onClick={openSettings}>
          {t('conn.settings')}
        </button>
      </section>

      <section className="card" aria-labelledby="appearance-title">
        <h2 className="card-title" id="appearance-title">
          {t('appearance.title')}
        </h2>
        <Appearance />
      </section>
    </>
  )
}
