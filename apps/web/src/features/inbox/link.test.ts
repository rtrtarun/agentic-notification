import { describe, expect, it } from 'vitest'
import { notificationLink } from './link'

describe('notificationLink', () => {
  const here = { origin: 'https://example.github.io', pathname: '/attention/', search: '' }

  it('opens the inbox with the notification selected', () => {
    expect(notificationLink('github-push-1', here)).toBe('https://example.github.io/attention/#/inbox?open=github-push-1')
  })

  it('escapes ids so they cannot break the link', () => {
    expect(notificationLink('a b&c', here)).toBe('https://example.github.io/attention/#/inbox?open=a%20b%26c')
  })
})
