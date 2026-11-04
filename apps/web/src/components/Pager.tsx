import { useI18n } from '../lib/i18n/I18nContext'

interface PagerProps {
  page: number
  pages: number
  onPage: (page: number) => void
}

/** Previous and next with a plain "Page 2 of 5" between them. Hidden when there is one page. */
export function Pager({ page, pages, onPage }: PagerProps) {
  const { t } = useI18n()
  if (pages <= 1) return null
  return (
    <nav className="pager" aria-label={t('pager.label')}>
      <button type="button" className="button" disabled={page <= 1} onClick={() => onPage(page - 1)}>
        {t('pager.prev')}
      </button>
      <p className="pager-status" aria-live="polite">
        {t('pager.page', { page, pages })}
      </p>
      <button type="button" className="button" disabled={page >= pages} onClick={() => onPage(page + 1)}>
        {t('pager.next')}
      </button>
    </nav>
  )
}
