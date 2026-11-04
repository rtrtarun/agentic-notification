import { useEffect, useMemo, useState } from 'react'
import { useApp } from '../app/AppState'
import { Flag } from '../components/Flag'
import { Lead } from '../components/Lead'
import { Ledger } from '../components/Ledger'
import { EmptyNotice, ErrorNotice } from '../components/Notices'
import { Pager } from '../components/Pager'
import { useToast } from '../design/Toast'
import { groupThreads } from '../lib/events'
import { countByView, filterEvents, isFiltered, paginate, type View } from '../lib/filters'
import { useI18n } from '../lib/i18n/I18nContext'

const VIEWS: View[] = ['all', 'critical', 'standard', 'background']

/** The notification list. Milestone 15 keeps the current list and moves it into the shell. */
export default function InboxPage() {
  const { t } = useI18n()
  const toast = useToast()
  const app = useApp()
  const { feed, filters, now, read, unread, settings, demo } = app
  const [pageNumber, setPageNumber] = useState(1)

  // A new search or filter starts again from the first page.
  useEffect(() => setPageNumber(1), [filters])

  const events = feed.events
  const counts = useMemo(() => countByView(events), [events])
  const threads = useMemo(() => groupThreads(filterEvents(events, filters, now, t)), [events, filters, now, t])
  const page = useMemo(() => paginate(threads, pageNumber), [threads, pageNumber])

  const hasEvents = events.length > 0
  const failing = feed.status === 'error'
  const showEmpty = feed.status === 'ready' && !hasEvents

  const markAll = () => {
    app.markEverythingRead()
    toast.push(t('toast.allRead'), 'success')
  }

  return (
    <>
      <div className="page-head">
        <h1 className="page-title">
          {t('page.title')}
          {unread.length > 0 && <span className="count-badge">{unread.length}</span>}
        </h1>
        {hasEvents && (
          <button type="button" className="text-button" disabled={unread.length === 0} onClick={markAll}>
            {t('list.markAll')}
          </button>
        )}
      </div>

      {demo && (
        <p className="demo-banner" role="note">
          {t('demo.banner')}{' '}
          <button type="button" className="text-button" onClick={app.openSettings}>
            {t('welcome.connect')}
          </button>
        </p>
      )}
      {failing && <ErrorNotice error={feed.error} settings={settings} onOpenSettings={app.openSettings} onRetry={app.reload} />}

      {hasEvents && (
        <>
          <div className="view-tabs" role="group" aria-label={t('nav.views')}>
            {VIEWS.map((v) => (
              <button
                key={v}
                type="button"
                className="view-tab"
                aria-pressed={filters.view === v}
                onClick={() => app.changeFilters({ view: v })}
              >
                {v !== 'all' && <Flag kind={v} decorative />}
                {t(`view.${v}`)}
                <span className="view-tab-count">{counts[v]}</span>
              </button>
            ))}
          </div>
          <Lead events={events} now={now} onOpen={app.selectEvent} />
          <p className="list-help">{t('list.help')}</p>
          <Ledger
            threads={page.items}
            now={now}
            selectedId={app.selectedId}
            isRead={(id) => read.has(id)}
            onSelect={app.selectEvent}
            filtered={isFiltered(filters)}
            onClear={app.clearFilters}
          />
          <Pager page={page.page} pages={page.pages} onPage={setPageNumber} />
        </>
      )}
      {showEmpty && <EmptyNotice settings={settings} />}
      {feed.status === 'loading' && !hasEvents && (
        <p className="loading" role="status">
          {t('loading')}
        </p>
      )}
    </>
  )
}
