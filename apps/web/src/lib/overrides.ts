// "Always notify" and "never notify" choices. They come from feedback on a
// notification or are added here, apply to standard and background events only,
// and are shown in full so any of them can be removed. Critical events never
// follow them (go-server pkg/overrides).

import { request, type Settings } from './api'

export type OverrideMode = 'always' | 'never'

export interface Override {
  id: string
  source?: string
  eventType?: string
  repository?: string
  mode: OverrideMode
  createdBy: string
  note?: string
  createdAt: string
}

export interface NewOverride {
  mode: OverrideMode
  source?: string
  eventType?: string
  repository?: string
  note?: string
}

export interface OverridesList {
  overrides: Override[]
  /** True when only admins may add or remove overrides. */
  adminOnly: boolean
}

export interface OverridesClient {
  list(signal?: AbortSignal): Promise<OverridesList>
  create(o: NewOverride): Promise<Override>
  remove(id: string): Promise<void>
}

/** A short phrase for what an override covers: "github / star" or "repository acme/*". */
export function describeOverride(o: Pick<Override, 'source' | 'eventType' | 'repository'>): string {
  const parts: string[] = []
  if (o.source) parts.push(o.source)
  if (o.eventType) parts.push(o.eventType)
  if (o.repository) parts.push(`repository ${o.repository}`)
  return parts.join(' / ')
}

/** A problem the person can fix before asking the service. */
export function overrideProblem(o: NewOverride): 'scope' | null {
  return [o.source, o.eventType, o.repository].some((v) => (v ?? '').trim() !== '') ? null : 'scope'
}

export function demoOverridesClient(): OverridesClient {
  let list: Override[] = [
    { id: 'o1', source: 'github', eventType: 'star', mode: 'never', createdBy: 'demo@example.com', createdAt: new Date(Date.now() - 86_400_000).toISOString() },
    { id: 'o2', repository: 'acme/billing', mode: 'always', createdBy: 'sam@example.com', note: 'billing matters', createdAt: new Date(Date.now() - 3_600_000).toISOString() },
  ]
  return {
    async list() {
      return { overrides: list, adminOnly: false }
    },
    async create(o) {
      const created: Override = { ...o, id: `o${list.length + 1}`, createdBy: 'demo@example.com', createdAt: new Date().toISOString() }
      list = [created, ...list]
      return created
    },
    async remove(id) {
      list = list.filter((o) => o.id !== id)
    },
  }
}

export function httpOverrides(settings: Settings): OverridesClient {
  return {
    async list(signal) {
      return (await (await request(settings, '/api/overrides', { signal })).json()) as OverridesList
    },
    async create(o) {
      return (await (await request(settings, '/api/overrides', { method: 'POST', body: JSON.stringify(o) })).json()) as Override
    },
    async remove(id) {
      await request(settings, `/api/overrides/${encodeURIComponent(id)}`, { method: 'DELETE' })
    },
  }
}

export function overridesClientFor(settings: Settings): OverridesClient {
  return settings.demo ? demoOverridesClient() : httpOverrides(settings)
}
