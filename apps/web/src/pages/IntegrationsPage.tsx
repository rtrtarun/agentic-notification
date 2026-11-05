import { useMemo, useState } from 'react'
import { useApp } from '../app/AppState'
import { useToast } from '../design/Toast'
import { LoadGate, useLoad } from '../features/manage/useLoad'
import { channelsClientFor, webhookUrl, type Integration } from '../lib/channels'
import { eventLabel, relativeTime } from '../lib/events'
import { useI18n } from '../lib/i18n/I18nContext'

const STATE = { ready: 'ok', unsigned: 'warn', blocked: 'bad' } as const

/** Inbound sources: how to connect them, whether they are secured, and what has arrived. */
export default function IntegrationsPage() {
  const { t, tag } = useI18n()
  const toast = useToast()
  const { settings, now, demo } = useApp()
  const client = useMemo(() => channelsClientFor(settings), [settings])
  const { state, reload } = useLoad((signal) => client.integrations(signal), [client], 30_000)
  const [copied, setCopied] = useState(false)

  const copy = async (url: string) => {
    try {
      await navigator.clipboard.writeText(url)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.push(t('int.copyFailed'), 'error')
    }
  }

  const card = (i: Integration) => {
    const url = demo ? '' : webhookUrl(settings.backendUrl, i.webhookPath)
    return (
      <li key={i.id} className="item-card">
        <h3>{i.name}</h3>
        <p>
          <span className="status" data-state={STATE[i.status]}>
            {t(`int.status.${i.status}`)}
          </span>
        </p>
        <p className="panel-note">{t(`int.status.${i.status}.body`)}</p>
        <dl className="kv">
          <dt>{t('int.webhook')}</dt>
          <dd>
            <code>{url || i.webhookPath}</code>{' '}
            {url && (
              <button type="button" className="row-link" onClick={() => void copy(url)}>
                {copied ? t('int.copied') : t('int.copy')}
              </button>
            )}
          </dd>
          <dt>{t('int.signing')}</dt>
          <dd>{i.signing}</dd>
          <dt>{t('int.secret')}</dt>
          <dd>{i.secretConfigured ? t('int.secretSet') : t('int.secretUnset', { name: i.setup })}</dd>
          <dt>{t('int.received')}</dt>
          <dd>{i.eventsReceived}</dd>
          <dt>{t('int.last')}</dt>
          <dd>
            {i.lastEventAt ? (
              <time dateTime={i.lastEventAt} title={new Date(i.lastEventAt).toLocaleString(tag)}>
                {relativeTime(i.lastEventAt, now, tag, t)}
                {i.lastEventType ? ` · ${eventLabel(i.lastEventType, t)}` : ''}
              </time>
            ) : (
              t('int.never')
            )}
          </dd>
        </dl>
        {i.eventTypes.length > 0 && (
          <>
            <h4 className="toolbar-legend">{t('int.eventTypes')}</h4>
            <ul className="chip-group" aria-label={t('int.eventTypes')}>
              {i.eventTypes.map((e) => (
                <li key={e.eventType} className="chip-toggle">
                  {eventLabel(e.eventType, t)} · {e.count}
                </li>
              ))}
            </ul>
          </>
        )}
      </li>
    )
  }

  return (
    <section className="mpage" aria-labelledby="int-title">
      <div className="mpage-head">
        <div>
          <h1 className="page-title" id="int-title">
            {t('nav.integrations')}
          </h1>
          <p className="mpage-lede">{t('int.lede')}</p>
        </div>
      </div>
      <LoadGate state={state} onRetry={reload}>
        {(items) => (
          <>
            <ul className="card-list" aria-label={t('nav.integrations')}>
              {items.map(card)}
            </ul>
            <div className="panel">
              <h2 className="panel-title">{t('int.howTitle')}</h2>
              <ol>
                <li>{t('int.how1')}</li>
                <li>{t('int.how2')}</li>
                <li>{t('int.how3')}</li>
              </ol>
              <p className="form-help">{t('int.more')}</p>
            </div>
          </>
        )}
      </LoadGate>
    </section>
  )
}
