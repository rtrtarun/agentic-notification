// The four levels people see, and the outcome of the pipeline's decision.
//
// The service computes these for live data. This mirror exists for demo mode,
// where no service runs. Both follow the same rules (see go-server pkg/inbox):
//   critical   -> Critical, always; nothing can change it.
//   standard   -> High when the pipeline sent it to you, otherwise Normal.
//   background -> Low.
// A person's override applies below critical only.

import type { EventSummary } from './api'
import type { Level, Outcome } from './inbox'

export const LEVELS: Level[] = ['critical', 'high', 'normal', 'low']

const RANK: Record<Level, number> = { critical: 0, high: 1, normal: 2, low: 3 }

/** 0 is most important. */
export function levelRank(level: Level): number {
  return RANK[level]
}

export function levelOf(e: Pick<EventSummary, 'attentionClass' | 'decisionState'>, override?: Level): Level {
  if (e.attentionClass === 'critical') return 'critical'
  if (override) return override
  if (e.attentionClass === 'background') return 'low'
  return e.decisionState?.status === 'sent' ? 'high' : 'normal'
}

export function outcomeOf(e: Pick<EventSummary, 'mergedInto' | 'decisionState'>): Outcome {
  if (e.mergedInto) return 'merged'
  switch (e.decisionState?.status) {
    case 'merged':
      return 'merged'
    case 'sent':
      return 'delivered'
    case 'queued':
    case 'delayed':
    case 'digest':
      return 'held'
    case 'budget_exceeded':
      return 'suppressed'
    default:
      return 'recorded'
  }
}
