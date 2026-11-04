import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, fetchEvents, sendFeedback, type Settings } from './api'

const settings: Settings = { backendUrl: 'http://svc.test:8080/', apiKey: 'k1' }

function respond(status: number, body: unknown = {}) {
  return vi.fn().mockResolvedValue(new Response(JSON.stringify(body), { status }))
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('fetchEvents', () => {
  it('requests the list with the API key and no double slash', async () => {
    const fetchMock = respond(200, { events: [{ id: 'a' }] })
    vi.stubGlobal('fetch', fetchMock)

    const events = await fetchEvents(settings, undefined, 50)

    expect(events).toEqual([{ id: 'a' }])
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('http://svc.test:8080/events?limit=50')
    expect((init as RequestInit).headers).toMatchObject({ 'X-API-Key': 'k1' })
  })

  it('omits the key header when none is set', async () => {
    const fetchMock = respond(200, { events: [] })
    vi.stubGlobal('fetch', fetchMock)
    await fetchEvents({ ...settings, apiKey: '' })
    expect((fetchMock.mock.calls[0][1] as RequestInit).headers).not.toHaveProperty('X-API-Key')
  })

  it.each([
    [401, 'unauthorized'],
    [503, 'unconfigured'],
    [400, 'rejected'],
    [500, 'server'],
  ])('maps HTTP %i to %s', async (status, kind) => {
    vi.stubGlobal('fetch', respond(status, { error: 'nope' }))
    await expect(fetchEvents(settings)).rejects.toMatchObject({ kind })
  })

  it('maps a network failure to unreachable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    const err = await fetchEvents(settings).catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.kind).toBe('unreachable')
  })

  it('lets an abort through untouched', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new DOMException('aborted', 'AbortError')))
    await expect(fetchEvents(settings)).rejects.toMatchObject({ name: 'AbortError' })
  })
})

describe('sendFeedback', () => {
  it('posts JSON with the key', async () => {
    const fetchMock = respond(202, { status: 'accepted' })
    vi.stubGlobal('fetch', fetchMock)

    await sendFeedback(settings, { eventId: 'e1', action: 'dismissed' })

    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('http://svc.test:8080/feedback')
    const req = init as RequestInit
    expect(req.method).toBe('POST')
    expect(JSON.parse(req.body as string)).toEqual({ eventId: 'e1', action: 'dismissed' })
    expect(req.headers).toMatchObject({ 'Content-Type': 'application/json', 'X-API-Key': 'k1' })
  })

  it('surfaces the service message for a rejected request', async () => {
    vi.stubGlobal('fetch', respond(400, { error: 'event id and action are required' }))
    const err = await sendFeedback(settings, { eventId: '', action: 'reply' }).catch((e) => e)
    expect(err.kind).toBe('rejected')
    expect(err.message).toBe('event id and action are required')
  })
})
