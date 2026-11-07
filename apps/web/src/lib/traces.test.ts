import { afterEach, describe, expect, it } from 'vitest'
import { demoEvents } from './demo'
import { barWidths, demoAgentEvents, demoTraces, demoTracesClient, formatCost, formatDuration, httpTraces } from './traces'

const settings = { backendUrl: 'http://svc', apiKey: 'k' }
const realFetch = globalThis.fetch
afterEach(() => {
  globalThis.fetch = realFetch
})

describe('formatting', () => {
  it('writes durations in plain units', () => {
    expect(formatDuration(850)).toBe('850 ms')
    expect(formatDuration(30000)).toBe('30 s')
    expect(formatDuration(90000)).toBe('1 min 30 s')
    expect(formatDuration(120000)).toBe('2 min')
  })

  it('keeps the digits of tiny costs', () => {
    expect(formatCost(0)).toBe('$0')
    expect(formatCost(0.0041)).toBe('$0.0041')
    expect(formatCost(0.006)).toBe('$0.006')
    expect(formatCost(1.5)).toBe('$1.50')
  })

  it('scales duration bars to the longest step and keeps tiny ones visible', () => {
    expect(barWidths([{ durationMs: 30000 }, { durationMs: 15000 }, { durationMs: 100 }, {}])).toEqual([100, 50, 3, 0])
    expect(barWidths([{}, {}])).toEqual([0, 0])
    expect(barWidths([])).toEqual([])
  })
})

describe('demo runs', () => {
  const runs = demoTraces(new Date('2026-03-01T12:00:00Z'))

  it('show a tool timeout, three retries and a rule alert', () => {
    const run = runs.find((r) => r.traceId.startsWith('run-research'))!
    expect(run.retries).toBe(3)
    expect(run.failures).toBe(1)
    expect(run.status).toBe('complete')
    const failure = run.stepList.find((s) => s.eventType === 'tool.call.failed')!
    expect(failure.durationMs).toBe(30000)
    const alert = run.stepList.find((s) => s.attempt === 4)!
    expect(alert.attentionClass).toBe('standard')
    expect(alert.decisionReason).toContain('agent-retries')
    expect(alert.decisionReason).toContain('Slack')
  })

  it('include a run that is paused for human approval', () => {
    const run = runs.find((r) => r.status === 'waiting')!
    expect(run.stepList[run.stepList.length - 1].stepKind).toBe('human_approval')
  })

  it('chain each step to its parent and total the cost', () => {
    for (const run of runs) {
      run.stepList.forEach((s, i) => {
        expect(s.depth).toBe(s.parentEventId ? run.stepList[i - 1].depth + 1 : 0)
      })
      const sum = run.stepList.reduce((n, s) => n + (s.costUsd ?? 0), 0)
      expect(run.costUsd).toBeCloseTo(sum, 6)
      expect(run.steps).toBe(run.stepList.length)
    }
  })

  it('never claim a critical event: an agent cannot make its own step critical', () => {
    for (const run of runs) {
      expect(run.worstClass).not.toBe('critical')
      for (const s of run.stepList) expect(s.attentionClass).not.toBe('critical')
    }
  })

  it('appear as ordinary events so "Inspect" opens the usual detail drawer', () => {
    const events = demoEvents(new Date('2026-03-01T12:00:00Z'))
    const agent = demoAgentEvents(new Date('2026-03-01T12:00:00Z'))
    expect(agent.length).toBe(runs.reduce((n, r) => n + r.steps, 0))
    for (const e of agent) {
      expect(events.find((x) => x.id === e.id)?.traceId).toBe(e.traceId)
    }
    const stamps = events.map((e) => e.createdAt)
    expect([...stamps].sort().reverse()).toEqual(stamps)
  })
})

describe('clients', () => {
  it('the demo client lists summaries without steps and returns a run in full', async () => {
    globalThis.fetch = (async () => {
      throw new Error('must not be called')
    }) as typeof fetch
    const c = demoTracesClient()
    const list = await c.list()
    expect(list.length).toBeGreaterThanOrEqual(3)
    expect('stepList' in list[0]).toBe(false)
    const full = await c.get(list[0].traceId)
    expect(full.stepList.length).toBe(list[0].steps)
    await expect(c.get('nope')).rejects.toThrow('could not be found')
  })

  it('the http client uses the documented endpoints', async () => {
    const calls: string[] = []
    globalThis.fetch = (async (url: string) => {
      const path = String(url).replace('http://svc', '')
      calls.push(path)
      if (path === '/api/traces') return new Response(JSON.stringify({ traces: [{ traceId: 'a/b' }] }), { status: 200 })
      return new Response(JSON.stringify({ traceId: 'a/b', stepList: [] }), { status: 200 })
    }) as typeof fetch
    const c = httpTraces(settings)
    expect((await c.list())[0].traceId).toBe('a/b')
    expect((await c.get('a/b')).traceId).toBe('a/b')
    expect(calls).toEqual(['/api/traces', '/api/traces/a%2Fb'])
  })
})
