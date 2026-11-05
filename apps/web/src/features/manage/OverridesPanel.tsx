import { useMemo, useState, type FormEvent } from 'react'
import { useApp } from '../../app/AppState'
import { useToast } from '../../design/Toast'
import { ApiError } from '../../lib/api'
import { relativeTime } from '../../lib/events'
import { useI18n } from '../../lib/i18n/I18nContext'
import { describeOverride, overrideProblem, overridesClientFor, type NewOverride, type Override } from '../../lib/overrides'
import { LoadGate, useLoad } from './useLoad'

const BLANK: NewOverride = { mode: 'never', source: '', eventType: '', repository: '' }

/** Every always and never notify choice, who made it, and a way to take it back. */
export function OverridesPanel() {
  const { t, locale } = useI18n()
  const toast = useToast()
  const { settings, now } = useApp()
  const client = useMemo(() => overridesClientFor(settings), [settings])
  const { state, reload } = useLoad((signal) => client.list(signal), [client])
  const [draft, setDraft] = useState<NewOverride>(BLANK)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const remove = async (o: Override) => {
    try {
      await client.remove(o.id)
      toast.push(t('ovr.removed'), 'success')
    } catch (err) {
      toast.push(err instanceof ApiError ? err.message : t('ovr.removeFailed'), 'error')
    }
    reload()
  }

  const add = async (e: FormEvent) => {
    e.preventDefault()
    if (overrideProblem(draft)) {
      setError(t('ovr.problem.scope'))
      return
    }
    setBusy(true)
    setError('')
    try {
      await client.create({ ...draft, source: draft.source?.trim(), eventType: draft.eventType?.trim(), repository: draft.repository?.trim() })
      toast.push(t('ovr.added'), 'success')
      setDraft(BLANK)
      reload()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t('ovr.addFailed'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="panel">
      <h2 className="panel-title">{t('ovr.title')}</h2>
      <p className="panel-note">{t('ovr.note')}</p>
      <LoadGate state={state} onRetry={reload}>
        {(data) => (
          <>
            {data.adminOnly && <p className="form-help">{t('ovr.adminOnly')}</p>}
            {data.overrides.length === 0 ? (
              <p className="ledger-empty">{t('ovr.none')}</p>
            ) : (
              <div className="table-wrap">
                <table className="data-table">
                  <caption className="sr-only">{t('ovr.title')}</caption>
                  <thead>
                    <tr>
                      <th scope="col">{t('ovr.covers')}</th>
                      <th scope="col">{t('ovr.mode')}</th>
                      <th scope="col">{t('ovr.by')}</th>
                      <th scope="col">
                        <span className="sr-only">{t('dl.actions')}</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.overrides.map((o) => (
                      <tr key={o.id}>
                        <td className="wrap">
                          {describeOverride(o)}
                          {o.note && <span className="form-help"> {o.note}</span>}
                        </td>
                        <td>
                          <span className="status" data-state={o.mode === 'always' ? 'ok' : 'warn'}>
                            {t(`ovr.mode.${o.mode}`)}
                          </span>
                        </td>
                        <td className="wrap" title={new Date(o.createdAt).toLocaleString(locale)}>
                          {o.createdBy} <span className="form-help">{relativeTime(o.createdAt, now, locale, t)}</span>
                        </td>
                        <td>
                          <button type="button" className="button" onClick={() => void remove(o)}>
                            {t('ovr.remove')}
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}

            <form onSubmit={add} aria-labelledby="ovr-add-title" noValidate>
              <h3 className="panel-title" id="ovr-add-title">
                {t('ovr.add')}
              </h3>
              <div className="form-grid">
                <label>
                  <span className="field-label">{t('ovr.mode')}</span>
                  <select className="field select" value={draft.mode} onChange={(e) => setDraft({ ...draft, mode: e.target.value as NewOverride['mode'] })}>
                    <option value="never">{t('ovr.mode.never')}</option>
                    <option value="always">{t('ovr.mode.always')}</option>
                  </select>
                </label>
                <label>
                  <span className="field-label">{t('all.source')}</span>
                  <input className="field" value={draft.source ?? ''} placeholder="github" onChange={(e) => setDraft({ ...draft, source: e.target.value })} />
                </label>
                <label>
                  <span className="field-label">{t('all.event')}</span>
                  <input className="field" value={draft.eventType ?? ''} placeholder="star" onChange={(e) => setDraft({ ...draft, eventType: e.target.value })} />
                </label>
                <label>
                  <span className="field-label">{t('all.repository')}</span>
                  <input className="field" value={draft.repository ?? ''} placeholder="acme/*" onChange={(e) => setDraft({ ...draft, repository: e.target.value })} />
                </label>
              </div>
              {error && (
                <p className="field-error" role="alert">
                  {error}
                </p>
              )}
              <div className="form-actions">
                <button type="submit" className="button" disabled={busy}>
                  {t('ovr.addButton')}
                </button>
              </div>
            </form>
          </>
        )}
      </LoadGate>
    </div>
  )
}
