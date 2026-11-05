import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { ConfirmDialog } from '../design/ConfirmDialog'
import { useToast } from '../design/Toast'
import { LoadGate, useLoad } from '../features/manage/useLoad'
import { RuleForm } from '../features/manage/RuleForm'
import { useRulesClient } from '../features/manage/useRules'
import { useI18n } from '../lib/i18n/I18nContext'
import { MAX_RULES, EMPTY_DRAFT, describeWhen, draftOf, type Rule, type RuleDraft, type Sample, type TestResult } from '../lib/rules'

type Editing = { kind: 'new' } | { kind: 'edit'; rule: Rule } | null

/** Create, order, disable and dry-run the rules that decide how important an event is. */
export default function RulesPage() {
  const { t } = useI18n()
  const toast = useToast()
  const client = useRulesClient()
  const { state, reload } = useLoad((signal) => client.list(signal), [client])
  const [editing, setEditing] = useState<Editing>(null)
  const [deleting, setDeleting] = useState<Rule | null>(null)
  const [busy, setBusy] = useState(false)
  const [formError, setFormError] = useState('')

  const run = async (work: () => Promise<unknown>, done?: string): Promise<boolean> => {
    setBusy(true)
    try {
      await work()
      if (done) toast.push(done, 'success')
      reload()
      return true
    } catch (err) {
      const message = err instanceof Error ? err.message : ''
      toast.push(t('rules.failed', { message }), 'error')
      setFormError(message)
      return false
    } finally {
      setBusy(false)
    }
  }

  const save = async (draft: RuleDraft) => {
    setFormError('')
    const ok = await run(() => (editing?.kind === 'edit' ? client.update(editing.rule.id, draft) : client.create(draft)), t('rules.saved'))
    if (ok) setEditing(null)
  }

  const move = (rules: Rule[], index: number, delta: -1 | 1) => {
    const ids = rules.map((r) => r.id)
    ;[ids[index], ids[index + delta]] = [ids[index + delta], ids[index]]
    void run(() => client.reorder(ids))
  }

  return (
    <section className="mpage" aria-labelledby="rules-title">
      <div className="mpage-head">
        <div>
          <h1 className="page-title" id="rules-title">
            {t('nav.rules')}
          </h1>
          <p className="mpage-lede">{t('rules.lede')}</p>
        </div>
        <div className="mpage-actions">
          <Link className="button" to="/policies">
            {t('nav.policies')}
          </Link>
          <button type="button" className="button button-primary" onClick={() => { setFormError(''); setEditing({ kind: 'new' }) }}>
            {t('rules.add')}
          </button>
        </div>
      </div>

      {editing && (
        <RuleForm
          key={editing.kind === 'edit' ? editing.rule.id : 'new'}
          initial={editing.kind === 'edit' ? draftOf(editing.rule) : EMPTY_DRAFT}
          ownId={editing.kind === 'edit' ? editing.rule.id : undefined}
          others={state.data ?? []}
          busy={busy}
          serverError={formError}
          onSubmit={save}
          onCancel={() => setEditing(null)}
        />
      )}

      <LoadGate state={state} onRetry={reload}>
        {(rules) => (
          <>
            <div className="table-wrap">
              <table className="data-table">
                <caption className="sr-only">{t('rules.caption', { n: rules.length })}</caption>
                <thead>
                  <tr>
                    <th scope="col">#</th>
                    <th scope="col">{t('rules.name')}</th>
                    <th scope="col">{t('rules.when')}</th>
                    <th scope="col">{t('rules.then')}</th>
                    <th scope="col">{t('rules.enabled')}</th>
                    <th scope="col">{t('rules.actions')}</th>
                  </tr>
                </thead>
                <tbody>
                  {rules.map((rule, i) => {
                    const when = describeWhen(rule.when)
                    const prevLocked = i > 0 && rules[i - 1].locked
                    return (
                      <tr key={rule.id}>
                        <td>{i + 1}</td>
                        <td className="wrap">
                          <strong>{rule.name}</strong>{' '}
                          {rule.locked && <span className="badge-lock">{t('rules.locked')}</span>}
                          {rule.description && <div className="form-help">{rule.description}</div>}
                        </td>
                        <td className="wrap">{when.length ? when.join(' · ') : t('rules.anything')}</td>
                        <td className="wrap">
                          <span className="badge-pill" data-level={rule.then.attentionClass === 'critical' ? 'critical' : rule.then.attentionClass === 'background' ? 'low' : 'normal'}>
                            {t(`class.${rule.then.attentionClass}`)}
                          </span>
                          <div className="form-help">{rule.then.reason}</div>
                          {rule.then.adaptive && <div className="form-help">{t('rules.adaptiveTag')}</div>}
                        </td>
                        <td>
                          <label className="switch">
                            <input
                              type="checkbox"
                              checked={!rule.disabled}
                              disabled={rule.locked || busy}
                              aria-label={t('rules.toggle', { name: rule.name })}
                              onChange={(e) => void run(() => client.update(rule.id, { ...draftOf(rule), disabled: !e.target.checked }))}
                            />
                          </label>
                        </td>
                        <td>
                          {rule.locked ? (
                            <span className="form-help">{t('rules.lockedHint')}</span>
                          ) : (
                            <div className="mpage-actions">
                              <button type="button" className="button" disabled={busy || i === 0 || prevLocked} aria-label={t('rules.up', { name: rule.name })} onClick={() => move(rules, i, -1)}>
                                ↑
                              </button>
                              <button type="button" className="button" disabled={busy || i === rules.length - 1} aria-label={t('rules.down', { name: rule.name })} onClick={() => move(rules, i, 1)}>
                                ↓
                              </button>
                              <button type="button" className="button" onClick={() => { setFormError(''); setEditing({ kind: 'edit', rule }) }}>
                                {t('rules.editShort')}
                              </button>
                              <button type="button" className="button" onClick={() => setDeleting(rule)}>
                                {t('act.delete')}
                              </button>
                            </div>
                          )}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
            <p className="form-help">{t('rules.count', { n: rules.length, max: MAX_RULES })}</p>
          </>
        )}
      </LoadGate>

      <RuleTester />

      <ConfirmDialog
        open={deleting !== null}
        title={t('rules.delete.title', { name: deleting?.name ?? '' })}
        body={t('rules.delete.body')}
        confirmLabel={t('act.delete')}
        onConfirm={() => {
          const rule = deleting
          setDeleting(null)
          if (rule) void run(() => client.remove(rule.id), t('rules.deleted'))
        }}
        onCancel={() => setDeleting(null)}
      />
    </section>
  )
}

/** Dry-runs a sample event through the current rules. Nothing is stored or sent. */
function RuleTester() {
  const { t } = useI18n()
  const client = useRulesClient()
  const [sample, setSample] = useState<Sample>({ source: 'github', eventType: 'pull_request', repository: 'acme/api' })
  const [result, setResult] = useState<TestResult | null>(null)
  const [error, setError] = useState('')

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')
    try {
      setResult(await client.test(sample))
    } catch (err) {
      setResult(null)
      setError(err instanceof Error ? err.message : '')
    }
  }
  const field = (key: keyof Sample, label: string) => (
    <label>
      <span className="field-label">{label}</span>
      <input className="field" value={sample[key]} onChange={(e) => setSample({ ...sample, [key]: e.target.value })} />
    </label>
  )

  return (
    <form className="panel" onSubmit={submit} aria-labelledby="tester-title">
      <h2 className="panel-title" id="tester-title">
        {t('rules.test.title')}
      </h2>
      <p className="panel-note">{t('rules.test.note')}</p>
      <div className="form-grid">
        {field('source', t('all.source'))}
        {field('eventType', t('all.event'))}
        {field('repository', t('all.repository'))}
      </div>
      <div className="form-actions">
        <button type="submit" className="button button-primary">
          {t('rules.test.run')}
        </button>
      </div>
      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
      {result && (
        <div role="status" style={{ marginBlockStart: '1rem' }}>
          <p>
            <strong>{t(`class.${result.attentionClass}`)}</strong>
            {' · '}
            {result.matched ? t('rules.test.matched', { name: result.matchedRule ?? '' }) : t('rules.test.none')}
          </p>
          <p className="form-help">{result.reason}</p>
          {result.aiBypassed && <p className="form-help">{t('rules.test.bypass')}</p>}
          <ol className="bars" aria-label={t('rules.test.steps')}>
            {result.steps.map((s) => (
              <li key={s.id} className="bar-row" style={{ gridTemplateColumns: '1fr auto' }}>
                <span>{s.name}</span>
                <span className="bar-value">{s.winner ? t('rules.test.winner') : s.disabled ? t('rules.test.disabled') : s.matched ? t('rules.test.alsoMatched') : t('rules.test.skipped')}</span>
              </li>
            ))}
          </ol>
        </div>
      )}
    </form>
  )
}
