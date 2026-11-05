// The delivery log: what the service sent, queued or gave up on. The service
// never returns a target or the message text (go-server pkg/delivery), and
// errors arrive with URLs reduced to their host. Demo mode shows sample rows.

import { request, type Settings } from './api'

export type DeliveryStatus = 'pending' | 'sending' | 'sent' | 'dead'

export interface Delivery {
  id: string
  eventId: string
  /** A channel ("slack") or, for an inline critical send, the providers used ("smtp+webhook+sms"). */
  channel: string
  class: string
  title: string
  status: DeliveryStatus
  attempts: number
  nextAttemptAt: string
  lastError?: string
  /** Sent inline (critical events) and only logged here. */
  direct?: boolean
  createdAt: string
  updatedAt: string
}

export interface DeliveryLog {
  deliveries: Delivery[]
  counts: Partial<Record<DeliveryStatus, number>>
}

export interface DeliveriesClient {
  list(limit: number, signal?: AbortSignal): Promise<DeliveryLog>
  /** Sends a dead delivery again. */
  retry(id: string): Promise<void>
}

/** A dead delivery is the only one a person can retry. */
export function canRetry(d: Delivery): boolean {
  return d.status === 'dead' && !d.direct
}

/** Counts for the summary line, always with every status present. */
export function summarise(counts: DeliveryLog['counts']): Record<DeliveryStatus, number> {
  return { pending: counts.pending ?? 0, sending: counts.sending ?? 0, sent: counts.sent ?? 0, dead: counts.dead ?? 0 }
}

function demoRows(now: Date): Delivery[] {
  const at = (minutesAgo: number) => new Date(now.getTime() - minutesAgo * 60_000).toISOString()
  const row = (id: string, minutesAgo: number, rest: Partial<Delivery>): Delivery => ({
    id,
    eventId: `evt-${id}`,
    channel: 'slack',
    class: 'standard',
    title: '[STANDARD] github/pull_request',
    status: 'sent',
    attempts: 1,
    nextAttemptAt: at(minutesAgo),
    createdAt: at(minutesAgo),
    updatedAt: at(minutesAgo),
    ...rest,
  })
  return [
    row('d1', 4, { channel: 'smtp+webhook+sms', class: 'critical', title: '[CRITICAL] github/security_advisory', direct: true }),
    row('d2', 12, { channel: 'slack' }),
    row('d3', 30, { channel: 'sms', status: 'pending', attempts: 1, lastError: 'sms gateway returned status 503' }),
    row('d4', 95, { channel: 'email', status: 'dead', attempts: 5, lastError: 'dial tcp smtp.example.com:587: i/o timeout' }),
  ]
}

export function demoDeliveriesClient(now: () => Date = () => new Date()): DeliveriesClient {
  let rows = demoRows(now())
  return {
    async list(limit) {
      const counts: DeliveryLog['counts'] = {}
      for (const r of rows) counts[r.status] = (counts[r.status] ?? 0) + 1
      return { deliveries: rows.slice(0, limit), counts }
    },
    async retry(id) {
      rows = rows.map((r) => (r.id === id && canRetry(r) ? { ...r, status: 'pending', attempts: 0 } : r))
    },
  }
}

export function httpDeliveries(settings: Settings): DeliveriesClient {
  return {
    async list(limit, signal) {
      return (await (await request(settings, `/api/deliveries?limit=${limit}`, { signal })).json()) as DeliveryLog
    },
    async retry(id) {
      await request(settings, `/api/deliveries/${encodeURIComponent(id)}/retry`, { method: 'POST' })
    },
  }
}

export function deliveriesClientFor(settings: Settings): DeliveriesClient {
  return settings.demo ? demoDeliveriesClient() : httpDeliveries(settings)
}
