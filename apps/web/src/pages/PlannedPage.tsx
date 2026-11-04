import { Link } from 'react-router-dom'
import { useI18n } from '../lib/i18n/I18nContext'
import type { NavItem } from '../lib/nav'

/**
 * A screen that is on the roadmap but not built. It says so plainly: no sample
 * numbers, no disabled controls pretending to work.
 */
export default function PlannedPage({ item }: { item: NavItem }) {
  const { t } = useI18n()
  const name = t(item.label)
  return (
    <section className="planned" aria-labelledby="planned-title">
      <h1 className="page-title" id="planned-title">
        {name}
      </h1>
      <div className="notice">
        <h2 className="notice-title">{t('planned.title', { name })}</h2>
        <p className="prose">{t('planned.body', { milestone: item.plannedIn ?? 0 })}</p>
        <Link className="button" to="/inbox">
          {t('planned.back')}
        </Link>
      </div>
    </section>
  )
}
