import { useEffect, useState, type FormEvent } from 'react'
import { useApp } from '../app/AppState'
import { loginFailureKey, ssoOutcome, type AuthClient, type AuthStatus, type Identity } from '../lib/auth'
import { useI18n } from '../lib/i18n/I18nContext'
import { Appearance } from './Appearance'

interface LoginPageProps {
  client: AuthClient
  onSignedIn: (who: Identity) => void
}

/** Sign in with an email and password, or with the company's single sign-on when the service offers it. */
export function LoginPage({ client, onSignedIn }: LoginPageProps) {
  const { t } = useI18n()
  const { settings, openSettings } = useApp()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<'locked' | 'invalid' | 'unreachable' | null>(null)
  const [status, setStatus] = useState<AuthStatus | null>(null)
  const ssoProblem = ssoOutcome(window.location.search)

  useEffect(() => {
    const controller = new AbortController()
    client
      .status(controller.signal)
      .then(setStatus)
      .catch(() => undefined) // the form still works; only the single sign-on button is missing
    return () => controller.abort()
  }, [client])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      onSignedIn(await client.login(email.trim(), password))
    } catch (err) {
      setError(loginFailureKey(err))
      setPassword('')
    } finally {
      setBusy(false)
    }
  }

  const ssoUrl = status?.sso.enabled && status.sso.startPath ? settings.backendUrl.replace(/\/+$/, '') + status.sso.startPath : null

  return (
    <div className="welcome-page">
      <header className="welcome-top">
        <p className="wordmark">{t('app.name')}</p>
        <Appearance />
      </header>
      <main className="welcome auth-card" aria-labelledby="login-title">
        <h1 id="login-title" className="welcome-title">
          {t('auth.title')}
        </h1>
        <p className="lead-detail">{t('auth.lede')}</p>

        {ssoProblem && (
          <p className="field-error" role="alert">
            {t(`auth.sso.${ssoProblem}`)}
          </p>
        )}
        {status?.needsSetup && <p className="panel-note">{t('auth.needsSetup')}</p>}

        {ssoUrl && (
          <p>
            <a className="button button-primary" href={ssoUrl}>
              {t('auth.sso', { name: status?.sso.name ?? '' })}
            </a>
          </p>
        )}

        <form onSubmit={submit} className="form-grid" noValidate>
          <label className="span-all">
            <span className="field-label">{t('auth.email')}</span>
            <input className="field" type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} autoFocus required />
          </label>
          <label className="span-all">
            <span className="field-label">{t('auth.password')}</span>
            <input className="field" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
          </label>
          {error && (
            <p className="field-error span-all" role="alert">
              {t(`auth.error.${error}`)}
            </p>
          )}
          <div className="form-actions span-all">
            <button type="submit" className="button button-primary" disabled={busy || email.trim() === '' || password === ''}>
              {busy ? t('auth.signingIn') : t('auth.signIn')}
            </button>
            <button type="button" className="button" onClick={openSettings}>
              {t('auth.useKey')}
            </button>
          </div>
        </form>
        <p className="hint">{t('auth.hint')}</p>
      </main>
    </div>
  )
}
