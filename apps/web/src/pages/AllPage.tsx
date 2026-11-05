import { useEffect, useMemo, useState } from 'react'
import { useApp } from '../app/AppState'
import { Pager } from '../components/Pager'
import { PriorityBadge, SourceBadge, StatusBadge } from '../features/inbox/badges'
import { LoadGate, useLoad } from '../features/manage/useLoad'
import {
  ALL_PAGE_SIZES,
  DEFAULT_ALL_FILTERS,
  nextSort,
  rangeError,
  sortDirection,
  toAllQuery,
  toCsv,
  type AllFilters,
} from '../lib/allQuery'
import { eventTitle, relativeTime } from '../lib/events'
import { useI18n } from '../lib/i18n/I18nContext'
import type { InboxItem, Level, Outcome } from '../lib/inbox'
import { LEVELS } from '../lib/priority'

const OUTCOMES: Outcome[] = ['delivered', 'held', 'suppressed', 'merged', 'recorded']
const SEARCH_DEBOUNCE_MS = 250

function toggle<T>(list: T[], value: T): T[] {
  return list.includes(value) ? list.filter((v) => v !== value) : [...list, value]
}

function download(name: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/csv;charset=utf-8' }))
  const link = document.createElement('a')
  link.href = url
  link.download = name
  link.click()
  URL.revokeObjectURL(url)
}

