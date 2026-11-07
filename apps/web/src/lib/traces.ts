// Agent traces (AgentPulse): one run of an AI agent, with its ordered steps, totals
// and the decision the platform made for each step (go-server pkg/traces). Telemetry
// comes from the events an agent publishes; nothing here decides importance.

import type { AttentionClass, EventSummary } from './api'
import { request, type Settings } from './api'

export type TraceStatus = 'running' | 'waiting' | 'complete' | 'failed'
export type StepKind = 'plan' | 'tool_call' | 'retry' | 'reflect' | 'human_approval' | 'complete'

export interface TraceSummary {
  traceId: string
  agentName?: string
  startedAt: string
  endedAt: string
  durationMs: number
  steps: number
  retries: number
  failures: number
  tokensIn: number
  tokensOut: number
  costUsd: number
  worstClass: AttentionClass
  status: TraceStatus
}

export interface TraceStep {
  eventId: string
  parentEventId?: string
  eventType: string
  stepKind?: StepKind
  toolName?: string
  attempt?: number
  durationMs?: number
  tokensIn?: number
  tokensOut?: number
  costUsd?: number
  confidence?: number
  attentionClass: AttentionClass
  decisionStatus?: string
  decisionReason?: string
  depth: number
  at: string
}

export interface TraceDetail extends TraceSummary {
  stepList: TraceStep[]
}

export interface TracesClient {
  list(signal?: AbortSignal): Promise<TraceSummary[]>
  get(traceId: string, signal?: AbortSignal): Promise<TraceDetail>
}

