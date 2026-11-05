import { useMemo } from 'react'
import { useApp } from '../../app/AppState'
import { rulesClientFor } from '../../lib/rules'

/** The rules client for the current connection (real service or demo). */
export function useRulesClient() {
  const { settings } = useApp()
  return useMemo(() => rulesClientFor(settings), [settings])
}
