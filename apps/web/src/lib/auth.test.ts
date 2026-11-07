import { afterEach, describe, expect, it } from 'vitest'
import { ApiError, request, setCsrfToken } from './api'
import { demoAuthClient, httpAuth, loginFailureKey, passwordProblem, signupFailure, ssoOutcome } from './auth'

const settings = { backendUrl: 'http://svc', apiKey: '' }

interface Call {
  url: string
  method: string
  headers: Record<string, string>
  credentials?: RequestCredentials
}

function stubFetch(reply: (call: Call) => Response): Call[] {
  const calls: Call[] = []
  globalThis.fetch = (async (url: string, init?: RequestInit) => {
    const call: Call = {
      url: String(url),
      method: init?.method ?? 'GET',
      headers: { ...(init?.headers as Record<string, string>) },
      credentials: init?.credentials,
    }
    calls.push(call)
    return reply(call)
  }) as typeof fetch
  return calls
}

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status })
const realFetch = globalThis.fetch

afterEach(() => {
  globalThis.fetch = realFetch
  setCsrfToken('')
})

describe('sign-in helpers', () => {
  it('words every failed sign-in the same way, except a lock-out or an unreachable service', () => {
    expect(loginFailureKey(new ApiError('rejected', 'incorrect email or password'))).toBe('invalid')
    expect(loginFailureKey(new ApiError('unauthorized', 'The API key was rejected.'))).toBe('invalid')
    expect(loginFailureKey(new ApiError('rejected', 'too many failed sign-ins; try again later'))).toBe('locked')
    expect(loginFailureKey(new ApiError('unreachable', 'down'))).toBe('unreachable')
    expect(loginFailureKey(new Error('x'))).toBe('invalid')
  })

  it('reads the single sign-on outcome from the address and ignores anything else', () => {
    expect(ssoOutcome('?signin=denied')).toBe('denied')
    expect(ssoOutcome('?a=1&signin=failed')).toBe('failed')
    expect(ssoOutcome('?signin=<script>')).toBeNull()
    expect(ssoOutcome('')).toBeNull()
  })

  it('checks a new password before asking the service', () => {
    expect(passwordProblem('short', 'short')).toBe('short')
    expect(passwordProblem('a long enough password', 'a different one entirely')).toBe('mismatch')
    expect(passwordProblem('a long enough password', 'a long enough password')).toBeNull()
  })
})

describe('http auth client', () => {
  it('sends cookies with every call', async () => {
    const calls = stubFetch(() => json({ ok: true }))
    await request(settings, '/api/anything')
    expect(calls[0].credentials).toBe('include')
  })

  it('sign-in stores the CSRF token and later writes carry it, but reads do not', async () => {
    const me = { via: 'session', user: { id: '1', email: 'a@b.c', name: '', role: 'admin' }, csrfToken: 'tok-1' }
    const calls = stubFetch((c) => (c.url.endsWith('/login') ? json({ user: me.user, csrfToken: 'tok-1' }) : c.url.endsWith('/api/auth/me') ? json(me) : json({})))
    const who = await httpAuth(settings).login('a@b.c', 'pw')
    expect(who).toEqual({ via: 'session', user: { id: '1', email: 'a@b.c', name: '', role: 'admin' } })

    await request(settings, '/api/preferences', { method: 'PUT', body: '{}' })
    await request(settings, '/api/preferences')
    const put = calls.find((c) => c.method === 'PUT')!
    const get = calls.find((c) => c.method === 'GET')!
    expect(put.headers['X-CSRF-Token']).toBe('tok-1')
    expect(get.headers['X-CSRF-Token']).toBeUndefined()
  })

  it('never sends the CSRF token on the sign-in call itself or before signing in', async () => {
    const user = { id: '1', email: 'a@b.c', name: '', role: 'member' }
    const calls = stubFetch((c) => (c.url.endsWith('/api/auth/me') ? json({ via: 'session', user, csrfToken: 't' }) : json({ user, csrfToken: 't' })))
    await httpAuth(settings).login('a@b.c', 'pw')
    expect(calls[0].headers['X-CSRF-Token']).toBeUndefined()
    expect(JSON.stringify(calls[0])).not.toContain('"t"')
  })

  it('me() returns the identity and keeps the token; a 401 means nobody is signed in', async () => {
    stubFetch(() => json({ via: 'session', user: { id: '1', email: 'a@b.c', name: 'A', role: 'member' }, csrfToken: 'tok-2' }))
    expect((await httpAuth(settings).me())?.user.email).toBe('a@b.c')
    const calls = stubFetch(() => json({}))
    await request(settings, '/x', { method: 'POST', body: '{}' })
    expect(calls[0].headers['X-CSRF-Token']).toBe('tok-2')

    stubFetch(() => json({ error: 'missing or invalid api key' }, 401))
    expect(await httpAuth(settings).me()).toBeNull()
    const after = stubFetch(() => json({}))
    await request(settings, '/x', { method: 'POST', body: '{}' })
    expect(after[0].headers['X-CSRF-Token']).toBeUndefined()
  })

  it('me() lets other failures through so the screens can explain them', async () => {
    stubFetch(() => json({}, 500))
    await expect(httpAuth(settings).me()).rejects.toBeInstanceOf(ApiError)
    globalThis.fetch = (async () => {
      throw new TypeError('network')
    }) as typeof fetch
    await expect(httpAuth(settings).me()).rejects.toMatchObject({ kind: 'unreachable' })
  })

  it('logout and a password change forget the token even when the call fails', async () => {
    setCsrfToken('tok-3')
    stubFetch(() => json({}, 500))
    await expect(httpAuth(settings).logout()).rejects.toBeInstanceOf(ApiError)
    const calls = stubFetch(() => json({}))
    await request(settings, '/x', { method: 'POST', body: '{}' })
    expect(calls[0].headers['X-CSRF-Token']).toBeUndefined()
  })

  it('uses the documented endpoints', async () => {
    const calls = stubFetch(() => json({ users: [], entries: [], id: 'u', email: '', name: '', role: 'member' }))
    const c = httpAuth(settings)
    await c.users()
    await c.createUser({ email: 'a@b.c', name: '', role: 'member', password: 'x'.repeat(12) })
    await c.updateUser('a/b', { disabled: true })
    await c.audit(25)
    await c.changePassword('old', 'new')
    expect(calls.map((x) => `${x.method} ${x.url.replace('http://svc', '')}`)).toEqual([
      'GET /api/users',
      'POST /api/users',
      'PATCH /api/users/a%2Fb',
      'GET /api/audit?limit=25',
      'POST /api/auth/password',
    ])
  })
})