/** Every notification in a sortable, filterable table. Clicking a row opens the same detail drawer as the inbox. */
export default function AllPage() {
  const { t, tag } = useI18n()
  const { client, now, selectEvent, selectedId } = useApp()
  const [filters, setFilters] = useState<AllFilters>(DEFAULT_ALL_FILTERS)
  const [pageNumber, setPageNumber] = useState(1)
  const [text, setText] = useState('')

  useEffect(() => {
    const id = window.setTimeout(() => setText(filters.text), SEARCH_DEBOUNCE_MS)
    return () => window.clearTimeout(id)
  }, [filters.text])

  const change = (patch: Partial<AllFilters>) => {
    setFilters((f) => ({ ...f, ...patch }))
    setPageNumber(1)
  }
  const invalidRange = rangeError(filters)

  const { priorities, outcomes, source, repository, from, to, sort, pageSize, includeArchived } = filters
  const { state, reload } = useLoad(
    (signal) => client.list(toAllQuery({ ...filters, text }, pageNumber, text), signal),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [client, text, priorities, outcomes, source, repository, from, to, sort, pageSize, includeArchived, pageNumber, invalidRange],
    15_000,
  )

  const sortHeader = (column: 'time' | 'priority', label: string) => (
    <th scope="col" aria-sort={sortDirection(sort, column)}>
      <button type="button" className="sort-button" data-active={sortDirection(sort, column) !== 'none'} onClick={() => change({ sort: nextSort(sort, column) })}>
        {label}
        <span aria-hidden="true">{sortDirection(sort, column) === 'ascending' ? '↑' : sortDirection(sort, column) === 'descending' ? '↓' : '↕'}</span>
      </button>
    </th>
  )

  const filtered = useMemo(
    () => text !== '' || priorities.length > 0 || outcomes.length > 0 || source !== '' || repository !== '' || from !== '' || to !== '' || includeArchived,
    [text, priorities, outcomes, source, repository, from, to, includeArchived],
  )

  return (
    <section className="mpage" aria-labelledby="all-title">
      <div className="mpage-head">
        <div>
          <h1 className="page-title" id="all-title">
            {t('nav.all')}
          </h1>
          <p className="mpage-lede">{t('all.lede')}</p>
        </div>
      </div>

      <div className="panel">
        <div className="toolbar">
          <label className="toolbar-field">
            <span>{t('all.search')}</span>
            <input className="field wide" type="search" value={filters.text} placeholder={t('all.searchHint')} onChange={(e) => change({ text: e.target.value })} />
          </label>
          <label className="toolbar-field">
            <span>{t('all.source')}</span>
            <input className="field" value={filters.source} placeholder="github" onChange={(e) => change({ source: e.target.value })} />
          </label>
          <label className="toolbar-field">
            <span>{t('all.repository')}</span>
            <input className="field" value={filters.repository} placeholder="acme/api" onChange={(e) => change({ repository: e.target.value })} />
          </label>
          <label className="toolbar-field">
            <span>{t('all.from')}</span>
            <input className="field" type="date" value={filters.from} aria-invalid={invalidRange} onChange={(e) => change({ from: e.target.value })} />
          </label>
          <label className="toolbar-field">
            <span>{t('all.to')}</span>
            <input className="field" type="date" value={filters.to} aria-invalid={invalidRange} onChange={(e) => change({ to: e.target.value })} />
          </label>
        </div>
        {invalidRange && (
          <p className="field-error" role="alert">
            {t('all.rangeError')}
          </p>
        )}
        <div className="toolbar" style={{ marginBlockStart: '0.75rem' }}>
          <fieldset className="chip-group">
            <legend className="toolbar-legend">{t('all.priority')}</legend>
            {LEVELS.map((level: Level) => (
              <button key={level} type="button" className="chip-toggle" aria-pressed={priorities.includes(level)} onClick={() => change({ priorities: toggle(priorities, level) })}>
                {t(`level.${level}`)}
              </button>
            ))}
          </fieldset>
          <fieldset className="chip-group">
            <legend className="toolbar-legend">{t('all.outcome')}</legend>
            {OUTCOMES.map((outcome) => (
              <button key={outcome} type="button" className="chip-toggle" aria-pressed={outcomes.includes(outcome)} onClick={() => change({ outcomes: toggle(outcomes, outcome) })}>
                {t(`outcome.${outcome}`)}
              </button>
            ))}
          </fieldset>
          <label className="switch">
            <input type="checkbox" checked={includeArchived} onChange={(e) => change({ includeArchived: e.target.checked })} />
            {t('all.archived')}
          </label>
          {filtered && (
            <button type="button" className="button" onClick={() => change({ ...DEFAULT_ALL_FILTERS, sort, pageSize })}>
              {t('list.clear')}
            </button>
          )}
        </div>
      </div>

      <LoadGate state={state} onRetry={reload}>
        {(page) => (
          <>
            {page.items.length === 0 ? (
              <p className="ledger-empty">{filtered ? t('list.empty') : t('all.none')}</p>
            ) : (
              <div className="table-wrap">
                <table className="data-table" aria-busy={state.status === 'loading'}>
                  <caption className="sr-only">{t('all.caption', { total: page.total })}</caption>
                  <thead>
                    <tr>
                      {sortHeader('priority', t('all.priority'))}
                      <th scope="col">{t('all.event')}</th>
                      <th scope="col">{t('all.source')}</th>
                      <th scope="col">{t('all.outcome')}</th>
                      <th scope="col">{t('all.reason')}</th>
                      {sortHeader('time', t('all.received'))}
                    </tr>
                  </thead>
                  <tbody>
                    {page.items.map((item: InboxItem) => (
                      <tr key={item.id} data-clickable data-unread={item.read ? undefined : ''} aria-selected={selectedId === item.id}>
                        <td>
                          <PriorityBadge level={item.priority} />
                        </td>
                        <td>
                          <button type="button" className="row-link" onClick={() => selectEvent(item.id)}>
                            {eventTitle(item, t)}
                          </button>
                          {item.repeatCount > 0 && <span className="form-help"> {t('all.repeats', { n: item.repeatCount })}</span>}
                        </td>
                        <td>
                          <SourceBadge source={item.source} />
                        </td>
                        <td>
                          <StatusBadge outcome={item.outcome} />
                        </td>
                        <td className="wrap">{item.decisionState?.reason ?? ''}</td>
                        <td title={new Date(item.createdAt).toLocaleString(tag)}>{relativeTime(item.createdAt, now, tag, t)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            <div className="table-foot">
              <p className="form-help">{t('all.total', { total: page.total })}</p>
              <div className="toolbar">
                <label className="toolbar-field">
                  <span>{t('all.pageSize')}</span>
                  <select className="field select" value={pageSize} onChange={(e) => change({ pageSize: Number(e.target.value) })}>
                    {ALL_PAGE_SIZES.map((n) => (
                      <option key={n} value={n}>
                        {n}
                      </option>
                    ))}
                  </select>
                </label>
                <button type="button" className="button" disabled={page.items.length === 0} onClick={() => download('notifications.csv', toCsv(page.items))}>
                  {t('all.export')}
                </button>
              </div>
            </div>
            <Pager page={page.page} pages={Math.max(1, Math.ceil(page.total / page.pageSize))} onPage={setPageNumber} />
          </>
        )}
      </LoadGate>
    </section>
  )
}
