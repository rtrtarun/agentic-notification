// Which events the person has opened. Kept in this browser (localStorage) as a
// list of ids; nothing is sent to the service.

const STORAGE_KEY = 'attention.read'
const MAX_IDS = 2000

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

export function saveRead(ids: Set<string>): void {
  // Sets keep insertion order, so the oldest ids are dropped first.
  const list = [...ids].slice(-MAX_IDS)
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(list))
  } catch {
    // read state just will not persist
  }
}