describe('demo auth client', () => {
  it('shows an admin, lists sample accounts and applies changes in memory', async () => {
    const c = demoAuthClient()
    expect((await c.me())?.user.role).toBe('admin')
    const created = await c.createUser({ email: 'new@example.com', name: 'New', role: 'member', password: '' })
    expect((await c.users()).some((u) => u.id === created.id)).toBe(true)
    const off = await c.updateUser(created.id, { disabled: true, role: 'admin' })
    expect(off).toMatchObject({ disabled: true, role: 'admin' })
    expect((await c.audit(10)).length).toBeGreaterThan(0)
    expect((await c.status()).sso.enabled).toBe(false)
  })
})

describe('sign-up and the new sign-in outcomes', () => {
  it('words a pending, a rejected and a blocked-cookie sign-in each in its own way', () => {
    expect(loginFailureKey(new ApiError('rejected', 'your account is waiting for approval', 'pending'))).toBe('pending')
    expect(loginFailureKey(new ApiError('rejected', 'your sign-up was not approved', 'rejected'))).toBe('rejected')
    expect(loginFailureKey(new ApiError('unauthorized', 'cookie', 'cookie'))).toBe('cookie')
    // A wrong password stays generic, so a pending state is never revealed without the right password.
    expect(loginFailureKey(new ApiError('unauthorized', 'incorrect email or password'))).toBe('invalid')
  })

  it('a sign-in the browser does not keep is reported, not looped back to the sign-in page', async () => {
    const user = { id: '1', email: 'a@b.c', name: '', role: 'member' }
    stubFetch((c) => (c.url.endsWith('/login') ? json({ user, csrfToken: 't' }) : json({ error: 'missing or invalid api key' }, 401)))
    await expect(httpAuth(settings).login('a@b.c', 'pw')).rejects.toMatchObject({ code: 'cookie' })
    // No token is kept for a session that does not exist.
    const after = stubFetch(() => json({}))
    await request(settings, '/x', { method: 'POST', body: '{}' })
    expect(after[0].headers['X-CSRF-Token']).toBeUndefined()
  })

  it('a pending account is refused with the service code, with no session', async () => {
    stubFetch(() => json({ error: 'your account is waiting for approval', code: 'pending' }, 403))
    const err = await httpAuth(settings)
      .login('new@b.c', 'pw')
      .catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(loginFailureKey(err)).toBe('pending')
  })

  it('sign-up posts the three fields without the API key or a CSRF token and returns nothing', async () => {
    const calls = stubFetch(() => json({ status: 'pending' }, 202))
    await expect(httpAuth(settings).signup('new@b.c', 'New', 'a long enough password')).resolves.toBeUndefined()
    expect(calls[0].url).toBe('http://svc/api/auth/signup')
    expect(calls[0].method).toBe('POST')
    expect(JSON.parse(String((calls[0] as unknown as { body?: string }).body ?? '{}') || '{}')).toBeTypeOf('object')
    expect(calls[0].headers['X-CSRF-Token']).toBeUndefined()
    expect(calls[0].headers['X-API-Key']).toBeUndefined()
  })

  it('explains why a sign-up failed', () => {
    expect(signupFailure(new ApiError('unreachable', 'down'))).toEqual({ key: 'unreachable' })
    expect(signupFailure(new ApiError('rejected', 'too many sign-up requests; try again later'))).toEqual({ key: 'tooMany' })
    expect(signupFailure(new ApiError('rejected', 'sign-up is not available'))).toEqual({ key: 'disabled' })
    expect(signupFailure(new ApiError('rejected', 'invalid input: a valid email is required'))).toEqual({ key: 'invalid', detail: 'invalid input: a valid email is required' })
    expect(signupFailure(new Error('x'))).toEqual({ key: 'failed' })
  })

  it('the demo lets you try sign-up and approve a pending request', async () => {
    const demo = demoAuthClient()
    expect((await demo.status()).signup).toBe(true)
    await expect(demo.signup('x@y.z', 'X', 'a long enough password')).resolves.toBeUndefined()
    const pending = (await demo.users()).find((u) => u.status === 'pending')!
    expect(pending).toBeDefined()
    expect((await demo.updateUser(pending.id, { status: 'active' })).status).toBe('active')
    expect((await demo.users()).some((u) => u.status === 'pending')).toBe(false)
  })
})
