import { useCallback, useEffect, useRef, useState } from 'react'
import { useApp } from '../../app/AppState'
import { useToast } from '../../design/Toast'
import { ApiError } from '../../lib/api'
import { useI18n } from '../../lib/i18n/I18nContext'
import type { Action, InboxPage } from '../../lib/inbox'
import { toQuery, type InboxFilters } from '../../lib/inboxQuery'
import { applyOptimistic } from './optimistic'

export type InboxState =
  | { status: 'loading'; page: InboxPage | null }
  | { status: 'ready'; page: InboxPage }
  | { status: 'error'; page: InboxPage | null; error: ApiError }

const REFRESH_MS = 15_000
const SEARCH_DEBOUNCE_MS = 250

function useDebounced<T>(value: T, ms: number): T {
  const [settled, setSettled] = useState(value)
  useEffect(() => {
    const id = window.setTimeout(() => setSettled(value), ms)
    return () => window.clearTimeout(id)
  }, [value, ms])
  return settled
}

/**
 * Loads one page of the inbox for the current filters and keeps it fresh.
 * `run` applies an action: the list changes at once and is put back if the
 * service refuses.
 */
export function useInbox(filters: InboxFilters, pageNumber: number) {
  const { client, version, invalidate } = useApp()
  const { t } = useI18n()
  const toast = useToast()
  const [state, setState] = useState<InboxState>({ status: 'loading', page: null })
  const text = useDebounced(filters.query, SEARCH_DEBOUNCE_MS)
  const latest = useRef(state)
  latest.current = state

  const { priorities, outcomes, unread, archived, sort, range } = filters
  useEffect(() => {
    let cancelled = false
    let controller = new AbortController()
    const query = () => toQuery({ query: '', range, priorities, outcomes, unread, archived, sort }, new Date(), pageNumber, text)

    const load = async () => {
      controller.abort()
      controller = new AbortController()
      try {
        const page = await client.list(query(), controller.signal)
        if (!cancelled) setState({ status: 'ready', page })
      } catch (err) {
        if (cancelled || (err instanceof DOMException && err.name === 'AbortError')) return
        const error = err instanceof ApiError ? err : new ApiError('server', 'The service hit an error.')
        // Keep the last page on screen so a brief outage does not blank the list.
        setState((previous) => ({ status: 'error', page: previous.page, error }))
      }
    }

    setState((previous) => ({ status: 'loading', page: previous.page }))
    void load()
    const timer = window.setInterval(() => {
      if (!document.hidden) void load()
    }, REFRESH_MS)
    return () => {
      cancelled = true
      controller.abort()
      window.clearInterval(timer)
    }
  }, [client, version, text, priorities, outcomes, unread, archived, sort, range, pageNumber])

  const run = useCallback(
    async (ids: string[], action: Action) => {
      if (ids.length === 0) return
      const before = latest.current.page
      if (before) setState((s) => (s.page ? { ...s, page: applyOptimistic(s.page, ids, action, filters) } : s))
      try {
        if (ids.length === 1) {
          await client.act(ids[0], action)
          if (action.type !== 'read' && action.type !== 'unread') toast.push(t('inbox.updated', { n: 1 }), 'success')
        } else {
          const results = await client.bulk(ids, action)
          const failed = results.filter((r) => !r.ok).length
          if (failed > 0) toast.push(t('inbox.partial', { ok: results.length - failed, failed }), 'error')
          else toast.push(t('inbox.updated', { n: ids.length }), 'success')
        }
        invalidate()
      } catch (err) {
        if (before) setState((s) => ({ ...s, page: before }))
        const message = err instanceof Error ? err.message : ''
        toast.push(t('inbox.failed', { message }), 'error')
      }
    },
    [client, filters, invalidate, t, toast],
  )

  return { state, run }
}
