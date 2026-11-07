import { useState, type FormEvent } from 'react'
import { passwordProblem, signupFailure, type AuthClient } from '../lib/auth'
import { useI18n } from '../lib/i18n/I18nContext'

interface SignUpFormProps {
  client: AuthClient
  onBack: () => void
}

/**
 * Ask for an account. The account starts pending: nobody can sign in or see
 * anything until an admin approves it, and the answer never says whether the
 * email already had an account.
 */
export function SignUpForm({ client, onBack }: SignUpFormProps) {
  const { t } = useI18n()
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [done, setDone] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (busy) return
    const problem = passwordProblem(password, confirm)
    if (problem) {
      setError(t(`users.problem.${problem}`))
      return
    }
    setBusy(true)
    setError('')
    try {
      await client.signup(email.trim(), name.trim(), password)
      setDone(true)
    } catch (err) {
      const failure = signupFailure(err)
      setError(failure.key === 'invalid' ? failure.detail : t(`signup.error.${failure.key}`))
      setPassword('')
      setConfirm('')
    } finally {
      setBusy(false)
    }
  }

  if (done) {
    return (
      <>
        <h1 id="login-title" className="welcome-title">
          {t('signup.doneTitle')}
        </h1>
        <p className="lead-detail" role="status">
          {t('signup.done')}
        </p>
        <div className="form-actions">
          <button type="button" className="button button-primary" onClick={onBack}>
            {t('signup.backToSignIn')}
          </button>
        </div>
      </>
    )
  }

  return (
    <>
      <h1 id="login-title" className="welcome-title">
        {t('signup.title')}
      </h1>
      <p className="lead-detail">{t('signup.lede')}</p>
      <form onSubmit={submit} className="form-grid" noValidate>
        <label className="span-all">
          <span className="field-label">{t('users.name')}</span>
          <input className="field" type="text" autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} autoFocus />
        </label>
        <label className="span-all">
          <span className="field-label">{t('auth.email')}</span>
          <input className="field" type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required />
        </label>
        <label className="span-all">
          <span className="field-label">{t('auth.password')}</span>
          <input className="field" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
          <span className="form-help">{t('signup.passwordHelp')}</span>
        </label>
        <label className="span-all">
          <span className="field-label">{t('users.confirmPassword')}</span>
          <input className="field" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
        </label>
        {error && (
          <p className="field-error span-all" role="alert">
            {error}
          </p>
        )}
        <div className="form-actions span-all">
          <button type="submit" className="button button-primary" disabled={busy || email.trim() === '' || password === '' || confirm === ''}>
            {busy ? t('signup.sending') : t('signup.submit')}
          </button>
          <button type="button" className="button" onClick={onBack}>
            {t('signup.backToSignIn')}
          </button>
        </div>
      </form>
    </>
  )
}
