import { useMemo, useState } from 'react'
import { useApp } from '../../app/AppState'
import { useAuth } from '../../app/AuthGate'
import { useToast } from '../../design/Toast'
import { ApiError } from '../../lib/api'
import { useI18n } from '../../lib/i18n/I18nContext'
import { evidenceParts, learningClientFor, type Suggestion } from '../../lib/learning'
import { LoadGate, useLoad } from './useLoad'

/** Suggestions made from feedback: what people dismissed or asked for, and the change that would follow. */
export function LearningPanel() {
  const { t } = useI18n()
  const toast = useToast()
  const { settings } = useApp()
  const { isAdmin } = useAuth()
  const client = useMemo(() => learningClientFor(settings), [settings])
  const { state, reload } = useLoad((signal) => client.list(signal), [client])
  const [busyId, setBusyId] = useState<string | null>(null)

  const resolve = async (s: Suggestion, action: 'apply' | 'dismiss') => {
    setBusyId(s.id)
    try {
      await (action === 'apply' ? client.apply(s.id) : client.dismiss(s.id))
      toast.push(t(action === 'apply' ? 'learn.applied' : 'learn.dismissed'), 'success')
      reload()
    } catch (err) {
      toast.push(err instanceof ApiError ? err.message : t('learn.failed'), 'error')
    } finally {
      setBusyId(null)
    }
  }

  return (
    <section aria-labelledby="learn-title" className="learn-panel">
      <h2 className="sheet-subtitle" id="learn-title">
        {t('learn.title')}
      </h2>
      <p className="form-help">{t('learn.lede')}</p>
      <LoadGate state={state} onRetry={reload}>
        {(data) =>
          data.suggestions.length === 0 ? (
            <p className="form-help">{t('learn.none', { n: data.minSignals })}</p>
          ) : (
            <ul className="item-list">
              {data.suggestions.map((s) => (
                <li className="item-card" key={s.id}>
                  <p>{s.summary}</p>
                  <p>
                    <strong>{t('learn.proposed')}</strong> {s.proposed}
                  </p>
                  <p className="form-help">
                    {t('learn.evidence')}: {evidenceParts(s.evidence).map((p) => t(`learn.signal.${p.label}`, { n: p.n })).join(', ')}
                  </p>
                  {isAdmin ? (
                    <div className="mpage-actions">
                      <button type="button" className="button button-primary" disabled={busyId !== null} onClick={() => void resolve(s, 'apply')}>
                        {t('learn.apply')}
                      </button>
                      <button type="button" className="button" disabled={busyId !== null} onClick={() => void resolve(s, 'dismiss')}>
                        {t('learn.dismiss')}
                      </button>
                    </div>
                  ) : (
                    <p className="form-help">{t('learn.adminOnly')}</p>
                  )}
                </li>
              ))}
            </ul>
          )
        }
      </LoadGate>
      <p className="form-help">{t('learn.criticalNote')}</p>
    </section>
  )
}
