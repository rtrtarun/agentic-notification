import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { useApp } from '../../app/AppState'
import { ErrorNotice } from '../../components/Notices'
import { SkeletonRows } from '../../design/Skeleton'
import { ApiError } from '../../lib/api'
import { useI18n } from '../../lib/i18n/I18nContext'

export type Loaded<T> =
  | { status: 'loading'; data: T | null }
  | { status: 'ready'; data: T }
  | { status: 'error'; data: T | null; error: ApiError }

/**
 * Loads data for a management screen. It reloads when `deps` change and when the
 * app signals an update, keeps the last data on screen through a failed reload,
 * and cancels a request that has been overtaken.
 */
export function useLoad<T>(fetcher: (signal: AbortSignal) => Promise<T>, deps: unknown[], refreshMs = 0) {
  const { version } = useApp()
  const [state, setState] = useState<Loaded<T>>({ status: 'loading', data: null })
  const [nonce, setNonce] = useState(0)
  const fetchRef = useRef(fetcher)
  fetchRef.current = fetcher

  useEffect(() => {
    let cancelled = false
    let controller = new AbortController()
    const load = async () => {
      controller.abort()
      controller = new AbortController()
      try {
        const data = await fetchRef.current(controller.signal)
        if (!cancelled) setState({ status: 'ready', data })
      } catch (err) {
        if (cancelled || (err instanceof DOMException && err.name === 'AbortError')) return
        const error = err instanceof ApiError ? err : new ApiError('server', 'The service hit an error.')
        setState((previous) => ({ status: 'error', data: previous.data, error }))
      }
    }
    setState((previous) => ({ status: 'loading', data: previous.data }))
    void load()
    const timer = refreshMs > 0 ? window.setInterval(() => !document.hidden && void load(), refreshMs) : undefined
    return () => {
      cancelled = true
      controller.abort()
      if (timer !== undefined) window.clearInterval(timer)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, version, nonce, refreshMs])

  const reload = useCallback(() => setNonce((n) => n + 1), [])
  return { state, reload }
}

/** Shows the loading skeleton or the error, and the content once there is data. */
export function LoadGate<T>({ state, onRetry, children }: { state: Loaded<T>; onRetry: () => void; children: (data: T) => ReactNode }) {
  const { settings, openSettings } = useApp()
  const { t } = useI18n()
  return (
    <>
      {state.status === 'error' && <ErrorNotice error={state.error} settings={settings} onOpenSettings={openSettings} onRetry={onRetry} />}
      {state.data === null && state.status !== 'error' && (
        <div role="status" aria-label={t('manage.loading')}>
          <SkeletonRows />
        </div>
      )}
      {state.data !== null && children(state.data)}
    </>
  )
}
