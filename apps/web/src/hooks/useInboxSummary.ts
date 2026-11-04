import { useEffect, useState } from 'react'
import type { InboxClient, InboxItem } from '../lib/inbox'

export interface InboxSummary {
  status: 'loading' | 'ready' | 'error'
  /** The newest unread notifications, for the bell. */
  items: InboxItem[]
  unread: number
}

const REFRESH_MS = 15_000
const BELL_ITEMS = 5

/**
 * The unread count and the bell's short list. It reloads when `version` changes
 * (something was read, archived and so on) and while the tab is visible.
 */
export function useInboxSummary(client: InboxClient, enabled: boolean, version: number): InboxSummary {
  const [summary, setSummary] = useState<InboxSummary>({ status: 'loading', items: [], unread: 0 })

  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    let controller = new AbortController()

    const load = async () => {
      controller.abort()
      controller = new AbortController()
      try {
        const page = await client.list(
          { text: '', priorities: [], outcomes: [], unread: true, archived: false, sort: 'newest', page: 1, pageSize: BELL_ITEMS },
          controller.signal,
        )
        if (!cancelled) setSummary({ status: 'ready', items: page.items, unread: page.counts.unread })
      } catch (err) {
        if (cancelled || (err instanceof DOMException && err.name === 'AbortError')) return
        // Keep the last numbers on screen so a brief outage does not blank the badge.
        setSummary((previous) => ({ ...previous, status: 'error' }))
      }
    }

    void load()
    const timer = window.setInterval(() => {
      if (!document.hidden) void load()
    }, REFRESH_MS)
    return () => {
      cancelled = true
      controller.abort()
      window.clearInterval(timer)
    }
  }, [client, enabled, version])

  return summary
}
