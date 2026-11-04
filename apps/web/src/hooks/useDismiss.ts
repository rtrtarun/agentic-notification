import { useEffect, type RefObject } from 'react'

/** Calls onDismiss on Escape or a pointer press outside the element, while `open` is true. */
export function useDismiss(ref: RefObject<HTMLElement>, open: boolean, onDismiss: () => void): void {
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onDismiss()
    }
    const onPointer = (e: PointerEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onDismiss()
    }
    document.addEventListener('keydown', onKey)
    document.addEventListener('pointerdown', onPointer)
    return () => {
      document.removeEventListener('keydown', onKey)
      document.removeEventListener('pointerdown', onPointer)
    }
  }, [ref, open, onDismiss])
}
