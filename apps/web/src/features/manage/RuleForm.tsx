import { useState, type FormEvent } from 'react'
import { useI18n } from '../../lib/i18n/I18nContext'
import type { AttentionClass } from '../../lib/api'
import { draftProblem, type Rule, type RuleDraft } from '../../lib/rules'

const CLASSES: AttentionClass[] = ['critical', 'standard', 'background']

interface RuleFormProps {
  initial: RuleDraft
  /** The id of the rule being edited, so it may keep its own name. */
  ownId?: string
  others: Rule[]
  busy: boolean
  /** Error text from the service, shown under the form. */
  serverError: string
  onSubmit: (draft: RuleDraft) => void
  onCancel: () => void
}

/** Create or edit a rule. The service validates again; this answers early. */
export function RuleForm({ initial, ownId, others, busy, serverError, onSubmit, onCancel }: RuleFormProps) {
  const { t } = useI18n()
  const [draft, setDraft] = useState<RuleDraft>(initial)
  const [touched, setTouched] = useState(false)
  const problem = draftProblem(draft, others, ownId)
  const set = (patch: Partial<RuleDraft>) => setDraft((d) => ({ ...d, ...patch }))
  const when = (patch: Partial<RuleDraft['when']>) => setDraft((d) => ({ ...d, when: { ...d.when, ...patch } }))

  const submit = (e: FormEvent) => {
    e.preventDefault()
    setTouched(true)
    if (!problem) onSubmit(draft)
  }

  return (
    <form className="panel" onSubmit={submit} aria-labelledby="rule-form-title" noValidate>
      <h2 className="panel-title" id="rule-form-title">
        {ownId ? t('rules.edit') : t('rules.new')}
      </h2>
      <div className="form-grid">
        <label className="span-all">
          <span className="field-label">{t('rules.name')}</span>
          <input className="field" value={draft.name} aria-invalid={touched && problem !== null} onChange={(e) => set({ name: e.target.value })} autoFocus />
          {touched && problem && <span className="field-error" role="alert">{t(`rules.problem.${problem}`)}</span>}
        </label>
        <label className="span-all">
          <span className="field-label">{t('rules.description')}</span>
          <input className="field" value={draft.description ?? ''} onChange={(e) => set({ description: e.target.value })} />
        </label>
        <label>
          <span className="field-label">{t('all.source')}</span>
          <input className="field" value={draft.when.source ?? ''} placeholder="github" onChange={(e) => when({ source: e.target.value })} />
        </label>
        <label>
          <span className="field-label">{t('all.event')}</span>
          <input className="field" value={draft.when.eventType ?? ''} placeholder="pull_request" onChange={(e) => when({ eventType: e.target.value })} />
        </label>
        <label>
          <span className="field-label">{t('all.repository')}</span>
          <input className="field" value={draft.when.repository ?? ''} placeholder="acme/*" onChange={(e) => when({ repository: e.target.value })} />
        </label>
        <label>
          <span className="field-label">{t('rules.class')}</span>
          <select className="field select" value={draft.then.attentionClass} onChange={(e) => set({ then: { ...draft.then, attentionClass: e.target.value as AttentionClass } })}>
            {CLASSES.map((c) => (
              <option key={c} value={c}>
                {t(`class.${c}`)}
              </option>
            ))}
          </select>
        </label>
        <label className="span-all">
          <span className="field-label">{t('rules.reason')}</span>
          <input className="field" value={draft.then.reason} placeholder={t('rules.reasonHint')} onChange={(e) => set({ then: { ...draft.then, reason: e.target.value } })} />
        </label>
      </div>
      <p className="form-help">{t('rules.patternHelp')}</p>
      {draft.then.attentionClass === 'critical' && <p className="form-help">{t('rules.criticalNote')}</p>}
      {serverError && (
        <p className="field-error" role="alert">
          {serverError}
        </p>
      )}
      <div className="form-actions">
        <button type="submit" className="button button-primary" disabled={busy}>
          {t('rules.save')}
        </button>
        <button type="button" className="button" onClick={onCancel}>
          {t('confirm.cancel')}
        </button>
      </div>
    </form>
  )
}
