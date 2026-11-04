import { useCallback, useState } from 'react'
import { loadRead, saveRead } from '../lib/read'

/** Tracks which events have been opened. Stored in this browser only. */
export function useRead() {
  const [read, setRead] = useState<Set<string>>(loadRead)

  const markRead = useCallback((id: string) => {
    setRead((previous) => {
      if (previous.has(id)) return previous
      const next = new Set(previous).add(id)
      saveRead(next)
      return next
    })
  }, [])

  const markAllRead = useCallback((ids: string[]) => {
    setRead((previous) => {
      const next = new Set(previous)
      for (const id of ids) next.add(id)
      if (next.size === previous.size) return previous
      saveRead(next)
      return next
    })
  }, [])

  return { read, markRead, markAllRead }
}
