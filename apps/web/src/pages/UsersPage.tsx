import { useState, type FormEvent } from 'react'
import { useAuth } from '../app/AuthGate'
import { useApp } from '../app/AppState'
import { useToast } from '../design/Toast'
import { LoadGate, useLoad } from '../features/manage/useLoad'
import { ApiError } from '../lib/api'
import { passwordProblem, type AuthUser, type NewUser, type Role } from '../lib/auth'
import { relativeTime } from '../lib/events'
import { useI18n } from '../lib/i18n/I18nContext'

const BLANK: NewUser = { email: '', name: '', role: 'member', password: '' }

/** Accounts, roles and the security audit trail (admins), and your own password (everyone with a password). */
export default function UsersPage() {
  const { t } = useI18n()
  const { identity, isAdmin, client, signOut } = useAuth()
  return (
    <section className="mpage" aria-labelledby="users-title">
      <div className="mpage-head">
        <div>
          <h1 className="page-title" id="users-title">
            {t('nav.users')}
          </h1>
          <p className="mpage-lede">{t('users.lede')}</p>
        </div>
      </div>
      {isAdmin ? <Accounts /> : <p className="panel panel-note">{t('users.adminOnly')}</p>}
      {identity?.via === 'session' && <OwnPassword onChanged={() => void signOut()} client={client} />}
      {isAdmin && <Audit />}
    </section>
  )
}

function message(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback
}

