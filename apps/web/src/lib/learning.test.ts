import { afterEach, describe, expect, it } from 'vitest'
import { demoLearningClient, evidenceParts, evidenceTotal, httpLearning } from './learning'

const settings = { backendUrl: 'http://svc', apiKey: 'k' }
const realFetch = globalThis.fetch
afterEach(() => {
  globalThis.fetch = realFetch
})

describe('evidence', () => {
  it('lists only the signals that exist, in a stable order', () => {
    expect(evidenceParts({ dismissed: 5, never: 0, opened: 1, reply: 0, always: 2 })).toEqual([
      { label: 'dismissed', n: 5 },
      { label: 'opened', n: 1 },
      { label: 'always', n: 2 },
    ])
    expect(evidenceParts({ dismissed: 0, never: 0, opened: 0, reply: 0, always: 0 })).toEqual([])
  })

  it('totals every signal', () => {
    expect(evidenceTotal({ dismissed: 5, never: 2, opened: 1, reply: 3, always: 4 })).toBe(15)
  })
})

describe('the demo client', () => {
  it('works with no network, and applying or dismissing removes the suggestion', async () => {
    globalThis.fetch = (async () => {
      throw new Error('must not be called')
    }) as typeof fetch
    const c = demoLearningClient()
    const { suggestions, minSignals } = await c.list()
    expect(suggestions.length).toBeGreaterThanOrEqual(3)
    expect(minSignals).toBe(5)
    expect(new Set(suggestions.map((s) => s.kind)).size).toBeGreaterThan(1)
    expect((await c.apply(suggestions[0].id)).status).toBe('applied')
    expect((await c.dismiss(suggestions[1].id)).status).toBe('dismissed')
    expect((await c.list()).suggestions).toHaveLength(suggestions.length - 2)
    await expect(c.apply(suggestions[0].id)).rejects.toThrow('no longer pending')
  })

  it('never proposes anything about a critical event or rule', async () => {
    const { suggestions } = await demoLearningClient().list()
    for (const s of suggestions) {
      expect(`${s.summary} ${s.proposed} ${s.ruleName ?? ''}`.toLowerCase()).not.toContain('critical')
    }
  })
})

describe('the http client', () => {
  it('uses the documented endpoints and reads the answers', async () => {
    const calls: string[] = []
    globalThis.fetch = (async (url: string, init?: RequestInit) => {
      const path = String(url).replace('http://svc', '')
      calls.push(`${init?.method ?? 'GET'} ${path}`)
      if (path.endsWith('/apply')) return new Response(JSON.stringify({ suggestion: { id: 'a/b', status: 'applied' } }), { status: 200 })
      if (path.endsWith('/dismiss')) return new Response(JSON.stringify({ suggestion: { id: 'x', status: 'dismissed' } }), { status: 200 })
      return new Response(JSON.stringify({ suggestions: [], minSignals: 5 }), { status: 200 })
    }) as typeof fetch
    const c = httpLearning(settings)
    expect((await c.list()).minSignals).toBe(5)
    expect((await c.apply('a/b')).status).toBe('applied')
    expect((await c.dismiss('x')).status).toBe('dismissed')
    expect(calls).toEqual(['GET /api/learning/suggestions', 'POST /api/learning/suggestions/a%2Fb/apply', 'POST /api/learning/suggestions/x/dismiss'])
  })

  it('turns a refused change into a readable error', async () => {
    globalThis.fetch = (async () => new Response(JSON.stringify({ error: 'critical and locked rules can never be changed by a suggestion' }), { status: 403 })) as typeof fetch
    await expect(httpLearning(settings).apply('x')).rejects.toThrow('locked rules')
  })
})
