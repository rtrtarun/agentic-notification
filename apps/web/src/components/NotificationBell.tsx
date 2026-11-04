import { useEffect, useRef, useState } from 'react'
import type { EventSummary } from '../lib/api'
import { eventTitle, relativeTime } from '../lib/events'
import { useI18n } from '../lib/i18n/I18nContext'
import { Chip } from './Chip'
import { Flag } from './Flag'
import { BellIcon } from './icons'

const SHOWN = 5

interface NotificationBellProps {
  /** Unread events, newest first. */
  unread: EventSummary[]
  now: Date
  onOpenEvent: (id: string) => void
  onMarkAllRead: () => void
  onViewAll: () => void
}

/** A bell with an unread count. Opens a short list of the latest unread events. */
export function NotificationBell({ unread, now, onOpenEvent, onMarkAllRead, onViewAll }: NotificationBellProps) {
  const { t, tag } = useI18n()
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  const buttonRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setOpen(false)
        buttonRef.current?.focus()
      }
    }
    const onPointer = (e: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('keydown', onKey)
    document.addEventListener('pointerdown', onPointer)
    return () => {
      document.removeEventListener('keydown', onKey)
      document.removeEventListener('pointerdown', onPointer)
    }
  }, [open])

  const count = unread.length
  return (
    <div className="bell" ref={rootRef}>
      <button
        type="button"
        className="icon-button"
        ref={buttonRef}
        aria-label={`${t('bell.label')}: ${count}`}
        aria-expanded={open}
        aria-haspopup="dialog"
        onClick={() => setOpen((v) => !v)}
      >
        <BellIcon />
        {count > 0 && (
          <span className="badge" aria-hidden="true">
            {count > 99 ? '99+' : count}
          </span>
        )}
      </button>

      {open && (
        <div className="popover" role="dialog" aria-label={t('bell.title')}>
          <p className="popover-title">
            {t('bell.title')} <span className="popover-count">{count}</span>
          </p>
          {count === 0 ? (
            <p className="popover-empty">{t('bell.empty')}</p>
          ) : (
            <ul className="popover-list">
              {unread.slice(0, SHOWN).map((e) => (
                <li key={e.id}>
                  <button
                    type="button"
                    className="popover-item"
                    onClick={() => {
                      onOpenEvent(e.id)
                      setOpen(false)
                    }}
                  >
                    <Flag kind={e.attentionClass} />
                    <span className="popover-text">
                      <span className="popover-name">{eventTitle(e, t)}</span>
                      <span className="popover-meta">
                        <Chip eventType={e.eventType} /> {relativeTime(e.createdAt, now, tag, t)}
                      </span>
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
          <div className="popover-foot">
            <button type="button" className="text-button" disabled={count === 0} onClick={onMarkAllRead}>
              {t('bell.markAll')}
            </button>
            <button
              type="button"
              className="text-button"
              onClick={() => {
                onViewAll()
                setOpen(false)
              }}
            >
              {t('bell.viewAll')}
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
