import { useCallback, useRef, useState } from 'react'
import { EventIcon } from '../../components/EventIcon'
import { useDismiss } from '../../hooks/useDismiss'
import { eventTitle, reasonText, relativeTime } from '../../lib/events'
import { useI18n } from '../../lib/i18n/I18nContext'
import type { Action, InboxItem } from '../../lib/inbox'
import { PriorityBadge, SourceBadge, StatusBadge } from './badges'

const DAY_MINUTES = 24 * 60

interface NotificationRowProps {
  item: InboxItem
  now: Date
  active: boolean
  checked: boolean
  archivedView: boolean
  onCheck: (id: string) => void
  onOpen: (id: string) => void
  onAction: (id: string, action: Action) => void
  onDelete: (id: string) => void
  onCopyLink: (id: string) => void
}

/**
 * One notification. The checkbox, the open button and the menu are separate
 * controls (never nested), so each is reachable and named on its own.
 */
export function NotificationRow({ item, now, active, checked, archivedView, onCheck, onOpen, onAction, onDelete, onCopyLink }: NotificationRowProps) {
  const { t, tag } = useI18n()
  const title = eventTitle(item, t)
  const reason = reasonText(item.decisionState?.reason, t)
  const snoozed = item.snoozedUntil && new Date(item.snoozedUntil) > now

  return (
    <li className="nrow" data-priority={item.priority} data-unread={!item.read} data-active={active}>
      <input
        type="checkbox"
        className="nrow-check"
        checked={checked}
        aria-label={t('inbox.select', { title })}
        onChange={() => onCheck(item.id)}
      />
      <button type="button" className="nrow-open" aria-current={active ? 'true' : undefined} onClick={() => onOpen(item.id)}>
        <EventIcon eventType={item.eventType} merged={item.outcome === 'merged'} />
        <span className="nrow-text">
          <span className="nrow-title">
            {!item.read && <span className="unread-dot" role="img" aria-label={t('inbox.unreadDot')} />}
            {title}
          </span>
          {reason && <span className="nrow-reason">{reason}</span>}
          <span className="nrow-notes">
            {item.repeatCount > 0 && <span>{t('inbox.repeats', { n: item.repeatCount })}</span>}
            {snoozed && <span>{t('inbox.snoozedUntil', { time: new Date(item.snoozedUntil!).toLocaleString(tag) })}</span>}
            {item.acknowledgedAt && <span>{t('inbox.acknowledged')}</span>}
            {item.resolvedAt && <span>{t('inbox.resolved')}</span>}
          </span>
        </span>
      </button>
      <span className="nrow-meta">
        <SourceBadge source={item.source} />
        <PriorityBadge level={item.priority} />
        <StatusBadge outcome={item.outcome} />
        <time dateTime={item.createdAt} title={new Date(item.createdAt).toLocaleString(tag)}>
          {relativeTime(item.createdAt, now, tag, t)}
        </time>
      </span>
      <RowMenu
        title={title}
        item={item}
        archivedView={archivedView}
        onOpen={() => onOpen(item.id)}
        onAction={(a) => onAction(item.id, a)}
        onDelete={() => onDelete(item.id)}
        onCopyLink={() => onCopyLink(item.id)}
      />
    </li>
  )
}

interface RowMenuProps {
  title: string
  item: InboxItem
  archivedView: boolean
  onOpen: () => void
  onAction: (a: Action) => void
  onDelete: () => void
  onCopyLink: () => void
}

function RowMenu({ title, item, archivedView, onOpen, onAction, onDelete, onCopyLink }: RowMenuProps) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const buttonRef = useRef<HTMLButtonElement>(null)
  const close = useCallback(() => setOpen(false), [])
  useDismiss(ref, open, close)

  // Close the menu, then do the thing.
  const choose = (fn: () => void) => () => {
    close()
    buttonRef.current?.focus()
    fn()
  }

  return (
    <div className="nrow-menu" ref={ref}>
      <button
        type="button"
        className="icon-button row-menu-button"
        ref={buttonRef}
        aria-label={t('row.more', { title })}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <span aria-hidden="true">⋮</span>
      </button>
      {open && (
        <div className="menu menu-right" role="menu">
          <button type="button" role="menuitem" className="menu-item" onClick={choose(onOpen)}>
            {t('act.open')}
          </button>
          <button
            type="button"
            role="menuitem"
            className="menu-item"
            onClick={choose(() => onAction({ type: item.read ? 'unread' : 'read' }))}
          >
            {item.read ? t('act.markUnread') : t('act.markRead')}
          </button>
          {!item.acknowledgedAt && (
            <button type="button" role="menuitem" className="menu-item" onClick={choose(() => onAction({ type: 'acknowledge' }))}>
              {t('act.acknowledge')}
            </button>
          )}
          {!item.resolvedAt && (
            <button type="button" role="menuitem" className="menu-item" onClick={choose(() => onAction({ type: 'resolve' }))}>
              {t('act.resolve')}
            </button>
          )}
          <button type="button" role="menuitem" className="menu-item" onClick={choose(() => onAction({ type: 'snooze', minutes: 60 }))}>
            {t('act.snooze1h')}
          </button>
          <button type="button" role="menuitem" className="menu-item" onClick={choose(() => onAction({ type: 'snooze', minutes: DAY_MINUTES }))}>
            {t('act.snooze1d')}
          </button>
          <button
            type="button"
            role="menuitem"
            className="menu-item"
            onClick={choose(() => onAction({ type: archivedView ? 'unarchive' : 'archive' }))}
          >
            {archivedView ? t('act.unarchive') : t('act.archive')}
          </button>
          <button type="button" role="menuitem" className="menu-item" onClick={choose(onCopyLink)}>
            {t('act.copyLink')}
          </button>
          <button type="button" role="menuitem" className="menu-item menu-danger" onClick={choose(onDelete)}>
            {t('act.delete')}
          </button>
        </div>
      )}
    </div>
  )
}
