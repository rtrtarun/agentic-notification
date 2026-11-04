// Read state used to live only in this browser (localStorage). It now lives on
// the service. This module remains for one job: sending ids opened before the
// upgrade to the service once, so unread counts do not jump back up.

import type { InboxClient } from './inbox'

const STORAGE_KEY = 'attention.read'
const CHUNK = 100

export function loadRead(): Set<string> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return new Set()
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return new Set()
    return new Set(parsed.filter((v): v is string => typeof v === 'string'))
  } catch {
    return new Set()
  }
}

function forget(): void {
  try {
    localStorage.removeItem(STORAGE_KEY)
  } catch {
    // nothing to clean up
  }
}

/** Marks the locally remembered ids read on the service, then forgets them. Returns how many were sent. */
export async function migrateLocalRead(client: InboxClient): Promise<number> {
  const ids = [...loadRead()]
  if (ids.length === 0) return 0
  for (let i = 0; i < ids.length; i += CHUNK) {
    await client.bulk(ids.slice(i, i + CHUNK), { type: 'read' })
  }
  forget()
  return ids.length
}
