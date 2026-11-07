// Rules and policies. The service stores and enforces them (go-server pkg/rules
// and pkg/policy); demo mode keeps an in-memory copy with the same guards, so
// the screens behave the same with or without a service.

import { request, type AttentionClass, type Settings } from './api'

export interface RuleCondition {
  source?: string
  eventType?: string
  repository?: string
}

export interface Rule {
  id: string
  name: string
  description?: string
  when: RuleCondition
  then: { attentionClass: AttentionClass; reason: string; adaptive?: boolean }
  disabled?: boolean
  /** Locked rules protect critical routing and cannot be changed here. */
  locked?: boolean
}

/** What a person can edit. The id, position and lock are the service's. */
export type RuleDraft = Omit<Rule, 'id' | 'locked'>

export interface Sample {
  source: string
  eventType: string
  repository: string
}

export interface TestStep {
  id: string
  name: string
  disabled?: boolean
  matched: boolean
  winner?: boolean
}

export interface TestResult {
  matched: boolean
  attentionClass: AttentionClass
  reason: string
  matchedRule?: string
  aiBypassed: boolean
  steps: TestStep[]
}

export interface Guardrail {
  id: string
  title: string
  description: string
}

export interface ClassPolicy {
  class: AttentionClass
  level: string
  delivery: string
  dedup: string
  aiEligible: boolean
  description: string
}

export interface PolicySummary {
  guardrails: Guardrail[]
  classes: ClassPolicy[]
  dedupWindowSeconds: number
  retentionHours: number
  lockedRules: string[]
  ruleCount: number
  disabledRuleCount: number
}

export interface RulesClient {
  list(signal?: AbortSignal): Promise<Rule[]>
  create(draft: RuleDraft): Promise<Rule>
  update(id: string, draft: RuleDraft): Promise<Rule>
  remove(id: string): Promise<void>
  reorder(ids: string[]): Promise<Rule[]>
  test(sample: Sample): Promise<TestResult>
  policies(signal?: AbortSignal): Promise<PolicySummary>
}

export const EMPTY_DRAFT: RuleDraft = { name: '', description: '', when: {}, then: { attentionClass: 'standard', reason: '' } }

export function draftOf(rule: Rule): RuleDraft {
  return { name: rule.name, description: rule.description ?? '', when: { ...rule.when }, then: { ...rule.then }, disabled: rule.disabled }
}

/** One-line description of a condition, for list rows. Empty fields are left out. */
export function describeWhen(when: RuleCondition): string[] {
  return [when.source, when.eventType, when.repository].filter((v): v is string => !!v)
}

/** `*` matches any run of characters except `/`; `?` matches one. Same as Go's path.Match for these cases. */
function globToRegExp(pattern: string): RegExp {
  const escaped = pattern.replace(/[.+^${}()|\\]/g, '\\$&').replace(/\*/g, '[^/]*').replace(/\?/g, '[^/]')
  return new RegExp(`^${escaped}$`)
}

function fieldMatches(pattern: string | undefined, value: string): boolean {
  return !pattern || globToRegExp(pattern).test(value)
}

/** Mirrors go-server pkg/rules Evaluate. */
export function evaluate(list: Rule[], sample: Sample): TestResult {
  let result: TestResult = {
    matched: false,
    attentionClass: 'standard',
    reason: 'no matching rule, default standard',
    aiBypassed: false,
    steps: [],
  }
  for (const r of list) {
    const matched =
      !r.disabled &&
      fieldMatches(r.when.source, sample.source) &&
      fieldMatches(r.when.eventType, sample.eventType) &&
      fieldMatches(r.when.repository, sample.repository)
    const winner = matched && !result.matched
    const step: TestStep = { id: r.id, name: r.name, disabled: r.disabled, matched, winner: winner || undefined }
    result = winner
      ? { ...result, matched: true, attentionClass: r.then.attentionClass, reason: r.then.reason, matchedRule: r.name, steps: [...result.steps, step] }
      : { ...result, steps: [...result.steps, step] }
  }
  return { ...result, aiBypassed: result.attentionClass === 'critical' }
}

export const MAX_RULES = 100

/** Validates a draft the way the service does, so the form can answer before sending. Returns a message key suffix or null. */
export function draftProblem(draft: RuleDraft, others: Rule[], ownId?: string): 'name' | 'duplicate' | 'long' | null {
  const name = draft.name.trim()
  if (name === '' || [...name].length > 80) return 'name'
  if (others.some((r) => r.id !== ownId && r.name.toLowerCase() === name.toLowerCase())) return 'duplicate'
  if ([...(draft.description ?? '')].length > 300 || [...draft.then.reason].length > 200) return 'long'
  return null
}

const SEED: Rule[] = [
  {
    id: 'demo-security',
    name: 'github-security-advisory',
    when: { source: 'github', eventType: 'security_advisory' },
    then: { attentionClass: 'critical', reason: 'security vulnerability detected' },
    locked: true,
  },
  {
    id: 'demo-dependabot',
    name: 'github-dependabot-alert',
    when: { source: 'github', eventType: 'dependabot_alert' },
    then: { attentionClass: 'critical', reason: 'dependency vulnerability detected' },
    locked: true,
  },
  {
    id: 'demo-star',
    name: 'github-star',
    when: { source: 'github', eventType: 'star' },
    then: { attentionClass: 'background', reason: 'low-priority social signal' },
  },
  { id: 'demo-default', name: 'default-github', when: { source: 'github' }, then: { attentionClass: 'standard', reason: 'default routing', adaptive: true } },
]

