import { useCallback, useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useApp } from '../app/AppState'
import { EmptyNotice, ErrorNotice } from '../components/Notices'
import { Lead } from '../components/Lead'
import { Pager } from '../components/Pager'
import { QuickStats } from '../features/dashboard/QuickStats'
import { RecentActivity } from '../features/dashboard/RecentActivity'
import { InboxHero } from '../features/dashboard/InboxHero'
import { StatCards } from '../features/dashboard/StatCards'
import { ConfirmDialog } from '../design/ConfirmDialog'
import { SkeletonRows } from '../design/Skeleton'
import { useToast } from '../design/Toast'
import { BulkBar } from '../features/inbox/BulkBar'
import { copyNotificationLink } from '../features/inbox/link'
import { NotificationRow } from '../features/inbox/NotificationRow'
import { NotificationTabs } from '../features/inbox/NotificationTabs'
import { useInbox } from '../features/inbox/useInbox'
import { useI18n } from '../lib/i18n/I18nContext'
import type { Action } from '../lib/inbox'
import { clearFilters, isFiltered, toggleTab } from '../lib/inboxQuery'
import { bucketCounts } from '../lib/sparkline'
import { LEVELS } from '../lib/priority'
import type { Level } from '../lib/inbox'

/** The inbox: filter tabs, a list with selection and bulk actions, and paging. The drawer is hosted by the shell. */
export default function InboxPage() {
  const { t } = useI18n()
  const toast = useToast()
  const app = useApp()
  const { filters, setFilters, summary, now, selectedId } = app
  const [pageNumber, setPageNumber] = useState(1)
  const [selection, setSelection] = useState<Set<string>>(new Set())
  const [confirming, setConfirming] = useState<string[] | null>(null)
  const [params, setParams] = useSearchParams()
  const { state, run } = useInbox(filters, pageNumber)
  const page = state.page

  // A new search, filter or sort starts again from the first page with nothing selected.
  useEffect(() => {
    setPageNumber(1)
    setSelection(new Set())
  }, [filters])

  // A shared link (#/inbox?open=<id>) opens that notification once.
  const openParam = params.get('open')
  useEffect(() => {
    if (!openParam) return
    app.selectEvent(openParam)
    setParams({}, { replace: true })
    // selectEvent is stable per connection; reacting to the parameter alone is intended.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [openParam])

  const ids = useMemo(() => page?.items.map((i) => i.id) ?? [], [page])
  const allChecked = ids.length > 0 && ids.every((id) => selection.has(id))

  const toggle = useCallback((id: string) => {
    setSelection((prev) => {
      const next = new Set(prev)
      if (!next.delete(id)) next.add(id)
      return next
    })
  }, [])

  const act = useCallback((targets: string[], action: Action) => void run(targets, action), [run])

  const bulk = (action: Action) => {
    const targets = [...selection]
    setSelection(new Set())
    act(targets, action)
  }

  const copyLink = useCallback(
    async (id: string) => {
      if (await copyNotificationLink(id)) toast.push(t('toast.linkCopied'), 'success')
      else toast.push(t('toast.copyFailed'), 'error')
    },
    [t, toast],
  )

  const markAll = async () => {
    try {
      await app.markEverythingRead()
      toast.push(t('toast.allRead'), 'success')
    } catch (err) {
      toast.push(t('inbox.failed', { message: err instanceof Error ? err.message : '' }), 'error')
    }
  }

  const confirmDelete = () => {
    const targets = confirming ?? []
    setConfirming(null)
    setSelection(new Set())
    act(targets, { type: 'delete' })
  }

  const events = app.feed.events
  const series = useMemo(() => {
    const out = {} as Record<Level, number[]>
    for (const level of LEVELS) out[level] = bucketCounts(events, now, filters.range, level)
    return out
  }, [events, now, filters.range])
  const filtered = isFiltered(filters)
  const totalNone = page !== null && page.total === 0

  return (
    <>
      {app.demo && (
        <p className="demo-banner" role="note">
          {t('demo.banner')}{' '}
          <button type="button" className="text-button" onClick={app.openSettings}>
            {t('welcome.connect')}
          </button>
        </p>
      )}
      <InboxHero unread={summary.unread} critical={page?.counts.critical ?? 0} onMarkAllRead={markAll} />
      <StatCards counts={page?.counts} series={series} active={filters.priorities} onToggle={(level) => setFilters(toggleTab(filters, level))} />

      {state.status === 'error' && (
        <ErrorNotice error={state.error} settings={app.settings} onOpenSettings={app.openSettings} onRetry={app.invalidate} />
      )}

      <div className="inbox-grid">
      <section className="card list-card" aria-label={t('inbox.list')}>
      <NotificationTabs filters={filters} counts={page?.counts} onChange={setFilters} onClear={() => setFilters(clearFilters(filters))} />

      {selection.size > 0 && (
        <BulkBar
          count={selection.size}
          archivedView={filters.archived}
          onAction={bulk}
          onDelete={() => setConfirming([...selection])}
          onClear={() => setSelection(new Set())}
        />
      )}

      {events.length > 0 && !filtered && <Lead events={events} now={now} onOpen={app.selectEvent} />}

      {page === null && state.status !== 'error' && (
        <div role="status" aria-label={t('inbox.loadingList')}>
          <SkeletonRows />
        </div>
      )}

      {page !== null && page.items.length > 0 && (
        <>
          <label className="select-all">
            <input
              type="checkbox"
              checked={allChecked}
              onChange={() => setSelection(allChecked ? new Set() : new Set(ids))}
            />
            {t('inbox.selectAll')}
          </label>
          <ul className="nlist" aria-label={t('inbox.page', { page: page.page })} aria-busy={state.status === 'loading'}>
            {page.items.map((item) => (
              <NotificationRow
                key={item.id}
                item={item}
                now={now}
                active={selectedId === item.id}
                checked={selection.has(item.id)}
                archivedView={filters.archived}
                onCheck={toggle}
                onOpen={app.selectEvent}
                onAction={(id, action) => act([id], action)}
                onDelete={(id) => setConfirming([id])}
                onCopyLink={copyLink}
              />
            ))}
          </ul>
          <Pager page={page.page} pages={Math.max(1, Math.ceil(page.total / page.pageSize))} onPage={setPageNumber} />
        </>
      )}

      {totalNone && filtered && (
        <div className="ledger-empty">
          <p>{t('list.empty')}</p>
          <button type="button" className="button" onClick={() => setFilters(clearFilters(filters))}>
            {t('list.clear')}
          </button>
        </div>
      )}
      {totalNone && !filtered && state.status === 'ready' && (events.length === 0 ? <EmptyNotice settings={app.settings} /> : <p className="ledger-empty">{t('inbox.caughtUp')}</p>)}

      </section>
      <aside className="inbox-rail" aria-label={t('quick.title')}>
        <QuickStats counts={page?.counts} />
        <RecentActivity events={events} now={now} onOpen={app.selectEvent} onViewAll={() => setFilters(clearFilters(filters))} />
      </aside>
      </div>

      <ConfirmDialog
        open={confirming !== null}
        title={t('confirm.delete.title')}
        body={t('confirm.delete.body')}
        confirmLabel={t('act.delete')}
        onConfirm={confirmDelete}
        onCancel={() => setConfirming(null)}
      />
    </>
  )
}
