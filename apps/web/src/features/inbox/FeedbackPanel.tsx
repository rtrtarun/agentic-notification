import { useEffect, useState } from 'react'
import { useApp } from '../../app/AppState'
import { ApiError, sendFeedback, type FeedbackAction, type FeedbackScope } from '../../lib/api'
import type { Translator } from '../../lib/i18n'
import { useI18n } from '../../lib/i18n/I18nContext'

function feedbackError(err: unknown, t: Translator): string {
  if (err instanceof ApiError) {
    if (err.kind === 'unauthorized') return t('fb.unauthorized')
    if (err.kind === 'unreachable') return t('fb.unreachable')
    if (err.kind === 'rejected') return t('fb.rejected', { message: err.message })
  }
  return t('fb.server')
}

/** "Was this right?" Dismiss, always notify, never notify, a note, and undo. These become training signals. */
export function FeedbackPanel({ eventId, critical }: { eventId: string; critical: boolean }) {
  const { t } = useI18n()
  const { settings } = useApp()
  const [last, setLast] = useState<FeedbackAction | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [reply, setReply] = useState('')
  const [scope, setScope] = useState<FeedbackScope>('type')
  const [signalOnly, setSignalOnly] = useState(false)

  // A different notification means a fresh conversation.
  useEffect(() => {
    setLast(null)
    setError(null)
    setReply('')
    setSignalOnly(false)
  }, [eventId])

  const submit = async (action: FeedbackAction, reason?: string) => {
    setBusy(true)
    setError(null)
    try {
      const result = await sendFeedback(settings, {
        eventId,
        action,
        reason,
        ...(action === 'always' || action === 'never' ? { scope } : {}),
      })
      setSignalOnly(result.overrideSkipped === true)
      setLast(action)
      if (action === 'reply') setReply('')
    } catch (err) {
      setError(feedbackError(err, t))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <ul className="choices">
        <li className="choice">
          <button type="button" className="button" disabled={busy} onClick={() => submit('dismissed')}>
            {t('act.dismiss')}
          </button>
          <span className="choice-hint">{t('act.dismiss.hint')}</span>
        </li>
        <li className="choice">
          <button type="button" className="button" disabled={busy} onClick={() => submit('always')}>
            {t('act.always')}
          </button>
          <span className="choice-hint">{t('act.always.hint')}</span>
        </li>
        {!critical && (
          <li className="choice">
            <button type="button" className="button" disabled={busy} onClick={() => submit('never')}>
              {t('act.never')}
            </button>
            <span className="choice-hint">{t('act.never.hint')}</span>
          </li>
        )}
      </ul>
      <label className="toolbar-field">
        <span>{t('ovr.scope')}</span>
        <select className="field select" value={scope} onChange={(e) => setScope(e.target.value as FeedbackScope)}>
          <option value="type">{t('ovr.scope.type')}</option>
          <option value="repository">{t('ovr.scope.repository')}</option>
          <option value="source">{t('ovr.scope.source')}</option>
        </select>
      </label>
      {critical && <p className="hint">{t('sheet.criticalHint')}</p>}

      <form
        className="reply"
        onSubmit={(e) => {
          e.preventDefault()
          if (reply.trim()) void submit('reply', reply.trim())
        }}
      >
        <label htmlFor="reply-text" className="field-label">
          {t('sheet.note')}
        </label>
        <textarea id="reply-text" className="field" rows={3} value={reply} maxLength={500} onChange={(e) => setReply(e.target.value)} />
        <button type="submit" className="button" disabled={busy || !reply.trim()}>
          {t('sheet.sendNote')}
        </button>
      </form>

      <div className="feedback-status" role="status">
        {last && last !== 'undo' && (
          <p>
            {t(`done.${last}`)}{' '}
            <button type="button" className="text-button" disabled={busy} onClick={() => submit('undo', last)}>
              {t('act.undo')}
            </button>
          </p>
        )}
        {last === 'undo' && <p>{t('done.undo')}</p>}
        {signalOnly && (last === 'always' || last === 'never') && <p className="hint">{t('ovr.signalOnly')}</p>}
      </div>
      {error && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}
    </>
  )
}
