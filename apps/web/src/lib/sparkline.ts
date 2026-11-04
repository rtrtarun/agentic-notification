// Small trend lines for the stat cards. They are drawn from the events already
// loaded, so they show real activity (events received per period), never made-up curves.

import type { EventSummary } from './api'
import type { Range } from './inboxQuery'
import type { Level } from './inbox'
import { levelOf } from './priority'

const SPAN_MS: Record<Exclude<Range, 'all'>, number> = {
  '1h': 3_600_000,
  '24h': 86_400_000,
  '7d': 7 * 86_400_000,
  '30d': 30 * 86_400_000,
}

export const BUCKETS = 12

/** Counts events per equal time slice, oldest first. `level` limits which events count; omit it to count all. */
export function bucketCounts(events: EventSummary[], now: Date, range: Range, level?: Level, buckets = BUCKETS): number[] {
  const counts = new Array<number>(buckets).fill(0)
  const times = events.map((e) => new Date(e.createdAt).getTime())
  const end = now.getTime()
  const oldest = times.length ? Math.min(...times) : end
  // "All" spans from the oldest loaded event, with at least an hour so a single event is not a spike.
  const span = range === 'all' ? Math.max(end - oldest, 3_600_000) : SPAN_MS[range]
  const start = end - span
  events.forEach((e, i) => {
    if (level && levelOf(e) !== level) return
    const at = times[i]
    if (at < start || at > end) return
    counts[Math.min(buckets - 1, Math.floor(((at - start) / span) * buckets))]++
  })
  return counts
}

/** SVG polyline points for a series, scaled into width x height with a little padding. */
export function sparklinePoints(values: number[], width: number, height: number, pad = 3): string {
  if (values.length === 0) return ''
  const max = Math.max(...values, 1)
  const step = values.length > 1 ? (width - pad * 2) / (values.length - 1) : 0
  return values
    .map((v, i) => `${(pad + i * step).toFixed(1)},${(height - pad - (v / max) * (height - pad * 2)).toFixed(1)}`)
    .join(' ')
}
