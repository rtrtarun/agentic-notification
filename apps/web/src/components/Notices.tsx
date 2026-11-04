import { useState } from 'react'
import type { ApiError, Settings } from '../lib/api'
import { useI18n } from '../lib/i18n/I18nContext'

interface ErrorNoticeProps {
  error: ApiError
  settings: Settings
  onOpenSettings: () => void
  onRetry: () => void
}

/** Says what went wrong and what to do about it. Never apologises, never vague. */
export function ErrorNotice({ error, settings, onOpenSettings, onRetry }: ErrorNoticeProps) {
  const { t } = useI18n()
  switch (error.kind) {
    case 'unauthorized':
    case 'unconfigured': {
      const base = error.kind === 'unauthorized' ? 'err.unauthorized' : 'err.unconfigured'
      return (
        <section className="notice" role="alert">
          <h2 className="notice-title">{t(`${base}.title`)}</h2>
          <p className="prose">{t(`${base}.body`)}</p>
          <button type="button" className="button button-primary" onClick={onOpenSettings}>
            {t('err.enterKey')}
          </button>
        </section>
      )
    }
    case 'unreachable':
      return (
        <section className="notice" role="alert">
          <h2 className="notice-title">{t('err.unreachable.title', { url: settings.backendUrl })}</h2>
          <p className="prose">{t('err.unreachable.body', { origin: window.location.origin })}</p>
          <div className="actions">
            <button type="button" className="button button-primary" onClick={onRetry}>
              {t('err.retry')}
            </button>
            <button type="button" className="button" onClick={onOpenSettings}>
              {t('err.changeAddress')}
            </button>
          </div>
        </section>
      )
    default:
      return (
        <section className="notice" role="alert">
          <h2 className="notice-title">{t('err.server.title')}</h2>
          <p className="prose">{t('err.server.body')}</p>
          <button type="button" className="button button-primary" onClick={onRetry}>
            {t('err.retry')}
          </button>
        </section>
      )
  }
}

/** Shown when the service is healthy but has not received an event yet. */
export function EmptyNotice({ settings }: { settings: Settings }) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)
  const webhookUrl = `${settings.backendUrl.replace(/\/+$/, '')}/webhooks/github`

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(webhookUrl)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2500)
    } catch {
      setCopied(false)
    }
  }

  return (
    <section className="notice" aria-labelledby="empty-heading">
      <h2 id="empty-heading" className="notice-title">
        {t('empty.title')}
      </h2>
      <p className="prose">{t('empty.body')}</p>
      <ol className="steps">
        <li>{t('empty.step1')}</li>
        <li>{t('empty.step2', { url: webhookUrl })}</li>
        <li>{t('empty.step3')}</li>
      </ol>
      <button type="button" className="button" onClick={copy}>
        {copied ? t('empty.copied') : t('empty.copy')}
      </button>
    </section>
  )
}
