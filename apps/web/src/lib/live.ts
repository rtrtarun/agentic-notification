// Real-time updates. The service streams small "something changed" messages
// (go-server pkg/stream); the console refreshes its lists when one arrives.
//
// The browser EventSource API cannot send the API key header, and the key must
// not go in a URL, so this reads the stream with fetch and parses the
// text/event-stream format itself.

import type { Settings } from './api'

export interface LiveMessage {
  type: 'ready' | 'event' | 'inbox' | string
  id?: string
  attentionClass?: string
}

export type LiveStatus = 'off' | 'connecting' | 'live' | 'retrying'

/**
 * Parses server-sent events. Feed it text as it arrives, in any chunking; it
 * calls `onMessage` once per complete event. Comment lines (heartbeats) and
 * events without data are ignored, and bad JSON is skipped rather than thrown.
 */
export function createSseParser(onMessage: (m: LiveMessage) => void): (chunk: string) => void {
  let buffer = ''
  return (chunk) => {
    buffer += chunk
    // An event ends at a blank line. Normalise CRLF so both line endings work.
    buffer = buffer.replace(/\r\n/g, '\n')
    let end: number
    while ((end = buffer.indexOf('\n\n')) >= 0) {
      const block = buffer.slice(0, end)
      buffer = buffer.slice(end + 2)
      const data: string[] = []
      for (const line of block.split('\n')) {
        if (line.startsWith(':')) continue
        if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''))
      }
      if (data.length === 0) continue
      try {
        const parsed = JSON.parse(data.join('\n')) as LiveMessage
        if (parsed && typeof parsed.type === 'string') onMessage(parsed)
      } catch {
        // ignore a malformed event; the next one will still refresh the lists
      }
    }
  }
}

/** 1 s, 2 s, 4 s ... capped at 30 s. */
export function backoffMs(attempt: number): number {
  return Math.min(30_000, 1000 * 2 ** Math.max(0, attempt))
}

export interface LiveHandle {
  close(): void
}

interface Options {
  onMessage: (m: LiveMessage) => void
  onStatus: (s: LiveStatus) => void
  /** Injected for tests. */
  fetchImpl?: typeof fetch
  /** Injected for tests. */
  sleep?: (ms: number, signal: AbortSignal) => Promise<void>
}

function defaultSleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const id = window.setTimeout(resolve, ms)
    signal.addEventListener('abort', () => {
      window.clearTimeout(id)
      resolve()
    })
  })
}

/**
 * Connects to the stream and keeps reconnecting with backoff until closed.
 * A 401 or 503 stops retrying as fast and goes to the slow end of the backoff,
 * so a wrong key is not hammered.
 */
export function connectLive(settings: Settings, opts: Options): LiveHandle {
  const controller = new AbortController()
  const doFetch = opts.fetchImpl ?? fetch
  const sleep = opts.sleep ?? defaultSleep
  const url = settings.backendUrl.replace(/\/+$/, '') + '/api/stream'

  const run = async () => {
    let attempt = 0
    while (!controller.signal.aborted) {
      opts.onStatus(attempt === 0 ? 'connecting' : 'retrying')
      try {
        const res = await doFetch(url, {
          headers: settings.apiKey ? { 'X-API-Key': settings.apiKey, Accept: 'text/event-stream' } : { Accept: 'text/event-stream' },
          signal: controller.signal,
        })
        if (!res.ok || !res.body) {
          attempt = res.status === 401 || res.status === 503 ? 5 : attempt + 1
        } else {
          const parse = createSseParser((m) => {
            if (m.type === 'ready') {
              attempt = 0
              opts.onStatus('live')
            }
            opts.onMessage(m)
          })
          const reader = res.body.getReader()
          const decoder = new TextDecoder()
          for (;;) {
            const { done, value } = await reader.read()
            if (done) break
            parse(decoder.decode(value, { stream: true }))
          }
          attempt += 1
        }
      } catch {
        if (controller.signal.aborted) return
        attempt += 1
      }
      if (controller.signal.aborted) return
      opts.onStatus('retrying')
      await sleep(backoffMs(attempt - 1), controller.signal)
    }
  }
  void run()

  return {
    close() {
      controller.abort()
      opts.onStatus('off')
    },
  }
}
