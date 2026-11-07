import { useMemo, useState } from 'react'
import { useApp } from '../app/AppState'
import { useToast } from '../design/Toast'
import { DeliveriesPanel } from '../features/manage/DeliveriesPanel'
import { LoadGate, useLoad } from '../features/manage/useLoad'
import { channelsClientFor, testFailureKey, type Channel } from '../lib/channels'
import { useI18n } from '../lib/i18n/I18nContext'

/** Where critical notifications are delivered. Targets are masked; a test sends one labelled message. */
export default function ChannelsPage() {
  const { t } = useI18n()
  const toast = useToast()
  const { settings, demo } = useApp()
  const client = useMemo(() => channelsClientFor(settings), [settings])
  const { state, reload } = useLoad((signal) => client.channels(signal), [client])
  const [testing, setTesting] = useState<string | null>(null)

  const test = async (channel: Channel) => {
    setTesting(channel.id)
    try {
      await client.test(channel.id)
      toast.push(t(demo ? 'ch.testedDemo' : 'ch.tested', { name: channel.name }), 'success')
    } catch (err) {
      const kind = testFailureKey(err instanceof Error ? err.message : '')
      toast.push(t(`ch.testFailed.${kind}`, { name: channel.name }), 'error')
    } finally {
      setTesting(null)
    }
  }

  return (
    <section className="mpage" aria-labelledby="ch-title">
      <div className="mpage-head">
        <div>
          <h1 className="page-title" id="ch-title">
            {t('nav.channels')}
          </h1>
          <p className="mpage-lede">{t('ch.lede')}</p>
        </div>
      </div>

      <LoadGate state={state} onRetry={reload}>
        {(channels) => (
          <>
            <ul className="card-list" aria-label={t('nav.channels')}>
              {channels.map((c) => (
                <li key={c.id} className="item-card">
                  <h3>{c.name}</h3>
                  <p>
                    <span className="status" data-state={c.configured ? 'ok' : 'warn'}>
                      {c.configured ? t('ch.ready') : t('ch.notConfigured')}
                    </span>
                    {c.mode === 'mock' && (
                      <>
                        {' '}
                        <span className="status" data-state="warn">
                          {t('ch.mock')}
                        </span>
                      </>
                    )}
                  </p>
                  <dl className="kv">
                    {c.configured && (
                      <>
                        <dt>{t('ch.target')}</dt>
                        <dd>{c.target}</dd>
                      </>
                    )}
                    <dt>{t('ch.receives')}</dt>
                    <dd>{c.receives.map((r) => t(`class.${r as 'critical'}`)).join(', ')}</dd>
                    {!c.configured && (
                      <>
                        <dt>{t('ch.setup')}</dt>
                        <dd className="mono">{c.setup}</dd>
                      </>
                    )}
                  </dl>
                  <div className="form-actions">
                    <button type="button" className="button" disabled={!c.configured || testing !== null} onClick={() => void test(c)}>
                      {testing === c.id ? t('ch.testing') : t('ch.test')}
                    </button>
                  </div>
                </li>
              ))}
            </ul>
            <div className="panel">
              <h2 className="panel-title">{t('ch.whyCritical')}</h2>
              <p className="panel-note">{t('ch.whyCriticalBody')}</p>
              <p className="form-help">{t('ch.secrets')}</p>
            </div>
            <DeliveriesPanel />
          </>
        )}
      </LoadGate>
    </section>
  )
}
