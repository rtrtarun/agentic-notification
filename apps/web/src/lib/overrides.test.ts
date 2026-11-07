import { afterEach, describe, expect, it } from 'vitest'
import { sendFeedback } from './api'
import { demoOverridesClient, describeOverride, httpOverrides, overrideProblem } from './overrides'

const settings = { backendUrl: 'http://svc', apiKey: 'k' }
const realFetch = globalThis.fetch
afterEach(() => {
  globalThis.fetch = realFetch
})

describe('overrides', () => {
  it('describes what an override covers', () => {
    expect(describeOverride({ source: 'github', eventType: 'star' })).toBe('github / star')
    expect(describeOverride({ repository: 'acme/*' })).toBe('repository acme/*')
    expect(describeOverride({ source: 'github', repository: 'acme/web' })).toBe('github / repository acme/web')
  })

  it('wants at least one thing to cover', () => {
    expect(overrideProblem({ mode: 'never' })).toBe('scope')
    expect(overrideProblem({ mode: 'never', source: '  ', eventType: '' })).toBe('scope')
    expect(overrideProblem({ mode: 'never', repository: 'acme/*' })).toBeNull()
  })

  it('the demo client adds and removes in memory', async () => {
    const c = demoOverridesClient()
    const before = (await c.list()).overrides.length
    const added = await c.create({ mode: 'always', source: 'jira' })
    expect((await c.list()).overrides).toHaveLength(before + 1)
    await c.remove(added.id)
    expect((await c.list()).overrides).toHaveLength(before)
  })

  it('the http client uses the documented endpoints', async () => {
    const calls: string[] = []
    globalThis.fetch = (async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${String(url).replace('http://svc', '')}`)
      return new Response(JSON.stringify({ overrides: [], adminOnly: false }), { status: 200 })
    }) as typeof fetch
    const c = httpOverrides(settings)
    await c.list()
    await c.create({ mode: 'never', source: 'github' })
    await c.remove('a/b')
    expect(calls).toEqual(['GET /api/overrides', 'POST /api/overrides', 'DELETE /api/overrides/a%2Fb'])
  })
})

describe('sendFeedback scope and result', () => {
  it('sends the scope and returns what the service said', async () => {
    let body = ''
    globalThis.fetch = (async (_url: string, init?: RequestInit) => {
      body = String(init?.body)
      return new Response(JSON.stringify({ status: 'accepted', overrideSkipped: true }), { status: 202 })
    }) as typeof fetch
    const r = await sendFeedback(settings, { eventId: 'e1', action: 'never', scope: 'repository' })
    expect(JSON.parse(body)).toEqual({ eventId: 'e1', action: 'never', scope: 'repository' })
    expect(r.overrideSkipped).toBe(true)
  })

  it('copes with an empty reply body and sends nothing in demo mode', async () => {
    globalThis.fetch = (async () => new Response(null, { status: 202 })) as typeof fetch
    expect(await sendFeedback(settings, { eventId: 'e1', action: 'dismissed' })).toEqual({})
    globalThis.fetch = (async () => {
      throw new Error('must not be called')
    }) as typeof fetch
    expect(await sendFeedback({ ...settings, demo: true }, { eventId: 'e1', action: 'never' })).toEqual({})
  })
})
