// Fuzzy matching for the command palette: every character of the query must
// appear in the text in order. Consecutive and word-start matches score higher.

/** Returns a score (higher is better) or null when the query does not match. */
export function fuzzyScore(query: string, text: string): number | null {
  const q = query.trim().toLowerCase()
  if (q === '') return 0
  const target = text.toLowerCase()
  let score = 0
  let from = 0
  let streak = 0
  for (const ch of q) {
    const at = target.indexOf(ch, from)
    if (at === -1) return null
    const wordStart = at === 0 || target[at - 1] === ' '
    streak = at === from ? streak + 1 : 0
    score += 1 + streak * 2 + (wordStart ? 3 : 0)
    from = at + 1
  }
  // Shorter text wins when two match equally well.
  return score - target.length * 0.01
}

/** Filters and orders items by how well `label(item)` matches the query. */
export function fuzzyFilter<T>(items: T[], query: string, label: (item: T) => string): T[] {
  if (query.trim() === '') return items
  return items
    .map((item) => ({ item, score: fuzzyScore(query, label(item)) }))
    .filter((r): r is { item: T; score: number } => r.score !== null)
    .sort((a, b) => b.score - a.score)
    .map((r) => r.item)
}
