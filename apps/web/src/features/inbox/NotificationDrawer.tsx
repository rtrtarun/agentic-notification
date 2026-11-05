import { ExplainButton } from './ExplainButton'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useApp } from '../../app/AppState'
import { Chip } from '../../components/Chip'
import { useToast } from '../../design/Toast'
import { classExplanation, eventTitle, reasonText, relativeTime } from '../../lib/events'
import { useI18n } from '../../lib/i18n/I18nContext'
import type { Action, InboxDetail } from '../../lib/inbox'
import { LEVELS } from '../../lib/priority'
import { PriorityBadge, SourceBadge, StatusBadge } from './badges'
import { EventTimeline } from './EventTimeline'
import { FeedbackPanel } from './FeedbackPanel'
import { copyNotificationLink } from './link'

type Load = { status: 'loading' } | { status: 'error' } | { status: 'ready'; detail: InboxDetail }

const PRIORITY_CHOICES = LEVELS.filter((l) => l !== 'critical')

/**
 * The right-hand drawer (full screen on small displays): what the notification
 * is, why you are seeing it, how it was decided, what happened to it, what is
 * related, and what you can do about it.
 */
export function NotificationDrawer({ id }: { id: string }) {
  const { t, tag } = useI18n()
  const { client, version, invalidate, closeEvent, selectEvent, now } = useApp()
  const toast = useToast()
  const headingRef = useRef<HTMLHeadingElement>(null)
  const [load, setLoad] = useState<Load>({ status: 'loading' })
  const [busy, setBusy] = useState(false)
  const [attempt, setAttempt] = useState(0)

  // Move focus into the drawer when it opens and give it back when it closes.
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null
    return () => opener?.focus()
  }, [])
  useEffect(() => headingRef.current?.focus(), [id])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') closeEvent()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [closeEvent])

  useEffect(() => {
    const controller = new AbortController()
    client
      .get(id, controller.signal)
      .then((detail) => setLoad({ status: 'ready', detail }))
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === 'AbortError') return
        setLoad({ status: 'error' })
      })
    return () => controller.abort()
  }, [client, id, version, attempt])

  const act = useCallback(
    async (action: Action) => {
      setBusy(true)
      try {
        await client.act(id, action)
        invalidate()
      } catch (err) {
        toast.push(t('inbox.failed', { message: err instanceof Error ? err.message : '' }), 'error')
      } finally {
        setBusy(false)
      }
    },
    [client, id, invalidate, t, toast],
  )

  const copy = async () => {
    if (await copyNotificationLink(id)) toast.push(t('toast.linkCopied'), 'success')
    else toast.push(t('toast.copyFailed'), 'error')
  }

  const shell = (children: React.ReactNode, title?: string) => (
    <aside className="sheet drawer" aria-labelledby="drawer-heading" aria-label={title ? undefined : t('drawer.label')}>
      <div className="sheet-bar">
        <button type="button" className="text-button" onClick={closeEvent}>
          {t('sheet.close')}
        </button>
      </div>
      <h2 id="drawer-heading" className="sheet-title" tabIndex={-1} ref={headingRef}>
        {title ?? t('drawer.label')}
      </h2>
      {children}
    </aside>
  )

  if (load.status === 'loading') return shell(<p className="loading" role="status">{t('drawer.loading')}</p>)
  if (load.status === 'error') {
    return shell(
      <div role="alert">
        <p className="error-text">{t('drawer.loadFailed')}</p>
        <button type="button" className="button" onClick={() => setAttempt((n) => n + 1)}>
          {t('drawer.retry')}
        </button>
      </div>,
    )
  }

  const { item, trail, related } = load.detail
  const critical = item.attentionClass === 'critical'
  const decision = item.decisionState
  const reason = reasonText(decision?.reason, t)
  const when = (iso: string) => new Date(iso).toLocaleString(tag)

  return shell(
    <>
      <p className="sheet-meta">
        <PriorityBadge level={item.priority} /> <StatusBadge outcome={item.outcome} /> <SourceBadge source={item.source} />{' '}
        <Chip eventType={item.eventType} />
      </p>

      <h3 className="sheet-subtitle">{t('drawer.why')}</h3>
      <p className="prose">{reason ? `${reason}.` : t('sheet.noReason')}</p>
      <p className="prose">{classExplanation(item.attentionClass, t)}</p>
      <ExplainButton eventId={item.id} />

      <h3 className="sheet-subtitle">{t('drawer.decision')}</h3>
      <dl className="facts">
        <dt>{t('drawer.rule')}</dt>
        <dd>{decision?.matchedRule || t('drawer.noRule')}</dd>
        <dt>{t('drawer.priority')}</dt>
        <dd>
          {t(`level.${item.priority}`)}
          {item.priorityOverride && <> ({t('drawer.priorityChanged')})</>}
        </dd>
        <dt>{t('drawer.dedup')}</dt>
        <dd>
          {item.mergedInto ? t('sheet.repeat') : item.repeatCount > 0 ? t('drawer.repeatsMerged', { n: item.repeatCount }) : t('drawer.notRepeat')}
        </dd>
        <dt>{t('drawer.status')}</dt>
        <dd>{t(`outcome.${item.outcome}`)}</dd>
      </dl>

      <h3 className="sheet-subtitle">{t('drawer.details')}</h3>
      <dl className="facts">
        <dt>{t('drawer.eventId')}</dt>
        <dd className="mono">{item.id}</dd>
        <dt>{t('drawer.type')}</dt>
        <dd>{item.eventType}</dd>
        <dt>{t('drawer.source')}</dt>
        <dd>{item.source}</dd>
        {item.repository && (
          <>
            <dt>{t('drawer.repo')}</dt>
            <dd>{item.repository}</dd>
          </>
        )}
        <dt>{t('drawer.received')}</dt>
        <dd>
          <time dateTime={item.createdAt}>{when(item.createdAt)}</time>
        </dd>
        <dt>{t('drawer.updated')}</dt>
        <dd>
          <time dateTime={item.updatedAt}>{when(item.updatedAt)}</time>
        </dd>
      </dl>

      <h3 className="sheet-subtitle">{t('drawer.timeline')}</h3>
      <EventTimeline trail={trail} />

      <h3 className="sheet-subtitle">{t('drawer.related')}</h3>
      {related.length === 0 ? (
        <p className="prose">{t('drawer.noRelated')}</p>
      ) : (
        <ul className="related">
          {related.map((r) => (
            <li key={r.id}>
              <button type="button" className="related-item" onClick={() => selectEvent(r.id)}>
                <span>{eventTitle(r, t)}</span>
                <span className="related-meta">
                  <PriorityBadge level={r.priority} /> {relativeTime(r.createdAt, now, tag, t)}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}

      <h3 className="sheet-subtitle">{t('drawer.actions')}</h3>
      <div className="drawer-actions">
        {!item.acknowledgedAt && (
          <button type="button" className="button" disabled={busy} onClick={() => act({ type: 'acknowledge' })}>
            {t('act.acknowledge')}
          </button>
        )}
        {!item.resolvedAt && (
          <button type="button" className="button" disabled={busy} onClick={() => act({ type: 'resolve' })}>
            {t('act.resolve')}
          </button>
        )}
        <button type="button" className="button" disabled={busy} onClick={() => act({ type: 'snooze', minutes: 60 })}>
          {t('act.snooze1h')}
        </button>
        <button type="button" className="button" disabled={busy} onClick={() => act({ type: item.read ? 'unread' : 'read' })}>
          {item.read ? t('act.markUnread') : t('act.markRead')}
        </button>
        <button type="button" className="button" disabled={busy} onClick={() => act({ type: item.archivedAt ? 'unarchive' : 'archive' })}>
          {item.archivedAt ? t('act.unarchive') : t('act.archive')}
        </button>
        <button type="button" className="button" onClick={copy}>
          {t('act.copyLink')}
        </button>
      </div>
      {critical ? (
        <p className="hint">{t('drawer.criticalLocked')}</p>
      ) : (
        <label className="priority-field">
          <span className="field-label">{t('act.priority')}</span>
          <select
            className="field select"
            disabled={busy}
            value={item.priorityOverride ?? ''}
            onChange={(e) => {
              const level = PRIORITY_CHOICES.find((l) => l === e.target.value)
              void act({ type: 'priority', priority: level ?? '' })
            }}
          >
            <option value="">{t('act.priorityAuto')}</option>
            {PRIORITY_CHOICES.map((l) => (
              <option key={l} value={l}>
                {t(`level.${l}`)}
              </option>
            ))}
          </select>
        </label>
      )}
      <p className="hint">{t('drawer.comingSoon')}</p>

      <h3 className="sheet-subtitle">{t('sheet.right')}</h3>
      <FeedbackPanel eventId={item.id} critical={critical} />
    </>,
    eventTitle(item, t),
  )
}
