import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { LoginPage } from '../components/LoginPage'
import { authClientFor, DEMO_USER, type AuthClient, type Identity } from '../lib/auth'
import { useApp } from './AppState'

interface AuthState {
  /** Who is using the console. Null when the service could not be asked. */
  identity: Identity | null
  /** True for admins and for service-key access. */
  isAdmin: boolean
  client: AuthClient
  /** Ends the session and returns to the sign-in screen. */
  signOut: () => Promise<void>
}

const AuthContext = createContext<AuthState | null>(null)

const SERVICE_IDENTITY: Identity = { via: 'apikey', user: { id: 'service', email: 'API key', name: 'Service', role: 'admin' } }

type Phase = 'checking' | 'signedOut' | 'ready'

/**
 * Decides whether to show the sign-in screen. Demo mode and API-key access go
 * straight in. Otherwise the service is asked who the browser is: a person
 * signed in goes in, nobody signed in sees the sign-in screen, and any other
 * failure (service down) goes in so the screens can show their own connection
 * messages.
 */
export function AuthGate({ children }: { children: ReactNode }) {
  const { settings, welcome, invalidate, reload } = useApp()
  const client = useMemo(() => authClientFor(settings), [settings])
  const [phase, setPhase] = useState<Phase>('checking')
  const [identity, setIdentity] = useState<Identity | null>(null)

  const keyMode = settings.apiKey.trim() !== ''
  const noService = settings.backendUrl.trim() === ''

  useEffect(() => {
    if (welcome) {
      setPhase('ready')
      return
    }
    if (settings.demo) {
      setIdentity({ via: 'demo', user: DEMO_USER })
      setPhase('ready')
      return
    }
    if (keyMode || noService) {
      setIdentity(keyMode ? SERVICE_IDENTITY : null)
      setPhase('ready')
      return
    }
    const controller = new AbortController()
    setPhase('checking')
    client
      .me(controller.signal)
      .then((who) => {
        setIdentity(who)
        setPhase(who ? 'ready' : 'signedOut')
      })
      .catch((err) => {
        if (err instanceof DOMException && err.name === 'AbortError') return
        setIdentity(null)
        setPhase('ready')
      })
    return () => controller.abort()
  }, [welcome, settings.demo, keyMode, noService, client])

  const signedIn = useCallback(
    (who: Identity) => {
      setIdentity(who)
      setPhase('ready')
      // Lists that failed before sign-in load again now.
      invalidate()
      reload()
    },
    [invalidate, reload],
  )

  const signOut = useCallback(async () => {
    try {
      await client.logout()
    } finally {
      setIdentity(null)
      setPhase('signedOut')
    }
  }, [client])

  const value = useMemo<AuthState>(
    () => ({ identity, isAdmin: identity?.user.role === 'admin', client, signOut }),
    [identity, client, signOut],
  )

  if (phase === 'checking') return <div className="auth-checking" role="status" aria-live="polite" />
  if (phase === 'signedOut') return <LoginPage client={client} onSignedIn={signedIn} />
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthState {
  const state = useContext(AuthContext)
  if (!state) throw new Error('useAuth must be used inside AuthGate')
  return state
}
