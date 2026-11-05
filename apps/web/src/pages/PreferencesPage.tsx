import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { useApp } from '../app/AppState'
import { useToast } from '../design/Toast'
import { LoadGate, useLoad } from '../features/manage/useLoad'
import { useI18n } from '../lib/i18n/I18nContext'
import {
  addToList,
  localTimezone,
  preferencesClientFor,
  quietHoursProblem,
  removeFromList,
  timezones,
  type Preferences,
} from '../lib/preferences'

/** Quiet hours and muted sources. They never apply to critical notifications. */
export default function PreferencesPage() {
  const { t } = useI18n()
  const { settings } = useApp()
  const client = useMemo(() => preferencesClientFor(settings), [settings])
  const { state, reload } = useLoad((signal) => client.get(signal), [client])

  return (
    <section className="mpage" aria-labelledby="pref-title">
      <div className="mpage-head">
        <div>
          <h1 className="page-title" id="pref-title">
            {t('nav.preferences')}
          </h1>
          <p className="mpage-lede">{t('pref.lede')}</p>
        </div>
        <Link className="button" to="/settings">
          {t('pref.appearance')}
        </Link>
      </div>
      <p className="panel" role="note">
        {t('pref.criticalNote')}
      </p>
      <LoadGate state={state} onRetry={reload}>
        {(loaded) => <PreferencesForm initial={loaded} save={(p) => client.save(p)} />}
      </LoadGate>
    </section>
  )
}

function PreferencesForm({ initial, save }: { initial: Preferences; save: (p: Preferences) => Promise<Preferences> }) {
  const { t, tag } = useI18n()
  const toast = useToast()
  const [draft, setDraft] = useState<Preferences>(initial)
  const [saved, setSaved] = useState<Preferences>(initial)
  const [busy, setBusy] = useState(false)
  const [source, setSource] = useState('')
  const [repo, setRepo] = useState('')
  const [error, setError] = useState('')

  // Another console may have changed them; take the newest when this form has no unsaved edits.
  const dirty = JSON.stringify(strip(draft)) !== JSON.stringify(strip(saved))
  useEffect(() => {
    if (!dirty) {
      setDraft(initial)
      setSaved(initial)
    }
    // Only a fresh load from the service should replace the draft.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initial])

  const zones = useMemo(() => timezones(draft.quietHours.timezone), [draft.quietHours.timezone])
  const problem = quietHoursProblem(draft.quietHours)
  const q = draft.quietHours
  const setQuiet = (patch: Partial<Preferences['quietHours']>) => setDraft({ ...draft, quietHours: { ...q, ...patch } })

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (problem) return
    setBusy(true)
    setError('')
    try {
      const next = await save(draft)
      setDraft(next)
      setSaved(next)
      toast.push(t('pref.saved'), 'success')
    } catch (err) {
      const message = err instanceof Error ? err.message : ''
      setError(message)
      toast.push(t('pref.failed', { message }), 'error')
    } finally {
      setBusy(false)
    }
  }

  const list = (label: string, hint: string, items: string[], value: string, setValue: (v: string) => void, kind: 'mutedSources' | 'mutedRepositories') => (
    <div className="panel">
      <h2 className="panel-title">{label}</h2>
      <p className="panel-note">{hint}</p>
      {items.length === 0 ? (
        <p className="form-help">{t('pref.nothingMuted')}</p>
      ) : (
        <ul className="chip-group" aria-label={label}>
          {items.map((item) => (
            <li key={item}>
              <button
                type="button"
                className="chip-toggle"
                aria-label={t('pref.unmute', { name: item })}
                onClick={() => setDraft({ ...draft, [kind]: removeFromList(items, item) })}
              >
                {item} ✕
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="toolbar" style={{ marginBlockStart: '0.75rem' }}>
        <label className="toolbar-field">
          <span>{t('pref.add')}</span>
          <input
            className="field"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                setDraft({ ...draft, [kind]: addToList(items, value, kind === 'mutedSources') })
                setValue('')
              }
            }}
          />
        </label>
        <button
          type="button"
          className="button"
          disabled={value.trim() === ''}
          onClick={() => {
            setDraft({ ...draft, [kind]: addToList(items, value, kind === 'mutedSources') })
            setValue('')
          }}
        >
          {t('pref.addButton')}
        </button>
      </div>
    </div>
  )

  return (
    <form onSubmit={submit} className="mpage" aria-label={t('nav.preferences')}>
      <div className="panel">
        <h2 className="panel-title">{t('pref.quiet')}</h2>
        <p className="panel-note">{t('pref.quietNote')}</p>
        <label className="switch">
          <input type="checkbox" checked={q.enabled} onChange={(e) => setQuiet({ enabled: e.target.checked })} />
          {t('pref.quietOn')}
        </label>
        <div className="form-grid" style={{ marginBlockStart: '0.75rem' }}>
          <label>
            <span className="field-label">{t('pref.start')}</span>
            <input className="field" type="time" value={q.start} aria-invalid={problem === 'start'} onChange={(e) => setQuiet({ start: e.target.value })} />
          </label>
          <label>
            <span className="field-label">{t('pref.end')}</span>
            <input className="field" type="time" value={q.end} aria-invalid={problem === 'end'} onChange={(e) => setQuiet({ end: e.target.value })} />
          </label>
          <label>
            <span className="field-label">{t('pref.zone')}</span>
            <select className="field select" value={q.timezone} onChange={(e) => setQuiet({ timezone: e.target.value })}>
              {zones.map((z) => (
                <option key={z} value={z}>
                  {z}
                </option>
              ))}
            </select>
            {q.timezone !== localTimezone() && (
              <button type="button" className="row-link" onClick={() => setQuiet({ timezone: localTimezone() })}>
                {t('pref.useMine', { zone: localTimezone() })}
              </button>
            )}
          </label>
        </div>
        {problem && (
          <p className="field-error" role="alert">
            {t(`pref.problem.${problem}`)}
          </p>
        )}
        <p className="form-help">{t('pref.quietEffect')}</p>
      </div>

      {list(t('pref.sources'), t('pref.sourcesNote'), draft.mutedSources, source, setSource, 'mutedSources')}
      {list(t('pref.repos'), t('pref.reposNote'), draft.mutedRepositories, repo, setRepo, 'mutedRepositories')}

      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
      <div className="form-actions">
        <button type="submit" className="button button-primary" disabled={busy || problem !== null || !dirty}>
          {busy ? t('pref.saving') : t('pref.save')}
        </button>
        <button type="button" className="button" disabled={!dirty || busy} onClick={() => setDraft(saved)}>
          {t('pref.discard')}
        </button>
        {saved.updatedAt && (
          <span className="form-help" style={{ alignSelf: 'center' }}>
            {t('pref.updated', { when: new Date(saved.updatedAt).toLocaleString(tag) })}
          </span>
        )}
      </div>
    </form>
  )
}

/** The part of the preferences a person edits; the save time is not an edit. */
function strip(p: Preferences) {
  return { quietHours: p.quietHours, mutedSources: p.mutedSources, mutedRepositories: p.mutedRepositories }
}