/** "30 s", "1 min 30 s", "850 ms". */
export function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)} ms`
  const seconds = Math.round(ms / 1000)
  if (seconds < 60) return `${seconds} s`
  const minutes = Math.floor(seconds / 60)
  const rest = seconds % 60
  return rest === 0 ? `${minutes} min` : `${minutes} min ${rest} s`
}

/** "$0.006", or "$0" when nothing was spent. Small amounts keep their digits. */
export function formatCost(usd: number): string {
  if (usd === 0) return '$0'
  if (usd < 0.01) return `$${usd.toFixed(4).replace(/0+$/, '').replace(/\.$/, '')}`
  return `$${usd.toFixed(2)}`
}

/** The width of each step's duration bar, as a percentage of the longest step (at least 3 so a step is visible). */
export function barWidths(steps: Pick<TraceStep, 'durationMs'>[]): number[] {
  const longest = Math.max(0, ...steps.map((s) => s.durationMs ?? 0))
  return steps.map((s) => {
    const ms = s.durationMs ?? 0
    return ms === 0 || longest === 0 ? 0 : Math.max(3, Math.round((ms / longest) * 100))
  })
}

// ---- demo data: realistic runs so the hosted demo shows AgentPulse without a service ----

interface Spec {
  id: string
  parent?: string
  type: string
  kind: StepKind
  minutesAgo: number
  cls: AttentionClass
  status: string
  reason: string
  rule?: string
  tool?: string
  attempt?: number
  durationMs?: number
  tokensIn?: number
  tokensOut?: number
  cost?: number
  confidence?: number
}

function build(traceId: string, agent: string, specs: Spec[], now: Date): TraceDetail {
  const at = (m: number) => new Date(now.getTime() - m * 60_000).toISOString()
  const depthOf = new Map<string, number>()
  const stepList: TraceStep[] = specs.map((s) => {
    const depth = s.parent ? (depthOf.get(s.parent) ?? 0) + 1 : 0
    depthOf.set(s.id, depth)
    return {
      eventId: s.id,
      parentEventId: s.parent,
      eventType: s.type,
      stepKind: s.kind,
      toolName: s.tool,
      attempt: s.attempt,
      durationMs: s.durationMs,
      tokensIn: s.tokensIn,
      tokensOut: s.tokensOut,
      costUsd: s.cost,
      confidence: s.confidence,
      attentionClass: s.cls,
      decisionStatus: s.status,
      decisionReason: s.reason,
      depth,
      at: at(s.minutesAgo),
    }
  })
  const rank = (c: AttentionClass) => (c === 'critical' ? 3 : c === 'standard' ? 2 : 1)
  const last = stepList[stepList.length - 1]
  const status: TraceStatus =
    last.stepKind === 'complete' ? 'complete' : last.stepKind === 'human_approval' ? 'waiting' : /(\.|_)(failed|failure|error)$/.test(last.eventType) ? 'failed' : 'running'
  return {
    traceId,
    agentName: agent,
    startedAt: stepList[0].at,
    endedAt: last.at,
    durationMs: stepList.reduce((n, s) => n + (s.durationMs ?? 0), 0),
    steps: stepList.length,
    retries: stepList.filter((s) => s.stepKind === 'retry').length,
    failures: stepList.filter((s) => /(\.|_)(failed|failure|error)$/.test(s.eventType)).length,
    tokensIn: stepList.reduce((n, s) => n + (s.tokensIn ?? 0), 0),
    tokensOut: stepList.reduce((n, s) => n + (s.tokensOut ?? 0), 0),
    costUsd: Math.round(stepList.reduce((n, s) => n + (s.costUsd ?? 0), 0) * 1e6) / 1e6,
    worstClass: stepList.reduce<AttentionClass>((w, s) => (rank(s.attentionClass) > rank(w) ? s.attentionClass : w), 'background'),
    status,
    stepList,
  }
}

const quiet = 'background events are kept for the record and never interrupt'

/** Three runs: a tool that times out and retries until a rule alerts, a run waiting for a person, and a clean run. */
export function demoTraces(now: Date = new Date()): TraceDetail[] {
  return [
    build(
      'run-research-7f3a',
      'researcher',
      [
        { id: 'r1', type: 'agent.plan', kind: 'plan', minutesAgo: 14, cls: 'background', status: 'queued', reason: quiet, durationMs: 1200, tokensIn: 620, tokensOut: 140, cost: 0.0041 },
        { id: 'r2', parent: 'r1', type: 'tool.call.failed', kind: 'tool_call', minutesAgo: 13, cls: 'standard', status: 'queued', reason: 'default routing', tool: 'web_search', attempt: 1, durationMs: 30000, confidence: 0.62 },
        { id: 'r3', parent: 'r2', type: 'agent.retry', kind: 'retry', minutesAgo: 12, cls: 'background', status: 'queued', reason: quiet, tool: 'web_search', attempt: 2, durationMs: 30000 },
        { id: 'r4', parent: 'r3', type: 'agent.retry', kind: 'retry', minutesAgo: 11, cls: 'background', status: 'queued', reason: quiet, tool: 'web_search', attempt: 3, durationMs: 30000 },
        { id: 'r5', parent: 'r4', type: 'agent.retry', kind: 'retry', minutesAgo: 10, cls: 'standard', status: 'queued', reason: 'an agent keeps retrying (rule agent-retries: attempt above 3, sent to Slack)', rule: 'agent-retries', tool: 'web_search', attempt: 4, durationMs: 30000 },
        { id: 'r6', parent: 'r5', type: 'agent.reflect', kind: 'reflect', minutesAgo: 9, cls: 'background', status: 'queued', reason: quiet, durationMs: 2100, tokensIn: 900, tokensOut: 310, cost: 0.0087 },
        { id: 'r7', parent: 'r6', type: 'agent.complete', kind: 'complete', minutesAgo: 8, cls: 'background', status: 'queued', reason: quiet, durationMs: 400, tokensIn: 300, tokensOut: 520, cost: 0.0062 },
      ],
      now,
    ),
    build(
      'run-deploy-91c2',
      'deployer',
      [
        { id: 'd1', type: 'agent.plan', kind: 'plan', minutesAgo: 6, cls: 'background', status: 'queued', reason: quiet, durationMs: 900, tokensIn: 480, tokensOut: 110, cost: 0.0033 },
        { id: 'd2', parent: 'd1', type: 'tool.call', kind: 'tool_call', minutesAgo: 5, cls: 'background', status: 'queued', reason: quiet, tool: 'plan_migration', durationMs: 4200, confidence: 0.91 },
        { id: 'd3', parent: 'd2', type: 'agent.approval.requested', kind: 'human_approval', minutesAgo: 4, cls: 'standard', status: 'queued', reason: 'waiting for a person to approve the production migration' },
      ],
      now,
    ),
    build(
      'run-summary-2b8e',
      'summariser',
      [
        { id: 's1', type: 'agent.plan', kind: 'plan', minutesAgo: 41, cls: 'background', status: 'queued', reason: quiet, durationMs: 700, tokensIn: 350, tokensOut: 90, cost: 0.0022 },
        { id: 's2', parent: 's1', type: 'tool.call', kind: 'tool_call', minutesAgo: 40, cls: 'background', status: 'queued', reason: quiet, tool: 'read_document', durationMs: 1800, confidence: 0.97 },
        { id: 's3', parent: 's2', type: 'agent.complete', kind: 'complete', minutesAgo: 39, cls: 'background', status: 'queued', reason: quiet, durationMs: 300, tokensIn: 1200, tokensOut: 260, cost: 0.0049 },
      ],
      now,
    ),
  ]
}

/** The demo runs as ordinary events, so "Inspect" opens the same detail drawer as any notification. */
export function demoAgentEvents(now: Date = new Date()): EventSummary[] {
  return demoTraces(now).flatMap((t) =>
    t.stepList.map((s) => ({
      id: s.eventId,
      source: 'agent',
      eventType: s.eventType,
      attentionClass: s.attentionClass,
      decisionState: { status: s.decisionStatus ?? 'queued', reason: s.decisionReason ?? '' },
      traceId: t.traceId,
      parentEventId: s.parentEventId,
      agentName: t.agentName,
      createdAt: s.at,
    })),
  )
}

export function demoTracesClient(): TracesClient {
  const all = () => demoTraces()
  return {
    async list() {
      return all().map((t) => {
        const { stepList, ...summary } = t
        void stepList
        return summary
      })
    },
    async get(traceId) {
      const found = all().find((t) => t.traceId === traceId)
      if (!found) throw new Error('That run could not be found.')
      return found
    },
  }
}

export function httpTraces(settings: Settings): TracesClient {
  return {
    async list(signal) {
      return ((await (await request(settings, '/api/traces', { signal })).json()) as { traces: TraceSummary[] }).traces
    },
    async get(traceId, signal) {
      return (await (await request(settings, `/api/traces/${encodeURIComponent(traceId)}`, { signal })).json()) as TraceDetail
    },
  }
}

export function tracesClientFor(settings: Settings): TracesClient {
  return settings.demo ? demoTracesClient() : httpTraces(settings)
}
