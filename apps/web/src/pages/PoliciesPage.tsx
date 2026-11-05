import { Link } from 'react-router-dom'
import { StatTile } from '../features/manage/charts'
import { LoadGate, useLoad } from '../features/manage/useLoad'
import { useRulesClient } from '../features/manage/useRules'
import { useI18n } from '../lib/i18n/I18nContext'
import { shortDuration } from '../lib/insights'

/** The effective policy, read only: what can never change, and what each class gets. */
export default function PoliciesPage() {
  const { t } = useI18n()
  const client = useRulesClient()
  const { state, reload } = useLoad((signal) => client.policies(signal), [client])

  return (
    <section className="mpage" aria-labelledby="pol-title">
      <div className="mpage-head">
        <div>
          <h1 className="page-title" id="pol-title">
            {t('nav.policies')}
          </h1>
          <p className="mpage-lede">{t('pol.lede')}</p>
        </div>
        <Link className="button" to="/rules">
          {t('nav.rules')}
        </Link>
      </div>

      <LoadGate state={state} onRetry={reload}>
        {(p) => (
          <>
            <div className="stat-grid">
              <StatTile label={t('pol.rules')} value={p.ruleCount} hint={t('pol.disabled', { n: p.disabledRuleCount })} />
              <StatTile label={t('pol.window')} value={shortDuration(p.dedupWindowSeconds)} hint={t('pol.windowHint')} />
              <StatTile label={t('pol.retention')} value={p.retentionHours > 0 ? shortDuration(p.retentionHours * 3600) : t('pol.forever')} hint={t('pol.retentionHint')} />
            </div>

            <div className="panel">
              <h2 className="panel-title">{t('pol.guardrails')}</h2>
              <p className="panel-note">{t('pol.guardrailsNote')}</p>
              <ul className="card-list" aria-label={t('pol.guardrails')}>
                {p.guardrails.map((g) => (
                  <li key={g.id} className="item-card">
                    <h3>
                      <span className="badge-lock">{t('rules.locked')}</span> {g.title}
                    </h3>
                    <p className="panel-note">{g.description}</p>
                  </li>
                ))}
              </ul>
            </div>

            <div className="panel">
              <h2 className="panel-title">{t('pol.classes')}</h2>
              <div className="table-wrap">
                <table className="data-table">
                  <caption className="sr-only">{t('pol.classes')}</caption>
                  <thead>
                    <tr>
                      <th scope="col">{t('rules.class')}</th>
                      <th scope="col">{t('all.priority')}</th>
                      <th scope="col">{t('pol.delivery')}</th>
                      <th scope="col">{t('pol.dedup')}</th>
                      <th scope="col">{t('pol.ai')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {p.classes.map((c) => (
                      <tr key={c.class}>
                        <td className="wrap">
                          <strong>{t(`class.${c.class}`)}</strong>
                          <div className="form-help">{c.description}</div>
                        </td>
                        <td>{c.level}</td>
                        <td className="wrap">{c.delivery}</td>
                        <td className="wrap">{c.dedup}</td>
                        <td>{c.aiEligible ? t('pol.aiYes') : t('pol.aiNo')}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>

            <div className="panel">
              <h2 className="panel-title">{t('pol.locked')}</h2>
              {p.lockedRules.length === 0 ? (
                <p className="panel-note">{t('pol.noLocked')}</p>
              ) : (
                <ul>
                  {p.lockedRules.map((name) => (
                    <li key={name}>{name}</li>
                  ))}
                </ul>
              )}
            </div>
          </>
        )}
      </LoadGate>
    </section>
  )
}
