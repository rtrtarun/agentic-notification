import { useMemo } from 'react'
import { useApp } from '../../app/AppState'
import { insightsClientFor } from '../../lib/insights'

/** The analytics and activity client for the current connection (real service or demo). */
export function useInsights() {
  const { settings } = useApp()
  return useMemo(() => insightsClientFor(settings), [settings])
}
