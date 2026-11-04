import { useEffect, useRef, useState } from 'react'
import { ApiError, sendFeedback, type EventSummary, type FeedbackAction, type Settings } from '../lib/api'
import { CLASS_EXPLANATIONS, CLASS_LABELS, eventTitle, statusLabel } from '../lib/events'
import { Flag } from './Flag'

interface EventSheetProps {
  event: EventSummary
  original?: EventSummary
  settings: Settings
  onOpenOriginal: (id: string) => void
  onClose: () => void
}

/** What the confirmation says. Each name matches the button that produced it. */
const DONE_TEXT: Record<FeedbackAction, string> = {
  dismissed: 'Dismissed.',
  always: 'You will always be notified about events like this.',
  never: 'You will never be notified about events like this.',
  undo: 'Undone.',
  reply: 'Reply sent.',
}

function feedbackError(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.kind === 'unauthorized') return 'The API key was rejected. Update it in connection settings, then try again.'
    if (err.kind === 'unreachable') return 'The service could not be reached. Check the connection and try again.'
    if (err.kind === 'rejected') return `${err.message} Check the entry and try again.`
  }
  return 'The service hit an error and did not save your feedback. Try again in a moment.'
}

export function EventSheet({ event, original, settings, onOpenOriginal, onClose }: EventSheetProps) {
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
      setError(feedbackError(err))
    } finally {
      setBusy(false)
    }
  }

  const critical = event.attentionClass === 'critical'
  const decision = event.decisionState

  return (
    <aside className="sheet" aria-labelledby="sheet-heading">
      <div className="sheet-bar">
        <button type="button" className="text-button" onClick={onClose}>
          Close
        </button>
      </div>

      <h2 id="sheet-heading" className="sheet-title" tabIndex={-1} ref={headingRef}>
        {eventTitle(event)}
      </h2>
      <p className="sheet-meta">
        <Flag kind={event.attentionClass} />
        {CLASS_LABELS[event.attentionClass]}. {statusLabel(decision?.status)}.{' '}
        <time dateTime={event.createdAt}>{new Date(event.createdAt).toLocaleString()}</time>
      </p>

      <h3 className="sheet-subtitle">Why this happened</h3>
      <p className="prose">{decision?.reason ? `${decision.reason.charAt(0).toUpperCase()}${decision.reason.slice(1)}.` : 'No reason was recorded.'}</p>
      {decision?.matchedRule && <p className="prose">Rule that matched: {decision.matchedRule}.</p>}
      <p className="prose">{CLASS_EXPLANATIONS[event.attentionClass]}</p>

      {event.mergedInto && (
        <p className="prose">
          This is a repeat of an earlier event.{' '}
          {original ? (
            <button type="button" className="text-button" onClick={() => onOpenOriginal(original.id)}>
              Open the original
            </button>
          ) : (
            'The original is older than the events shown here.'
          )}
        </p>
      )}

      <h3 className="sheet-subtitle">Tell the system</h3>
      <div className="actions">
        <button type="button" className="button" disabled={busy} onClick={() => submit('dismissed')}>
          Dismiss
        </button>
        <button type="button" className="button" disabled={busy} onClick={() => submit('always')}>
          Always notify me
        </button>
        {!critical && (
          <button type="button" className="button" disabled={busy} onClick={() => submit('never')}>
            Never notify me
          </button>
        )}
      </div>
      {critical && <p className="hint">Critical events can&rsquo;t be silenced, so there is no option to never be notified.</p>}

      <form
        className="reply"
        onSubmit={(e) => {
          e.preventDefault()
          if (reply.trim()) void submit('reply', reply.trim())
        }}
      >
        <label htmlFor="reply-text" className="field-label">
          Add a note
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
          Send note
        </button>
      </form>

      <div className="feedback-status" role="status">
        {last && last !== 'undo' && (
          <p>
            {DONE_TEXT[last]}{' '}
            <button type="button" className="text-button" disabled={busy} onClick={() => submit('undo', last)}>
              Undo
            </button>
          </p>
        )}
        {last === 'undo' && <p>{DONE_TEXT.undo}</p>}
      </div>
      {error && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}
    </aside>
  )
}
