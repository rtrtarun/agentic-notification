// Digests and explanations from Attention Intelligence (go-server pkg/attention).
// A digest is a summary of what was held back; an explanation says in plain
// words why one event was handled the way it was. Neither is ever about a
// critical event, which is sent at once and needs no summary.

import { request, type Settings } from './api'

export interface DigestGroup {
  key: string
  title: string
  summary: string
  count: number
  eventIds: string[]
}

export interface Highlight {
  eventId: string
  title: string
  reason: string
  score: number
}

export interface Digest {
  id: string
  from: string
  to: string
  count: number
  title: string
  summary: string
  groups: DigestGroup[]
  highlights: Highlight[]
  model: string
  createdBy: string
  createdAt: string
}

export interface DigestList {
  digests: Digest[]
  /** How often one is made on its own, for example "24h0m0s"; "0s" means only on request. */
  every: string
}

export interface DigestsClient {
  list(signal?: AbortSignal): Promise<DigestList>
  /** Makes a digest of what was held back since the last one. Null when there is nothing to report. */
  generate(): Promise<Digest | null>
  explain(eventId: string): Promise<string>
}

/** "Every 24 hours", "Every 30 minutes" or null when digests are only made on request. */
export function describeEvery(every: string): { unit: 'hours' | 'minutes'; n: number } | null {
  const m = /^(?:(\d+)h)?(?:(\d+)m)?(?:[\d.]+s)?$/.exec(every.trim())
  if (!m) return null
  const hours = Number(m[1] ?? 0)
  const minutes = Number(m[2] ?? 0)
  if (hours > 0 && minutes === 0) return { unit: 'hours', n: hours }
  const total = hours * 60 + minutes
  return total > 0 ? { unit: 'minutes', n: total } : null
}

function demoDigests(): Digest[] {
  const at = (hoursAgo: number) => new Date(Date.now() - hoursAgo * 3_600_000).toISOString()
  return [
    {
      id: 'dg-2',
      from: at(48),
      to: at(24),
      count: 9,
      title: '9 notifications held back',
      summary: '9 notifications held back, from 7 github, 2 jira. Busiest: acme/web (github) with 5.',
      groups: [
        { key: 'github/acme/web', title: 'acme/web (github)', summary: '5 notifications: 3 star, 2 pull_request', count: 5, eventIds: [] },
        { key: 'github/acme/api', title: 'acme/api (github)', summary: '2 notifications: 2 push', count: 2, eventIds: [] },
        { key: 'jira', title: 'jira', summary: '2 notifications: 2 issue', count: 2, eventIds: [] },
      ],
      highlights: [{ eventId: 'demo-pr', title: 'github pull_request in acme/web', reason: 'scored 75: someone asked for your review', score: 75 }],
      model: 'heuristic-v1',
      createdBy: 'schedule',
      createdAt: at(24),
    },
    {
      id: 'dg-1',
      from: at(72),
      to: at(48),
      count: 3,
      title: '3 notifications held back',
      summary: '3 notifications held back, from 3 github. Busiest: acme/docs (github) with 2.',
      groups: [{ key: 'github/acme/docs', title: 'acme/docs (github)', summary: '3 notifications: 3 star', count: 3, eventIds: [] }],
      highlights: [],
      model: 'heuristic-v1',
      createdBy: 'schedule',
      createdAt: at(48),
    },
  ]
}

export function demoDigestsClient(): DigestsClient {
  let list = demoDigests()
  return {
    async list() {
      return { digests: list, every: '24h0m0s' }
    },
    async generate() {
      const made: Digest = { ...list[0], id: `dg-${list.length + 1}`, createdAt: new Date().toISOString(), createdBy: 'demo@example.com', title: '2 notifications held back', count: 2 }
      list = [made, ...list]
      return made
    },
    async explain() {
      return 'Demo mode: explanations come from the service. For a real event it says which rule applied, whether Attention Intelligence was involved, and why it was held, merged or sent.'
    },
  }
}

export function httpDigests(settings: Settings): DigestsClient {
  return {
    async list(signal) {
      return (await (await request(settings, '/api/digests', { signal })).json()) as DigestList
    },
    async generate() {
      const body = (await (await request(settings, '/api/digests/generate', { method: 'POST' })).json()) as { generated: boolean; digest: Digest | null }
      return body.generated ? body.digest : null
    },
    async explain(eventId) {
      return ((await (await request(settings, `/api/explain/${encodeURIComponent(eventId)}`)).json()) as { explanation: string }).explanation
    },
  }
}

export function digestsClientFor(settings: Settings): DigestsClient {
  return settings.demo ? demoDigestsClient() : httpDigests(settings)
}
