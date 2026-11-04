import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from 'react'
import { useI18n } from '../lib/i18n/I18nContext'
import { CloseIcon } from '../components/icons'

export type ToastTone = 'info' | 'success' | 'error'

interface ToastItem {
  id: number
  message: string
  tone: ToastTone
}

interface ToastApi {
  /** Shows a short message. Errors stay until dismissed; others fade after a few seconds. */
  push: (message: string, tone?: ToastTone) => void
}

const ToastContext = createContext<ToastApi | null>(null)

const LIFETIME_MS = 5000
const MAX_VISIBLE = 4

export function ToastProvider({ children }: { children: ReactNode }) {
  const { t } = useI18n()
  const [items, setItems] = useState<ToastItem[]>([])
  const nextId = useRef(1)

  const dismiss = useCallback((id: number) => setItems((all) => all.filter((i) => i.id !== id)), [])

  const push = useCallback(
    (message: string, tone: ToastTone = 'info') => {
      const id = nextId.current++
      setItems((all) => [...all, { id, message, tone }].slice(-MAX_VISIBLE))
      if (tone !== 'error') window.setTimeout(() => dismiss(id), LIFETIME_MS)
    },
    [dismiss],
  )

  const api = useMemo(() => ({ push }), [push])

  return (
    <ToastContext.Provider value={api}>
      {children}
      <div className="toasts" role="region" aria-label="Notifications" aria-live="polite">
        {items.map((item) => (
          <div key={item.id} className="toast" data-tone={item.tone} role={item.tone === 'error' ? 'alert' : 'status'}>
            <span>{item.message}</span>
            <button type="button" className="toast-close" aria-label={t('toast.dismiss')} onClick={() => dismiss(item.id)}>
              <CloseIcon />
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}

export function useToast(): ToastApi {
  const api = useContext(ToastContext)
  if (!api) throw new Error('useToast must be used inside ToastProvider')
  return api
}
