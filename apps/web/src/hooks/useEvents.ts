import { useCallback, useEffect, useState } from 'react'
import { ApiError, fetchEvents, type EventSummary, type Settings } from '../lib/api'

export type Feed =
  | { status: 'loading'; events: EventSummary[] }
  | { status: 'ready'; events: EventSummary[]; updatedAt: Date }
  | { status: 'error'; events: EventSummary[]; error: ApiError }

const REFRESH_MS = 15_000

/** Loads recent events and refreshes them while the tab is visible. */
export function useEvents(settings: Settings): { feed: Feed; reload: () => void } {
  const [feed, setFeed] = useState<Feed>({ status: 'loading', events: [] })
  const [nonce, setNonce] = useState(0)
  const reload = useCallback(() => setNonce((n) => n + 1), [])

  useEffect(() => {
    let controller = new AbortController()
    let cancelled = false

    const load = async () => {
      controller.abort()
      controller = new AbortController()
      try {
        const events = await fetchEvents(settings, controller.signal)
        if (!cancelled) setFeed({ status: 'ready', events, updatedAt: new Date() })
      } catch (err) {
        if (cancelled || (err instanceof DOMException && err.name === 'AbortError')) return
        const error = err instanceof ApiError ? err : new ApiError('server', 'The service hit an error.')
        // Keep the last events on screen so a brief outage does not blank the page.
        setFeed((previous) => ({ status: 'error', events: previous.events, error }))
      }
    }

    setFeed((previous) => ({ status: 'loading', events: previous.events }))
    void load()

    const timer = window.setInterval(() => {
      if (!document.hidden) void load()
    }, REFRESH_MS)
    const onVisible = () => {
      if (!document.hidden) void load()
    }
    document.addEventListener('visibilitychange', onVisible)

    return () => {
      cancelled = true
      controller.abort()
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [settings, nonce])

  return { feed, reload }
}
