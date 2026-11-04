import { useI18n } from '../../lib/i18n/I18nContext'
import { BellArt } from './BellArt'

interface InboxHeroProps {
  unread: number
  critical: number
  onMarkAllRead: () => void
}

/** The page header: what this screen is, whether anything needs you, and the one bulk action. */
export function InboxHero({ unread, critical, onMarkAllRead }: InboxHeroProps) {
  const { t } = useI18n()
  const clear = unread === 0
  return (
    <section className="hero" aria-labelledby="inbox-title">
      <div className="hero-copy">
        <h1 className="hero-title" id="inbox-title">
          {t('page.title')}
        </h1>
        <p className="hero-sub">{t('hero.subtitle')}</p>
        <div className="hero-row">
          <p className="hero-pill" data-state={clear ? 'clear' : 'busy'} role="status">
            <span className="hero-dot" aria-hidden="true" />
            {clear ? (
              <>
                <strong>{t('hero.upToDate')}</strong> {t('hero.upToDateBody')}
              </>
            ) : (
              <>
                <strong>{t('hero.unread', { n: unread })}</strong>
                {critical > 0 && <> {t('hero.critical', { n: critical })}</>}
              </>
            )}
          </p>
          <button type="button" className="hero-button" disabled={clear} onClick={onMarkAllRead}>
            {t('list.markAll')} <span className="arrow" aria-hidden="true">→</span>
          </button>
        </div>
      </div>
      <BellArt />
    </section>
  )
}
