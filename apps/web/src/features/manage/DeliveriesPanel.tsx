import { useMemo, useState } from 'react'
import { useApp } from '../../app/AppState'
import { useToast } from '../../design/Toast'
import { canRetry, deliveriesClientFor, summarise, type Delivery } from '../../lib/deliveries'
import { relativeTime } from '../../lib/events'
import { useI18n } from '../../lib/i18n/I18nContext'
import { LoadGate, useLoad } from './useLoad'

const PAGE = 25

/** What was sent, what is waiting and what gave up. Targets and message text are never shown. */
export function DeliveriesPanel() {
  const { t, locale } = useI18n()
  const toast = useToast()
  const { settings, now } = useApp()
  const client = useMemo(() => deliveriesClientFor(settings), [settings])
  const { state, reload } = useLoad((signal) => client.list(PAGE, signal), [client], 15_000)
  const [retrying, setRetrying] = useState<string | null>(null)

  const retry = async (d: Delivery) => {
    setRetrying(d.id)
    try {
      await client.retry(d.id)
      toast.push(t('dl.retried'), 'success')
      reload()
    } catch {
      toast.push(t('dl.retryFailed'), 'error')
    } finally {
      setRetrying(null)
    }
  }

  return (
    <div className="panel">
      <h2 className="panel-title">{t('dl.title')}</h2>
      <p className="panel-note">{t('dl.note')}</p>
      <LoadGate state={state} onRetry={reload}>
        {(log) => {
          const c = summarise(log.counts)
          return (
            <>
              <p className="form-help">{t('dl.counts', { sent: c.sent, pending: c.pending + c.sending, dead: c.dead })}</p>
              {log.deliveries.length === 0 ? (
                <p className="ledger-empty">{t('dl.none')}</p>
              ) : (
                <div className="table-wrap">
                  <table className="data-table">
                    <caption className="sr-only">{t('dl.title')}</caption>
                    <thead>
                      <tr>
                        <th scope="col">{t('dl.when')}</th>
                        <th scope="col">{t('dl.event')}</th>
                        <th scope="col">{t('dl.channel')}</th>
                        <th scope="col">{t('dl.status')}</th>
                        <th scope="col">{t('dl.detail')}</th>
                        <th scope="col">
                          <span className="sr-only">{t('dl.actions')}</span>
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {log.deliveries.map((d) => (
                        <tr key={d.id}>
                          <td title={new Date(d.createdAt).toLocaleString(locale)}>{relativeTime(d.createdAt, now, locale, t)}</td>
                          <td className="wrap">{d.title}</td>
                          <td>{d.channel}</td>
                          <td>
                            <span className="status" data-state={d.status === 'sent' ? 'ok' : d.status === 'dead' ? 'bad' : 'warn'}>
                              {t(`dl.status.${d.status}`)}
                            </span>
                          </td>
                          <td className="wrap">
                            {d.direct && <span className="form-help">{t('dl.direct')} </span>}
                            {d.status !== 'sent' && d.attempts > 0 && <span className="form-help">{t('dl.attempts', { n: d.attempts })} </span>}
                            {d.lastError}
                          </td>
                          <td>
                            {canRetry(d) && (
                              <button type="button" className="button" disabled={retrying !== null} onClick={() => void retry(d)}>
                                {t('dl.retry')}
                              </button>
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </>
          )
        }}
      </LoadGate>
    </div>
  )
}
