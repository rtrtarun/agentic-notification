import { useEffect, useRef } from 'react'
import { useI18n } from '../lib/i18n/I18nContext'

interface ConfirmDialogProps {
  open: boolean
  title: string
  body: string
  confirmLabel: string
  onConfirm: () => void
  onCancel: () => void
}

/**
 * Asks before something that cannot be undone. Focus starts on Cancel so a stray
 * Enter does not confirm, Escape cancels, and focus returns to where it was.
 */
export function ConfirmDialog({ open, title, body, confirmLabel, onConfirm, onCancel }: ConfirmDialogProps) {
  const { t } = useI18n()
  const cancelRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (!open) return
    const opener = document.activeElement as HTMLElement | null
    cancelRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onCancel()
      if (e.key === 'Tab') {
        // Two buttons: keep focus inside the dialog.
        const buttons = [...document.querySelectorAll<HTMLElement>('.dialog button')]
        const first = buttons[0]
        const last = buttons[buttons.length - 1]
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault()
          last.focus()
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault()
          first.focus()
        }
      }
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      opener?.focus()
    }
  }, [open, onCancel])

  if (!open) return null
  return (
    <div className="dialog-layer">
      <button type="button" className="scrim" tabIndex={-1} aria-label={t('confirm.cancel')} onClick={onCancel} />
      <div className="dialog" role="alertdialog" aria-modal="true" aria-labelledby="dialog-title" aria-describedby="dialog-body">
        <h2 id="dialog-title" className="dialog-title">
          {title}
        </h2>
        <p id="dialog-body" className="prose">
          {body}
        </p>
        <div className="actions">
          <button type="button" className="button" ref={cancelRef} onClick={onCancel}>
            {t('confirm.cancel')}
          </button>
          <button type="button" className="button button-danger" onClick={onConfirm}>
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
