import { afterEach, describe, expect, it } from 'vitest'
import { demoDigestsClient, describeEvery, httpDigests } from './digests'

const settings = { backendUrl: 'http://svc', apiKey: 'k' }
const realFetch = globalThis.fetch
afterEach(() => {
  globalThis.fetch = realFetch
})

describe('describeEvery', () => {
  it('reads the service duration format', () => {
    expect(describeEvery('24h0m0s')).toEqual({ unit: 'hours', n: 24 })
    expect(describeEvery('1h0m0s')).toEqual({ unit: 'hours', n: 1 })
    expect(describeEvery('30m0s')).toEqual({ unit: 'minutes', n: 30 })
    expect(describeEvery('1h30m0s')).toEqual({ unit: 'minutes', n: 90 })
  })

  it('treats zero and anything unreadable as "only on request"', () => {
    expect(describeEvery('0s')).toBeNull()
    expect(describeEvery('')).toBeNull()
    expect(describeEvery('soon')).toBeNull()
  })
})

describe('digests clients', () => {
  it('the demo client lists, makes a digest and explains without any network', async () => {
    globalThis.fetch = (async () => {
      throw new Error('must not be called')
    }) as typeof fetch
    const c = demoDigestsClient()
    const before = (await c.list()).digests.length
    const made = await c.generate()
    expect(made?.createdBy).toBe('demo@example.com')
    expect((await c.list()).digests).toHaveLength(before + 1)
    expect(await c.explain('x')).toContain('Demo mode')
  })

  it('the http client uses the documented endpoints and reads the answers', async () => {
    const calls: string[] = []
    globalThis.fetch = (async (url: string, init?: RequestInit) => {
      const path = String(url).replace('http://svc', '')
      calls.push(`${init?.method ?? 'GET'} ${path}`)
      if (path === '/api/digests/generate') return new Response(JSON.stringify({ generated: false, digest: null }), { status: 200 })
      if (path.startsWith('/api/explain/')) return new Response(JSON.stringify({ explanation: 'because' }), { status: 200 })
      return new Response(JSON.stringify({ digests: [], every: '0s' }), { status: 200 })
    }) as typeof fetch
    const c = httpDigests(settings)
    expect((await c.list()).every).toBe('0s')
    expect(await c.generate()).toBeNull()
    expect(await c.explain('a/b')).toBe('because')
    expect(calls).toEqual(['GET /api/digests', 'POST /api/digests/generate', 'GET /api/explain/a%2Fb'])
  })
})
