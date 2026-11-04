// Sample data for demo mode, so the console can be explored (and hosted as a
// static site) without a running service. Times are relative to "now" so the
// list always looks current.

import type { EventSummary } from './api'

const MINUTE = 60_000

function at(now: Date, minutesAgo: number): string {
  return new Date(now.getTime() - minutesAgo * MINUTE).toISOString()
}

/** Newest first, like GET /events. Covers every class and a merged thread. */
export function demoEvents(now: Date = new Date()): EventSummary[] {
  return [
    {
      id: 'demo-advisory',
      source: 'github',
      eventType: 'security_advisory',
      repository: 'acme/billing',
      attentionClass: 'critical',
      decisionState: { status: 'sent', reason: 'security vulnerability detected', matchedRule: 'github-security-advisory' },
      createdAt: at(now, 4),
    },
    {
      id: 'demo-pr',
      source: 'github',
      eventType: 'pull_request',
      repository: 'acme/web',
      attentionClass: 'standard',
      decisionState: { status: 'queued', reason: 'standard events are held back instead of interrupting' , matchedRule: 'default-github' },
      createdAt: at(now, 9),
    },
    {
      id: 'demo-star-2',
      source: 'github',
      eventType: 'star',
      repository: 'acme/web',
      attentionClass: 'background',
      decisionState: { status: 'merged', reason: 'duplicate of demo-star-1', matchedRule: 'github-star' },
      threadId: 'github:acme/web',
      mergedInto: 'demo-star-1',
      createdAt: at(now, 12),
    },
    {
      id: 'demo-push',
      source: 'github',
      eventType: 'push',
      repository: 'acme/api',
      attentionClass: 'standard',
      decisionState: { status: 'queued', reason: 'standard events are held back instead of interrupting', matchedRule: 'default-github' },
      createdAt: at(now, 26),
    },
    {
      id: 'demo-star-1',
      source: 'github',
      eventType: 'star',
      repository: 'acme/web',
      attentionClass: 'background',
      decisionState: { status: 'queued', reason: 'low-priority social signal', matchedRule: 'github-star' },
      threadId: 'github:acme/web',
      createdAt: at(now, 41),
    },
    {
      id: 'demo-dependabot',
      source: 'github',
      eventType: 'dependabot_alert',
      repository: 'acme/api',
      attentionClass: 'critical',
      decisionState: { status: 'sent', reason: 'dependency vulnerability detected', matchedRule: 'github-dependabot-alert' },
      createdAt: at(now, 95),
    },
    {
      id: 'demo-issue',
      source: 'github',
      eventType: 'issues',
      repository: 'acme/web',
      attentionClass: 'standard',
      decisionState: { status: 'queued', reason: 'standard events are held back instead of interrupting', matchedRule: 'default-github' },
      createdAt: at(now, 180),
    },
    {
      id: 'demo-fork',
      source: 'github',
      eventType: 'fork',
      repository: 'acme/api',
      attentionClass: 'background',
      decisionState: { status: 'queued', reason: 'low-priority social signal', matchedRule: 'github-star' },
      createdAt: at(now, 320),
    },
  ]
}
