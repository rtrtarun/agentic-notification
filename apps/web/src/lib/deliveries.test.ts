import { describe, expect, it } from 'vitest'
import { canRetry, demoDeliveriesClient, httpDeliveries, summarise, type Delivery } from './deliveries'

const base: Delivery = {
  id: 'x', eventId: 'e', channel: 'sms', class: 'standard', title: 't', status: 'dead', attempts: 5,
  nextAttemptAt: '', createdAt: '', updatedAt: '',
}

describe('deliveries', () => {
  it('lets a person retry only a dead, queued delivery', () => {
    expect(canRetry(base)).toBe(true)
    expect(canRetry({ ...base, status: 'sent' })).toBe(false)
    expect(canRetry({ ...base, status: 'pending' })).toBe(false)
    expect(canRetry({ ...base, direct: true })).toBe(false)
  })

  it('summarises with every status present', () => {
    expect(summarise({ sent: 3 })).toEqual({ pending: 0, sending: 0, sent: 3, dead: 0 })
  })

  it('demo log counts match its rows and retry requeues a dead row', async () => {
    const client = demoDeliveriesClient(() => new Date('2026-06-01T12:00:00Z'))
    const log = await client.list(10)
    const total = Object.values(log.counts).reduce((n, c) => n + (c ?? 0), 0)
    expect(total).toBe(log.deliveries.length)
    const dead = log.deliveries.find((d) => d.status === 'dead')!
    await client.retry(dead.id)
    const after = await client.list(10)
    expect(after.deliveries.find((d) => d.id === dead.id)?.status).toBe('pending')
    expect(after.counts.dead).toBeUndefined()
  })

  it('demo retry leaves a direct critical row alone', async () => {
    const client = demoDeliveriesClient(() => new Date('2026-06-01T12:00:00Z'))
    await client.retry('d1')
    expect((await client.list(10)).deliveries.find((d) => d.id === 'd1')?.status).toBe('sent')
  })

  it('the http client asks for the limit and posts the retry', async () => {
    const calls: { url: string; method: string }[] = []
    const real = globalThis.fetch
    globalThis.fetch = (async (url: string, init?: RequestInit) => {
      calls.push({ url: String(url), method: init?.method ?? 'GET' })
      return new Response(JSON.stringify({ deliveries: [], counts: {} }), { status: 200 })
    }) as typeof fetch
    try {
      const client = httpDeliveries({ backendUrl: 'http://svc', apiKey: 'k' })
      await client.list(25)
      await client.retry('a/b')
      expect(calls[0].url).toBe('http://svc/api/deliveries?limit=25')
      expect(calls[1]).toEqual({ url: 'http://svc/api/deliveries/a%2Fb/retry', method: 'POST' })
    } finally {
      globalThis.fetch = real
    }
  })
})
