import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useApp } from '../app/AppState'
import { paletteShortcut } from '../app/AppTopBar'
import { useToast } from '../design/Toast'
import { LoadGate, useLoad } from '../features/manage/useLoad'
import { checkHealth, formatDiagnostics, type HealthResult } from '../lib/diagnostics'
import { useI18n } from '../lib/i18n/I18nContext'
import type { MessageKey } from '../lib/i18n'

const FAQ: { q: MessageKey; a: MessageKey }[] = [
  { q: 'help.faq.why.q', a: 'help.faq.why.a' },
  { q: 'help.faq.missing.q', a: 'help.faq.missing.a' },
  { q: 'help.faq.mute.q', a: 'help.faq.mute.a' },
  { q: 'help.faq.held.q', a: 'help.faq.held.a' },
  { q: 'help.faq.ai.q', a: 'help.faq.ai.a' },
  { q: 'help.faq.data.q', a: 'help.faq.data.a' },
]

const SHORTCUTS: { keys: string; action: MessageKey }[] = [
  { keys: '', action: 'help.keys.palette' },
  { keys: 'Esc', action: 'help.keys.close' },
  { keys: '↑ ↓ Enter', action: 'help.keys.list' },
  { keys: 'Tab', action: 'help.keys.tab' },
]

/** How to use the console, what the words mean, and a diagnostics summary to paste into a support request. */
export default function HelpPage() {
  const { t, locale } = useI18n()
  const toast = useToast()
  const { settings, liveStatus, summary, now } = useApp()
  const health = useLoad<HealthResult>((signal) => checkHealth(settings, fetch, signal), [settings])
  const [copied, setCopied] = useState(false)

  const diagnostics = (h: HealthResult) =>
    formatDiagnostics({
      appVersion: __APP_VERSION__,
      settings,
      health: h,
      live: liveStatus,
      language: locale,
      unread: summary.unread,
      userAgent: typeof navigator === 'undefined' ? '' : navigator.userAgent,
      now,
    })

  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.push(t('help.diag.copyFailed'), 'error')
    }
  }

  return (
    <section className="mpage" aria-labelledby="help-title">
      <div className="mpage-head">
        <div>
          <h1 className="page-title" id="help-title">
            {t('nav.help')}
          </h1>
          <p className="mpage-lede">{t('help.lede')}</p>
        </div>
      </div>

      <div className="panel">
        <h2 className="panel-title">{t('help.start')}</h2>
        <ol>
          <li>{t('help.start.1')}</li>
          <li>
            {t('help.start.2')} <Link to="/integrations">{t('nav.integrations')}</Link>
          </li>
          <li>
            {t('help.start.3')} <Link to="/rules">{t('nav.rules')}</Link>
          </li>
          <li>
            {t('help.start.4')} <Link to="/channels">{t('nav.channels')}</Link>
          </li>
        </ol>
      </div>

      <div className="panel-grid">
        <div className="panel">
          <h2 className="panel-title">{t('help.words')}</h2>
          <dl className="kv">
            <dt>{t('class.critical')}</dt>
            <dd>{t('class.critical.explain')}</dd>
            <dt>{t('class.standard')}</dt>
            <dd>{t('class.standard.explain')}</dd>
            <dt>{t('class.background')}</dt>
            <dd>{t('class.background.explain')}</dd>
            <dt>{t('outcome.delivered')}</dt>
            <dd>{t('help.word.delivered')}</dd>
            <dt>{t('outcome.held')}</dt>
            <dd>{t('help.word.held')}</dd>
            <dt>{t('outcome.merged')}</dt>
            <dd>{t('help.word.merged')}</dd>
            <dt>{t('level.high')}</dt>
            <dd>{t('help.word.high')}</dd>
          </dl>
        </div>

        <div className="panel">
          <h2 className="panel-title">{t('help.keys')}</h2>
          <dl className="kv">
            {SHORTCUTS.map((s) => (
              <div key={s.action} style={{ display: 'contents' }}>
                <dt>
                  <kbd>{s.keys || paletteShortcut()}</kbd>
                </dt>
                <dd>{t(s.action)}</dd>
              </div>
            ))}
          </dl>
        </div>
      </div>

      <div className="panel">
        <h2 className="panel-title">{t('help.faq')}</h2>
        {FAQ.map((item) => (
          <details key={item.q} style={{ marginBlock: '0.5rem' }}>
            <summary style={{ cursor: 'pointer', fontWeight: 650 }}>{t(item.q)}</summary>
            <p className="panel-note" style={{ marginBlockStart: '0.4rem' }}>
              {t(item.a)}
            </p>
          </details>
        ))}
      </div>

      <div className="panel">
        <h2 className="panel-title">{t('help.diag')}</h2>
        <p className="panel-note">{t('help.diag.note')}</p>
        <LoadGate state={health.state} onRetry={health.reload}>
          {(h) => {
            const text = diagnostics(h)
            return (
              <>
                <p>
                  <span className="status" data-state={h.health === 'ok' || h.health === 'demo' ? 'ok' : 'bad'}>
                    {t(`help.health.${h.health}`)}
                  </span>
                </p>
                <pre className="panel mono" style={{ overflowX: 'auto', margin: 0 }} aria-label={t('help.diag')}>
                  {text}
                </pre>
                <div className="form-actions">
                  <button type="button" className="button" onClick={() => void copy(text)}>
                    {copied ? t('int.copied') : t('help.diag.copy')}
                  </button>
                  <button type="button" className="button" onClick={health.reload}>
                    {t('help.diag.recheck')}
                  </button>
                </div>
              </>
            )
          }}
        </LoadGate>
      </div>
    </section>
  )
}
