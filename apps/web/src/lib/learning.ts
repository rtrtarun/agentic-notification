// Learning suggestions: proposals the service makes from stored feedback
// (go-server pkg/learning). An admin reads the evidence and applies or dismisses
// each one. Nothing changes until then, and critical events and locked rules are
// never the subject of a suggestion.

import { request, type Settings } from './api'

export type SuggestionKind = 'adaptive' | 'demote' | 'promote' | 'override'

export interface Evidence {
  dismissed: number
  never: number
  opened: number
  reply: number
  always: number
}

export interface Suggestion {
  id: string
  kind: SuggestionKind
  status: 'pending' | 'applied' | 'dismissed'
  source: string
  eventType: string
  repository?: string
  ruleId?: string
  ruleName?: string
  summary: string
  proposed: string
  evidence: Evidence
  createdAt: string
  updatedAt: string
}

export interface SuggestionList {
  suggestions: Suggestion[]
  /** Dismissed or never signals needed before the service suggests a change. */
  minSignals: number
}

export interface LearningClient {
  list(signal?: AbortSignal): Promise<SuggestionList>
  apply(id: string): Promise<Suggestion>
  dismiss(id: string): Promise<Suggestion>
}

/** "5 dismissed, 2 muted" for the signals that say it was not worth it, then "1 opened" for the others. */
export function evidenceParts(e: Evidence): { label: 'dismissed' | 'never' | 'opened' | 'reply' | 'always'; n: number }[] {
  const all = [
    { label: 'dismissed', n: e.dismissed },
    { label: 'never', n: e.never },
    { label: 'opened', n: e.opened },
    { label: 'reply', n: e.reply },
    { label: 'always', n: e.always },
  ] as const
  return all.filter((p) => p.n > 0).map((p) => ({ ...p }))
}

/** The total number of signals behind a suggestion. */
export function evidenceTotal(e: Evidence): number {
  return e.dismissed + e.never + e.opened + e.reply + e.always
}

function demoSuggestions(): Suggestion[] {
  const at = (hoursAgo: number) => new Date(Date.now() - hoursAgo * 3_600_000).toISOString()
  return [
    {
      id: 'ls-1',
      kind: 'adaptive',
      status: 'pending',
      source: 'github',
      eventType: 'pull_request',
      repository: 'acme/web',
      ruleId: 'r-repo-activity',
      ruleName: 'repo-activity',
      summary: 'People dismissed or muted github pull_request notifications 7 times and never opened or replied to one.',
      proposed: 'Let Attention refine the rule "repo-activity", so quiet events like this can move to the digest.',
      evidence: { dismissed: 5, never: 2, opened: 0, reply: 0, always: 0 },
      createdAt: at(30),
      updatedAt: at(2),
    },
    {
      id: 'ls-2',
      kind: 'override',
      status: 'pending',
      source: 'github',
      eventType: 'fork',
      summary: 'People dismissed or muted github fork notifications 6 times and never opened or replied to one.',
      proposed: 'Add a "never notify" override for github fork notifications.',
      evidence: { dismissed: 4, never: 2, opened: 0, reply: 0, always: 0 },
      createdAt: at(20),
      updatedAt: at(1),
    },
    {
      id: 'ls-3',
      kind: 'promote',
      status: 'pending',
      source: 'github',
      eventType: 'push',
      ruleId: 'r-quiet-pushes',
      ruleName: 'quiet-pushes',
      summary: 'People asked to always be told about github push notifications 3 times.',
      proposed: 'Change the rule "quiet-pushes" from background to standard.',
      evidence: { dismissed: 0, never: 0, opened: 1, reply: 0, always: 3 },
      createdAt: at(10),
      updatedAt: at(3),
    },
  ]
}

export function demoLearningClient(): LearningClient {
  let list = demoSuggestions()
  const resolve = (id: string, status: 'applied' | 'dismissed'): Suggestion => {
    const found = list.find((s) => s.id === id)
    if (!found) throw new Error('That suggestion is no longer pending.')
    list = list.filter((s) => s.id !== id)
    return { ...found, status, updatedAt: new Date().toISOString() }
  }
  return {
    async list() {
      return { suggestions: list, minSignals: 5 }
    },
    async apply(id) {
      return resolve(id, 'applied')
    },
    async dismiss(id) {
      return resolve(id, 'dismissed')
    },
  }
}

export function httpLearning(settings: Settings): LearningClient {
  const post = async (id: string, action: 'apply' | 'dismiss') =>
    ((await (await request(settings, `/api/learning/suggestions/${encodeURIComponent(id)}/${action}`, { method: 'POST' })).json()) as { suggestion: Suggestion }).suggestion
  return {
    async list(signal) {
      return (await (await request(settings, '/api/learning/suggestions', { signal })).json()) as SuggestionList
    },
    apply: (id) => post(id, 'apply'),
    dismiss: (id) => post(id, 'dismiss'),
  }
}

export function learningClientFor(settings: Settings): LearningClient {
  return settings.demo ? demoLearningClient() : httpLearning(settings)
}
