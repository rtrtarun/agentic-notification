// Accounts and sign-in. The browser holds only an HttpOnly session cookie that
// scripts cannot read, plus an in-memory CSRF token. The service answers sign-in
// errors identically for every cause, and so does this module.

import { ApiError, request, setCsrfToken, type Settings } from './api'

export type Role = 'admin' | 'member'

/** An account is pending until an admin approves it; a rejected sign-up never gets in. */
export type AccountStatus = 'active' | 'pending' | 'rejected'

export interface AuthUser {
  id: string
  email: string
  name: string
  role: Role
  disabled?: boolean
  status?: AccountStatus
}

export interface Identity {
  /** "session" for a person, "apikey" for a service key, "demo" for sample data. */
  via: 'session' | 'apikey' | 'demo'
  user: AuthUser
}

export interface AuthStatus {
  needsSetup: boolean
  /** True when people may ask for an account (an admin approves it). */
  signup?: boolean
  sso: { enabled: boolean; name?: string; startPath?: string }
}

export interface AuditEntry {
  at: string
  actor: string
  action: string
  target?: string
  ip?: string
  detail?: string
}

export interface NewUser {
  email: string
  name: string
  role: Role
  password: string
}

export interface UserPatch {
  name?: string
  role?: Role
  disabled?: boolean
  password?: string
  /** "active" approves a pending sign-up; "rejected" refuses it. */
  status?: 'active' | 'rejected'
}

export interface AuthClient {
  status(signal?: AbortSignal): Promise<AuthStatus>
  /** The signed-in identity, or null when nobody is signed in. Other failures throw. */
  me(signal?: AbortSignal): Promise<Identity | null>
  login(email: string, password: string): Promise<Identity>
  /** Ask for an account. It starts pending; an admin must approve it before it can sign in. */
  signup(email: string, name: string, password: string): Promise<void>
  logout(): Promise<void>
  changePassword(current: string, next: string): Promise<void>
  users(signal?: AbortSignal): Promise<AuthUser[]>
  createUser(u: NewUser): Promise<AuthUser>
  updateUser(id: string, patch: UserPatch): Promise<AuthUser>
  audit(limit: number, signal?: AbortSignal): Promise<AuditEntry[]>
}

export const DEMO_USER: AuthUser = { id: 'demo', email: 'demo@example.com', name: 'Demo user', role: 'admin' }

const DEMO_USERS: AuthUser[] = [
  DEMO_USER,
  { id: 'u2', email: 'sam@example.com', name: 'Sam Lee', role: 'member' },
  { id: 'u3', email: 'former@example.com', name: 'Former Teammate', role: 'member', disabled: true },
  { id: 'u4', email: 'newhire@example.com', name: 'New Hire', role: 'member', status: 'pending' },
]

export type LoginFailure = 'locked' | 'invalid' | 'unreachable' | 'pending' | 'rejected' | 'cookie'

/**
 * Plain-language reason a sign-in failed. The same wording for a wrong email and a
 * wrong password. "pending" and "rejected" only follow a correct password, and
 * "cookie" means the sign-in worked but the browser did not keep the session.
 */
export function loginFailureKey(err: unknown): LoginFailure {
  if (err instanceof ApiError) {
    if (err.code === 'pending') return 'pending'
    if (err.code === 'rejected') return 'rejected'
    if (err.code === 'cookie') return 'cookie'
    if (/too many failed/i.test(err.message)) return 'locked'
    if (err.kind === 'unreachable') return 'unreachable'
  }
  return 'invalid'
}

export type SignupFailure = { key: 'disabled' | 'tooMany' | 'unreachable' | 'failed' } | { key: 'invalid'; detail: string }

/** Why a sign-up request failed, in terms the sign-up form can word. */
export function signupFailure(err: unknown): SignupFailure {
  if (err instanceof ApiError) {
    if (err.kind === 'unreachable') return { key: 'unreachable' }
    if (/too many/i.test(err.message)) return { key: 'tooMany' }
    if (/not available/i.test(err.message)) return { key: 'disabled' }
    if (err.kind === 'rejected') return { key: 'invalid', detail: err.message }
  }
  return { key: 'failed' }
}

/** What the redirect back from single sign-on says, from the address query. */
export function ssoOutcome(search: string): 'denied' | 'failed' | null {
  const v = new URLSearchParams(search).get('signin')
  return v === 'denied' || v === 'failed' ? v : null
}

