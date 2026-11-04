import { useEffect, useRef, useState } from 'react'
import { ApiError, sendFeedback, type EventSummary, type FeedbackAction, type Settings } from '../lib/api'
import { classExplanation, classLabel, eventTitle, reasonText, statusLabel } from '../lib/events'
import { useI18n } from '../lib/i18n/I18nContext'
import type { Translator } from '../lib/i18n'
import { Chip } from './Chip'
import { Flag } from './Flag'

interface EventSheetProps {
  event: EventSummary
  original?: EventSummary
  settings: Settings
  onOpenOriginal: (id: string) => void
  onClose: () => void
}

function feedbackError(err: unknown, t: Translator): string {
  if (err instanceof ApiError) {
    if (err.kind === 'unauthorized') return t('fb.unauthorized')
    if (err.kind === 'unreachable') return t('fb.unreachable')
    if (err.kind === 'rejected') return t('fb.rejected', { message: err.message })
  }
  return t('fb.server')
}

export function EventSheet({ event, original, settings, onOpenOriginal, onClose }: EventSheetProps) {
  const { t, tag } = useI18n()
  const headingRef = useRef<HTMLHeadingElement>(null)
  const [last, setLast] = useState<FeedbackAction | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [reply, setReply] = useState('')

  // Move focus into the sheet when it opens and give it back when it closes.
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null
    headingRef.current?.focus()
    return () => opener?.focus()
  }, [])

  // A different event means a fresh conversation.
  useEffect(() => {
    setLast(null)
    setError(null)
    setReply('')
    headingRef.current?.focus()
  }, [event.id])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  const submit = async (action: FeedbackAction, reason?: string) => {
    setBusy(true)
    setError(null)
    try {
      await sendFeedback(settings, { eventId: event.id, action, reason })
      setLast(action)
      if (action === 'reply') setReply('')
    } catch (err) {
      setError(feedbackError(err, t))
    } finally {
      setBusy(false)
    }
  }

  const critical = event.attentionClass === 'critical'
  const decision = event.decisionState
  const reason = reasonText(decision?.reason, t)

  return (
    <aside className="sheet" aria-labelledby="sheet-heading">
      <div className="sheet-bar">
        <button type="button" className="text-button" onClick={onClose}>
          {t('sheet.close')}
        </button>
      </div>

      <h2 id="sheet-heading" className="sheet-title" tabIndex={-1} ref={headingRef}>
        {eventTitle(event, t)}
      </h2>
      <p className="sheet-meta">
        <Flag kind={event.attentionClass} />
        {classLabel(event.attentionClass, t)}. {statusLabel(decision?.status, t)}.{' '}
        <Chip eventType={event.eventType} />{' '}
        <time dateTime={event.createdAt}>{new Date(event.createdAt).toLocaleString(tag)}</time>
      </p>

      <h3 className="sheet-subtitle">{t('sheet.why')}</h3>
      <p className="prose">{reason ? `${reason}.` : t('sheet.noReason')}</p>
      {decision?.matchedRule && <p className="prose">{t('sheet.rule', { rule: decision.matchedRule })}</p>}
      <p className="prose">{classExplanation(event.attentionClass, t)}</p>

      {event.mergedInto && (
        <p className="prose">
          {t('sheet.repeat')}{' '}
          {original ? (
            <button type="button" className="text-button" onClick={() => onOpenOriginal(original.id)}>
              {t('sheet.openOriginal')}
            </button>
          ) : (
            t('sheet.originalOld')
          )}
        </p>
      )}

      <h3 className="sheet-subtitle">{t('sheet.right')}</h3>
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
        <textarea
          id="reply-text"
          className="field"
          rows={3}
          value={reply}
          maxLength={500}
          onChange={(e) => setReply(e.target.value)}
        />
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
      </div>
      {error && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}
    </aside>
  )
}
