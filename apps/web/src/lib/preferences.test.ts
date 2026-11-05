import { describe, expect, it } from 'vitest'
import { DEFAULT_PREFERENCES, addToList, demoPreferences, quietHoursProblem, removeFromList, timezones } from './preferences'

describe('quietHoursProblem', () => {
  const q = { enabled: true, start: '22:00', end: '07:00', timezone: 'UTC' }
  it('accepts a window that crosses midnight', () => expect(quietHoursProblem(q)).toBeNull())
  it('rejects malformed times', () => {
    expect(quietHoursProblem({ ...q, start: '25:00' })).toBe('start')
    expect(quietHoursProblem({ ...q, end: '7am' })).toBe('end')
    expect(quietHoursProblem({ ...q, start: '' })).toBe('start')
  })
  it('rejects equal times only while enabled', () => {
    expect(quietHoursProblem({ ...q, start: '08:00', end: '08:00' })).toBe('same')
    expect(quietHoursProblem({ ...q, enabled: false, start: '08:00', end: '08:00' })).toBeNull()
  })
})

describe('lists', () => {
  it('adds trimmed values once and lower-cases sources only when asked', () => {
    expect(addToList([], ' GitHub ', true)).toEqual(['github'])
    expect(addToList(['github'], 'GITHUB', true)).toEqual(['github'])
    expect(addToList([], ' Acme/* ', false)).toEqual(['Acme/*'])
  })
  it('ignores blanks and does not mutate the input', () => {
    const list = ['a']
    expect(addToList(list, '   ', true)).toBe(list)
    expect(removeFromList(['a', 'b'], 'a')).toEqual(['b'])
    expect(list).toEqual(['a'])
  })
})

describe('timezones', () => {
  it('always includes UTC first and the current zone', () => {
    const zones = timezones('Mars/Base')
    expect(zones[0]).toBe('UTC')
    expect(zones).toContain('Mars/Base')
    expect(new Set(zones).size).toBe(zones.length)
  })
})

describe('demo client', () => {
  it('stores what was saved, stamps the time and rejects bad quiet hours', async () => {
    const saved = await demoPreferences.save({ ...DEFAULT_PREFERENCES, mutedSources: ['gitlab'] })
    expect(saved.updatedAt).toBeDefined()
    expect((await demoPreferences.get()).mutedSources).toEqual(['gitlab'])
    await expect(demoPreferences.save({ ...DEFAULT_PREFERENCES, quietHours: { ...DEFAULT_PREFERENCES.quietHours, start: 'bad' } })).rejects.toThrow(/invalid/)
    expect((await demoPreferences.get()).mutedSources).toEqual(['gitlab'])
  })
})
