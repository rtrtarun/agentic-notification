import { describe, expect, it } from 'vitest'
import type { Settings } from './api'
import { backoffMs, connectLive, createSseParser, type LiveMessage, type LiveStatus } from './live'

describe('createSseParser', () => {
  const collect = () => {
    const got: LiveMessage[] = []
    return { got, push: createSseParser((m) => got.push(m)) }
  }

  it('parses complete events', () => {
    const { got, push } = collect()
    push('event: event\ndata: {"type":"event","id":"e1","attentionClass":"critical"}\n\n')
    expect(got).toEqual([{ type: 'event', id: 'e1', attentionClass: 'critical' }])
  })

  it('handles events split across chunks at any point', () => {
    const { got, push } = collect()
    const text = 'event: inbox\ndata: {"type":"inbox"}\n\nevent: event\ndata: {"type":"event","id":"x"}\n\n'
    for (const ch of text) push(ch)
    expect(got.map((m) => m.type)).toEqual(['inbox', 'event'])
  })

  it('accepts CRLF line endings', () => {
    const { got, push } = collect()
    push('data: {"type":"ready"}\r\n\r\n')
    expect(got).toEqual([{ type: 'ready' }])
  })

  it('ignores heartbeats, retry hints and events without data', () => {
    const { got, push } = collect()
    push(': keep-alive\n\nretry: 5000\n\nevent: ping\n\n')
    expect(got).toEqual([])
  })

  it('skips malformed JSON and keeps going', () => {
    const { got, push } = collect()
    push('data: {not json\n\ndata: {"type":"inbox"}\n\n')
    expect(got).toEqual([{ type: 'inbox' }])
  })

  it('joins multi-line data', () => {
    const { got, push } = collect()
    push('data: {"type":\ndata: "inbox"}\n\n')
    expect(got).toEqual([{ type: 'inbox' }])
  })

  it('waits for the blank line before emitting', () => {
    const { got, push } = collect()
    push('data: {"type":"inbox"}\n')
    expect(got).toEqual([])
    push('\n')
    expect(got).toHaveLength(1)
  })
})

describe('backoffMs', () => {
  it('doubles and caps at 30 seconds', () => {
    expect([0, 1, 2, 3].map(backoffMs)).toEqual([1000, 2000, 4000, 8000])
    expect(backoffMs(20)).toBe(30_000)
    expect(backoffMs(-3)).toBe(1000)
  })
})

const settings: Settings = { backendUrl: 'http://svc/', apiKey: 'k' }

function streamOf(...chunks: string[]): ReadableStream<Uint8Array> {
  const enc = new TextEncoder()
  return new ReadableStream({
    start(controller) {
      for (const c of chunks) controller.enqueue(enc.encode(c))
      controller.close()
    },
  })
}

describe('connectLive', () => {
  it('sends the key as a header, reports live on ready and delivers messages', async () => {
    const statuses: LiveStatus[] = []
    const messages: LiveMessage[] = []
    let seen: { url: string; headers: Record<string, string> } | undefined
    const fetchImpl = (async (url: string, init: RequestInit) => {
      seen = { url, headers: init.headers as Record<string, string> }
      return new Response(streamOf('data: {"type":"ready"}\n\n', 'data: {"type":"event","id":"e1"}\n\n'), { status: 200 })
    }) as unknown as typeof fetch

    const done = new Promise<void>((resolve) => {
      const handle = connectLive(settings, {
        fetchImpl,
        onStatus: (s) => statuses.push(s),
        onMessage: (m) => {
          messages.push(m)
          if (m.type === 'event') queueMicrotask(() => { handle.close(); resolve() })
        },
        sleep: async () => undefined,
      })
    })
    await done
    expect(seen?.url).toBe('http://svc/api/stream')
    expect(seen?.headers['X-API-Key']).toBe('k')
    expect(statuses).toContain('live')
    expect(statuses[statuses.length - 1]).toBe('off')
    expect(messages.map((m) => m.type)).toEqual(['ready', 'event'])
  })

  it('backs off hard after a rejected key and stops when closed', async () => {
    const sleeps: number[] = []
    let calls = 0
    const fetchImpl = (async () => {
      calls++
      return new Response('no', { status: 401 })
    }) as unknown as typeof fetch
    await new Promise<void>((resolve) => {
      const handle = connectLive(settings, {
        fetchImpl,
        onStatus: () => undefined,
        onMessage: () => undefined,
        sleep: async (ms) => {
          sleeps.push(ms)
          handle.close()
          resolve()
        },
      })
    })
    expect(calls).toBe(1)
    expect(sleeps[0]).toBe(backoffMs(4))
  })
})
