import { describe, expect, it } from 'vitest'
import { checkHealth, formatDiagnostics, safeOrigin } from './diagnostics'

const reply = (status: number, body: unknown) => (async () => new Response(JSON.stringify(body), { status })) as unknown as typeof fetch

describe('checkHealth', () => {
  const s = { backendUrl: 'http://svc/', apiKey: 'secret' }
  it('reports ok with a timing', async () => {
    const r = await checkHealth(s, reply(200, { status: 'ok' }))
    expect(r.health).toBe('ok')
    expect(r.ms).toBeGreaterThanOrEqual(0)
  })
  it('reports unhealthy for a bad status or body', async () => {
    expect((await checkHealth(s, reply(500, {}))).health).toBe('unhealthy')
    expect((await checkHealth(s, reply(200, { status: 'degraded' }))).health).toBe('unhealthy')
  })
  it('reports unreachable instead of throwing', async () => {
    const down = (async () => {
      throw new TypeError('network')
    }) as unknown as typeof fetch
    expect((await checkHealth(s, down)).health).toBe('unreachable')
  })
  it('does not call the network in demo mode or without an address', async () => {
    const never = (async () => {
      throw new Error('should not be called')
    }) as unknown as typeof fetch
    expect((await checkHealth({ ...s, demo: true }, never)).health).toBe('demo')
    expect((await checkHealth({ backendUrl: ' ', apiKey: '' }, never)).health).toBe('unset')
  })
  it('never sends the API key to the health endpoint', async () => {
    let init: RequestInit | undefined
    const spy = (async (_url: string, i: RequestInit) => {
      init = i
      return new Response('{"status":"ok"}')
    }) as unknown as typeof fetch
    await checkHealth(s, spy)
    expect(JSON.stringify(init)).not.toContain('secret')
  })
})

describe('safeOrigin', () => {
  it('drops paths, credentials and queries', () => {
    expect(safeOrigin('https://user:pass@api.example.com:8443/v1?token=abc')).toBe('https://api.example.com:8443')
  })
  it('handles empty and invalid addresses', () => {
    expect(safeOrigin('  ')).toBe('not set')
    expect(safeOrigin('not a url')).toBe('invalid address')
  })
})

describe('formatDiagnostics', () => {
  const base = {
    appVersion: '1.0.0',
    settings: { backendUrl: 'https://api.example.com/secret-path', apiKey: 'topsecret' },
    health: { health: 'ok' as const, ms: 42 },
    live: 'live' as const,
    language: 'en',
    unread: 3,
    userAgent: 'UA',
    now: new Date('2026-06-01T12:00:00Z'),
  }
  it('never includes the key or the address path', () => {
    const text = formatDiagnostics(base)
    expect(text).not.toContain('topsecret')
    expect(text).not.toContain('secret-path')
    expect(text).toContain('Service: https://api.example.com')
    expect(text).toContain('API key: entered')
    expect(text).toContain('Health: ok (42 ms)')
  })
  it('describes demo mode without a service', () => {
    const text = formatDiagnostics({ ...base, settings: { ...base.settings, demo: true }, health: { health: 'demo' } })
    expect(text).toContain('Mode: demo')
    expect(text).toContain('Service: not used')
  })
})