/** A problem with a new password the person can fix before asking the service. */
export function passwordProblem(next: string, confirm: string): 'short' | 'mismatch' | null {
  if ([...next].length < 12) return 'short'
  return next === confirm ? null : 'mismatch'
}

export function demoAuthClient(): AuthClient {
  let users = DEMO_USERS.map((u) => ({ ...u }))
  return {
    async status() {
      return { needsSetup: false, signup: true, sso: { enabled: false } }
    },
    async me() {
      return { via: 'demo', user: DEMO_USER }
    },
    async login() {
      return { via: 'demo', user: DEMO_USER }
    },
    async signup() {},
    async logout() {},
    async changePassword() {},
    async users() {
      return users
    },
    async createUser(u) {
      const created: AuthUser = { id: `u${users.length + 1}`, email: u.email, name: u.name, role: u.role }
      users = [...users, created]
      return created
    },
    async updateUser(id, patch) {
      users = users.map((u) => {
        if (u.id !== id) return u
        const next = { ...u }
        if (patch.name !== undefined) next.name = patch.name
        if (patch.role) next.role = patch.role
        if (patch.disabled !== undefined) next.disabled = patch.disabled
        if (patch.status) next.status = patch.status
        return next
      })
      return users.find((u) => u.id === id) as AuthUser
    },
    async audit() {
      const at = (m: number) => new Date(Date.now() - m * 60_000).toISOString()
      return [
        { at: at(3), actor: 'sam@example.com', action: 'login', detail: 'password' },
        { at: at(40), actor: 'demo@example.com', action: 'user.updated', target: 'u3', detail: 'disabled=true' },
        { at: at(95), actor: 'unknown@example.com', action: 'login.failed' },
      ]
    },
  }
}

export function httpAuth(settings: Settings): AuthClient {
  const json = async (res: Response) => res.json()
  const me = async (signal?: AbortSignal): Promise<Identity | null> => {
    try {
      const body = (await json(await request(settings, '/api/auth/me', { signal }))) as { via: Identity['via']; user: AuthUser; csrfToken?: string }
      setCsrfToken(body.csrfToken ?? '')
      return { via: body.via, user: body.user }
    } catch (err) {
      if (err instanceof ApiError && err.kind === 'unauthorized') {
        setCsrfToken('')
        return null
      }
      throw err
    }
  }
  return {
    async status(signal) {
      return (await json(await request(settings, '/api/auth/status', { signal }))) as AuthStatus
    },
    me,
    async login(email, password) {
      const body = (await json(await request(settings, '/api/auth/login', { method: 'POST', body: JSON.stringify({ email, password }) }))) as {
        user: AuthUser
        csrfToken: string
      }
      setCsrfToken(body.csrfToken)
      // A sign-in the browser does not keep (a cookie from another site, blocked) would
      // silently return to the sign-in page. Check once, and say so.
      const who = await me()
      if (!who) throw new ApiError('unauthorized', 'The browser did not keep the session cookie.', 'cookie')
      return who
    },
    async signup(email, name, password) {
      await request(settings, '/api/auth/signup', { method: 'POST', body: JSON.stringify({ email, name, password }) })
    },
    async logout() {
      try {
        await request(settings, '/api/auth/logout', { method: 'POST' })
      } finally {
        setCsrfToken('')
      }
    },
    async changePassword(current, next) {
      await request(settings, '/api/auth/password', { method: 'POST', body: JSON.stringify({ current, next }) })
      setCsrfToken('')
    },
    async users(signal) {
      return ((await json(await request(settings, '/api/users', { signal }))) as { users: AuthUser[] }).users
    },
    async createUser(u) {
      return (await json(await request(settings, '/api/users', { method: 'POST', body: JSON.stringify(u) }))) as AuthUser
    },
    async updateUser(id, patch) {
      return (await json(await request(settings, `/api/users/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(patch) }))) as AuthUser
    },
    async audit(limit, signal) {
      return ((await json(await request(settings, `/api/audit?limit=${limit}`, { signal }))) as { entries: AuditEntry[] }).entries
    },
  }
}

export function authClientFor(settings: Settings): AuthClient {
  return settings.demo ? demoAuthClient() : httpAuth(settings)
}
