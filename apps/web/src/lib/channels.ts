// Delivery channels and inbound integrations. The service reports them with
// secrets masked (go-server pkg/channels and pkg/integrations); demo mode shows
// sample values and never sends anything.

import { request, type Settings } from './api'
import { demoEvents } from './demo'

export interface Channel {
  id: 'email' | 'slack' | 'teams' | 'sms'
  name: string
  configured: boolean
  /** Already masked by the service. */
  target?: string
  receives: string[]
  setup: string
  /** 'mock' when messages are only logged and never leave the service. */
  mode?: 'mock'
}

export type IntegrationStatus = 'ready' | 'unsigned' | 'blocked'

export interface Integration {
  id: string
  name: string
  status: IntegrationStatus
  webhookPath: string
  signing: string
  secretConfigured: boolean
  eventsReceived: number
  lastEventAt?: string
  lastEventType?: string
  eventTypes: { eventType: string; count: number }[]
  setup: string
}

export interface ChannelsClient {
  channels(signal?: AbortSignal): Promise<Channel[]>
  /** Resolves when the test message was handed to the channel. Rejects with a message people can read. */
  test(id: Channel['id']): Promise<void>
  integrations(signal?: AbortSignal): Promise<Integration[]>
}

/** Plain-language reason for a rejected test, keyed by HTTP-ish situation. */
export function testFailureKey(message: string): 'rate' | 'unconfigured' | 'failed' {
  if (/wait a few seconds/i.test(message)) return 'rate'
  if (/not configured/i.test(message)) return 'unconfigured'
  return 'failed'
}

export const demoChannelsClient: ChannelsClient = {
  async channels() {
    return [
      { id: 'email', name: 'Email', configured: true, target: 'o***@example.com', receives: ['critical'], setup: 'SMTP_HOST, SMTP_SENDER and DEFAULT_NOTIFY_EMAIL' },
      { id: 'slack', name: 'Slack', configured: true, target: 'hooks.slack.com/…', receives: ['critical', 'standard'], setup: 'SLACK_WEBHOOK_URL' },
      { id: 'teams', name: 'Microsoft Teams', configured: false, receives: ['critical'], setup: 'TEAMS_WEBHOOK_URL' },
      { id: 'sms', name: 'SMS', configured: true, target: '+14*******00', receives: ['critical'], setup: 'SMS_GATEWAY (mock, http or sns) and SMS_RECIPIENTS', mode: 'mock' },
    ]
  },
  async test(id) {
    if (id === 'teams') throw new Error('channel is not configured')
  },
  async integrations() {
    const events = demoEvents().filter((e) => e.source === 'github')
    const counts = new Map<string, number>()
    for (const e of events) counts.set(e.eventType, (counts.get(e.eventType) ?? 0) + 1)
    const latest = events.reduce<string | undefined>((a, e) => (a === undefined || e.createdAt > a ? e.createdAt : a), undefined)
    return [
      {
        id: 'github',
        name: 'GitHub',
        status: 'ready',
        webhookPath: '/webhooks/github',
        signing: 'HMAC-SHA256 signature required (X-Hub-Signature-256)',
        secretConfigured: true,
        eventsReceived: events.length,
        lastEventAt: latest,
        lastEventType: events.find((e) => e.createdAt === latest)?.eventType,
        eventTypes: [...counts.entries()].map(([eventType, count]) => ({ eventType, count })).sort((a, b) => b.count - a.count || a.eventType.localeCompare(b.eventType)),
        setup: 'GITHUB_WEBHOOK_SECRET',
      },
    ]
  },
}

export function httpChannels(settings: Settings): ChannelsClient {
  return {
    async channels(signal) {
      return ((await (await request(settings, '/api/channels', { signal })).json()) as { channels: Channel[] }).channels
    },
    async test(id) {
      await request(settings, `/api/channels/${encodeURIComponent(id)}/test`, { method: 'POST' })
    },
    async integrations(signal) {
      return ((await (await request(settings, '/api/integrations', { signal })).json()) as { integrations: Integration[] }).integrations
    },
  }
}

export function channelsClientFor(settings: Settings): ChannelsClient {
  return settings.demo ? demoChannelsClient : httpChannels(settings)
}

/** The full webhook URL to paste into GitHub, from the service address. Empty in demo mode. */
export function webhookUrl(backendUrl: string, path: string): string {
  const base = backendUrl.trim().replace(/\/+$/, '')
  return base ? `${base}${path}` : ''
}