function Accounts() {
  const { t } = useI18n()
  const toast = useToast()
  const { client, identity } = useAuth()
  const { state, reload } = useLoad((signal) => client.users(signal), [client])
  const [draft, setDraft] = useState<NewUser>(BLANK)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const create = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await client.createUser({ ...draft, email: draft.email.trim(), name: draft.name.trim() })
      toast.push(t('users.created'), 'success')
      setDraft(BLANK)
      reload()
    } catch (err) {
      setError(message(err, t('users.createFailed')))
    } finally {
      setBusy(false)
    }
  }

  const update = async (u: AuthUser, patch: { role?: Role; disabled?: boolean; status?: 'active' | 'rejected' }) => {
    try {
      await client.updateUser(u.id, patch)
      reload()
    } catch (err) {
      toast.push(message(err, t('users.updateFailed')), 'error')
      reload()
    }
  }

  return (
    <>
      <div className="panel">
        <h2 className="panel-title">{t('users.accounts')}</h2>
        <LoadGate state={state} onRetry={reload}>
          {(users) => (
            <>
            {users.some((u) => u.status === 'pending') && (
              <div className="panel-note" role="region" aria-label={t('users.pending')}>
                <h3 className="panel-title">{t('users.pending')}</h3>
                <p className="form-help">{t('users.pendingNote')}</p>
                <ul className="plain-list">
                  {users
                    .filter((u) => u.status === 'pending')
                    .map((u) => (
                      <li key={u.id}>
                        <strong>{u.name || u.email}</strong>
                        {u.name && <span className="form-help"> {u.email}</span>}{' '}
                        <button type="button" className="button button-primary" onClick={() => void update(u, { status: 'active' })}>
                          {t('users.approve')}
                        </button>{' '}
                        <button type="button" className="button" onClick={() => void update(u, { status: 'rejected' })}>
                          {t('users.reject')}
                        </button>
                      </li>
                    ))}
                </ul>
              </div>
            )}
            <div className="table-wrap">
              <table className="data-table">
                <caption className="sr-only">{t('users.accounts')}</caption>
                <thead>
                  <tr>
                    <th scope="col">{t('users.person')}</th>
                    <th scope="col">{t('users.role')}</th>
                    <th scope="col">{t('users.state')}</th>
                  </tr>
                </thead>
                <tbody>
                  {users.map((u) => {
                    const self = u.email === identity?.user.email
                    return (
                      <tr key={u.id}>
                        <td className="wrap">
                          <strong>{u.name || u.email}</strong>
                          {u.name && <span className="form-help"> {u.email}</span>}
                        </td>
                        <td>
                          <select
                            className="field select"
                            aria-label={t('users.roleOf', { who: u.email })}
                            value={u.role}
                            onChange={(e) => void update(u, { role: e.target.value as Role })}
                          >
                            <option value="member">{t('users.role.member')}</option>
                            <option value="admin">{t('users.role.admin')}</option>
                          </select>
                        </td>
                        <td>
                          <button type="button" className="button" disabled={self && !u.disabled} onClick={() => void update(u, { disabled: !u.disabled })}>
                            {u.disabled ? t('users.enable') : t('users.disable')}
                          </button>
                          {u.disabled && <span className="status" data-state="warn"> {t('users.disabled')}</span>}
                          {u.status === 'rejected' && <span className="status" data-state="warn"> {t('users.rejected')}</span>}
                          {u.status === 'pending' && <span className="status" data-state="warn"> {t('users.waiting')}</span>}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
            </>
          )}
        </LoadGate>
        <p className="form-help">{t('users.lastAdmin')}</p>
      </div>

      <form className="panel" onSubmit={create} aria-labelledby="new-user-title" noValidate>
        <h2 className="panel-title" id="new-user-title">
          {t('users.new')}
        </h2>
        <div className="form-grid">
          <label>
            <span className="field-label">{t('auth.email')}</span>
            <input className="field" type="email" value={draft.email} onChange={(e) => setDraft({ ...draft, email: e.target.value })} required />
          </label>
          <label>
            <span className="field-label">{t('users.name')}</span>
            <input className="field" value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
          </label>
          <label>
            <span className="field-label">{t('users.role')}</span>
            <select className="field select" value={draft.role} onChange={(e) => setDraft({ ...draft, role: e.target.value as Role })}>
              <option value="member">{t('users.role.member')}</option>
              <option value="admin">{t('users.role.admin')}</option>
            </select>
          </label>
          <label>
            <span className="field-label">{t('users.startPassword')}</span>
            <input className="field" type="password" autoComplete="new-password" value={draft.password} onChange={(e) => setDraft({ ...draft, password: e.target.value })} />
            <span className="form-help">{t('users.startPasswordHelp')}</span>
          </label>
        </div>
        {error && (
          <p className="field-error" role="alert">
            {error}
          </p>
        )}
        <div className="form-actions">
          <button type="submit" className="button button-primary" disabled={busy || draft.email.trim() === ''}>
            {t('users.add')}
          </button>
        </div>
      </form>
    </>
  )
}

function OwnPassword({ client, onChanged }: { client: ReturnType<typeof useAuth>['client']; onChanged: () => void }) {
  const { t } = useI18n()
  const toast = useToast()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const problem = next === '' ? null : passwordProblem(next, confirm)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (passwordProblem(next, confirm)) return
    setBusy(true)
    setError('')
    try {
      await client.changePassword(current, next)
      toast.push(t('users.passwordChanged'), 'success')
      onChanged()
    } catch (err) {
      setError(message(err, t('users.passwordFailed')))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="panel" onSubmit={submit} aria-labelledby="pw-title" noValidate>
      <h2 className="panel-title" id="pw-title">
        {t('users.password')}
      </h2>
      <p className="panel-note">{t('users.passwordNote')}</p>
      <div className="form-grid">
        <label className="span-all">
          <span className="field-label">{t('users.currentPassword')}</span>
          <input className="field" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
        </label>
        <label>
          <span className="field-label">{t('users.newPassword')}</span>
          <input className="field" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} aria-invalid={problem === 'short'} />
        </label>
        <label>
          <span className="field-label">{t('users.confirmPassword')}</span>
          <input className="field" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} aria-invalid={problem === 'mismatch'} />
        </label>
      </div>
      {(problem || error) && (
        <p className="field-error" role="alert">
          {problem ? t(`users.problem.${problem}`) : error}
        </p>
      )}
      <div className="form-actions">
        <button type="submit" className="button" disabled={busy || current === '' || next === '' || problem !== null}>
          {t('users.changePassword')}
        </button>
      </div>
    </form>
  )
}

function Audit() {
  const { t, locale } = useI18n()
  const { now } = useApp()
  const { client } = useAuth()
  const { state, reload } = useLoad((signal) => client.audit(50, signal), [client], 30_000)
  return (
    <div className="panel">
      <h2 className="panel-title">{t('users.audit')}</h2>
      <p className="panel-note">{t('users.auditNote')}</p>
      <LoadGate state={state} onRetry={reload}>
        {(entries) =>
          entries.length === 0 ? (
            <p className="ledger-empty">{t('users.auditNone')}</p>
          ) : (
            <div className="table-wrap">
              <table className="data-table">
                <caption className="sr-only">{t('users.audit')}</caption>
                <thead>
                  <tr>
                    <th scope="col">{t('dl.when')}</th>
                    <th scope="col">{t('users.actor')}</th>
                    <th scope="col">{t('users.action')}</th>
                    <th scope="col">{t('dl.detail')}</th>
                  </tr>
                </thead>
                <tbody>
                  {entries.map((e, i) => (
                    <tr key={`${e.at}-${i}`}>
                      <td title={new Date(e.at).toLocaleString(locale)}>{relativeTime(e.at, now, locale, t)}</td>
                      <td className="wrap">{e.actor}</td>
                      <td className="mono">{e.action}</td>
                      <td className="wrap">{[e.target, e.detail, e.ip].filter(Boolean).join(' · ')}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )
        }
      </LoadGate>
    </div>
  )
}
