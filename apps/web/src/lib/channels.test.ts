import { describe, expect, it } from 'vitest'
import { demoChannelsClient, testFailureKey, webhookUrl } from './channels'

describe('channels', () => {
  it('maps service messages to a plain reason', () => {
    expect(testFailureKey('a test was just sent; wait a few seconds')).toBe('rate')
    expect(testFailureKey('channel is not configured')).toBe('unconfigured')
    expect(testFailureKey('The service hit an error.')).toBe('failed')
  })

  it('builds the webhook address from the service address', () => {
    expect(webhookUrl('https://api.example.com/', '/webhooks/github')).toBe('https://api.example.com/webhooks/github')
    expect(webhookUrl('  ', '/webhooks/github')).toBe('')
  })

  it('demo channels never expose a full address and reject tests on unconfigured ones', async () => {
    const list = await demoChannelsClient.channels()
    expect(list.every((c) => !c.target || c.target.includes('***') || c.target.endsWith('/…'))).toBe(true)
    await expect(demoChannelsClient.test('teams')).rejects.toThrow(/not configured/)
    await expect(demoChannelsClient.test('slack')).resolves.toBeUndefined()
  })

  it('demo integration counts the sample GitHub events', async () => {
    const [github] = await demoChannelsClient.integrations()
    expect(github.status).toBe('ready')
    expect(github.eventsReceived).toBe(github.eventTypes.reduce((n, e) => n + e.count, 0))
    expect(github.eventsReceived).toBeGreaterThan(0)
  })
})
