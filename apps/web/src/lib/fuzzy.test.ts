import { describe, expect, it } from 'vitest'
import { fuzzyFilter, fuzzyScore } from './fuzzy'

describe('fuzzyScore', () => {
  it('matches characters in order', () => {
    expect(fuzzyScore('ibx', 'Inbox')).not.toBeNull()
    expect(fuzzyScore('xbi', 'Inbox')).toBeNull()
  })

  it('treats an empty query as a match for everything', () => {
    expect(fuzzyScore('  ', 'anything')).toBe(0)
  })

  it('ignores case', () => {
    expect(fuzzyScore('INBOX', 'open inbox')).not.toBeNull()
  })

  it('prefers consecutive and word-start matches', () => {
    const tight = fuzzyScore('set', 'Open settings') ?? 0
    const loose = fuzzyScore('set', 'Switch workspace to eat') ?? 0
    expect(tight).toBeGreaterThan(loose)
  })
})

describe('fuzzyFilter', () => {
  const items = ['Open inbox', 'Open settings', 'Mark all as read', 'Change theme']

  it('returns everything for an empty query, in order', () => {
    expect(fuzzyFilter(items, '', (s) => s)).toEqual(items)
  })

  it('drops non-matches and ranks the best first', () => {
    expect(fuzzyFilter(items, 'sett', (s) => s)[0]).toBe('Open settings')
    expect(fuzzyFilter(items, 'zzz', (s) => s)).toEqual([])
  })
})