/** An in-memory client for demo mode, with the service's guards. Each call to makeDemoRules starts from the seed. */
export function makeDemoRules(): RulesClient {
  let rules: Rule[] = SEED.map((r) => ({ ...r }))
  let counter = 0
  const fail = (message: string): never => {
    throw new Error(message)
  }
  const find = (id: string) => rules.find((r) => r.id === id) ?? fail('rule not found')
  const checked = (draft: RuleDraft, ownId?: string): RuleDraft => {
    if (draftProblem(draft, rules, ownId)) fail('invalid rule: check the name and lengths')
    return { ...draft, name: draft.name.trim(), then: { ...draft.then, reason: draft.then.reason.trim() || `matched rule ${draft.name.trim()}` } }
  }
  return {
    async list() {
      return rules.map((r) => ({ ...r }))
    },
    async create(draft) {
      if (rules.length >= MAX_RULES) fail(`invalid rule: at most ${MAX_RULES} rules`)
      const rule: Rule = { ...checked(draft), id: `demo-${++counter}-${Date.now()}` }
      rules = [...rules, rule]
      return rule
    },
    async update(id, draft) {
      if (find(id).locked) fail('this rule is locked: it protects critical routing')
      const rule: Rule = { ...checked(draft, id), id }
      rules = rules.map((r) => (r.id === id ? rule : r))
      return rule
    },
    async remove(id) {
      if (find(id).locked) fail('this rule is locked: it protects critical routing')
      rules = rules.filter((r) => r.id !== id)
    },
    async reorder(ids) {
      const byId = new Map(rules.map((r) => [r.id, r]))
      if (ids.length !== rules.length || new Set(ids).size !== ids.length || ids.some((id) => !byId.has(id))) fail('invalid rule: send every rule id exactly once')
      const next = ids.map((id) => byId.get(id)!)
      const firstOpen = next.findIndex((r) => !r.locked)
      if (firstOpen >= 0 && next.slice(firstOpen).some((r) => r.locked)) fail('invalid rule: locked rules must stay above all other rules')
      rules = next
      return rules.map((r) => ({ ...r }))
    },
    async test(sample) {
      return evaluate(rules, sample)
    },
    async policies() {
      return {
        guardrails: DEMO_GUARDRAILS,
        classes: DEMO_CLASSES,
        dedupWindowSeconds: 600,
        retentionHours: 0,
        lockedRules: rules.filter((r) => r.locked).map((r) => r.name),
        ruleCount: rules.length,
        disabledRuleCount: rules.filter((r) => r.disabled).length,
      }
    },
  }
}

const DEMO_GUARDRAILS: Guardrail[] = [
  { id: 'never-suppress-critical', title: 'Critical events are never suppressed', description: 'Critical events skip deduplication, budgets and digests. They are delivered to every configured channel.' },
  { id: 'no-ai-in-layer-1', title: 'No AI in the policy layer', description: 'Classification by rules, deduplication and threading are deterministic. Critical events never reach the AI layer.' },
  { id: 'locked-rules-first', title: 'Locked rules run first', description: 'Rules that protect critical routing cannot be edited, disabled, removed or placed below other rules.' },
  { id: 'critical-priority-fixed', title: 'Critical priority cannot be lowered', description: 'A person can raise or lower standard and background notifications, never a critical one.' },
]

const DEMO_CLASSES: ClassPolicy[] = [
  { class: 'critical', level: 'critical', delivery: 'immediate, every configured channel', dedup: 'never merged', aiEligible: false, description: 'Security alerts, incidents, payment failures and outages.' },
  { class: 'standard', level: 'high or normal', delivery: 'held and shown in the inbox', dedup: 'repeats merged inside the window', aiEligible: true, description: 'Normal work that does not need an interruption.' },
  { class: 'background', level: 'low', delivery: 'recorded quietly', dedup: 'repeats merged inside the window', aiEligible: true, description: 'Low-value signals such as stars and watches.' },
]

const demoRules = makeDemoRules()

export function httpRules(settings: Settings): RulesClient {
  const send = (path: string, method: string, body?: unknown, signal?: AbortSignal) =>
    request(settings, path, { method, signal, ...(body === undefined ? {} : { body: JSON.stringify(body) }) })
  return {
    async list(signal) {
      return ((await (await send('/api/rules', 'GET', undefined, signal)).json()) as { rules: Rule[] }).rules
    },
    async create(draft) {
      return (await (await send('/api/rules', 'POST', draft)).json()) as Rule
    },
    async update(id, draft) {
      return (await (await send(`/api/rules/${encodeURIComponent(id)}`, 'PUT', draft)).json()) as Rule
    },
    async remove(id) {
      await send(`/api/rules/${encodeURIComponent(id)}`, 'DELETE')
    },
    async reorder(ids) {
      return ((await (await send('/api/rules/reorder', 'POST', { ids })).json()) as { rules: Rule[] }).rules
    },
    async test(sample) {
      return (await (await send('/api/rules/test', 'POST', sample)).json()) as TestResult
    },
    async policies(signal) {
      return (await (await send('/api/policies', 'GET', undefined, signal)).json()) as PolicySummary
    },
  }
}

export function rulesClientFor(settings: Settings): RulesClient {
  return settings.demo ? demoRules : httpRules(settings)
}
