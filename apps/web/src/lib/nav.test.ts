import { describe, expect, it } from 'vitest'
import { en } from './i18n/en'
import { HOME_PATH, NAV_GROUPS, NAV_ITEMS, navByPath } from './nav'

describe('navigation map', () => {
  it('has unique ids and paths', () => {
    expect(new Set(NAV_ITEMS.map((i) => i.id)).size).toBe(NAV_ITEMS.length)
    expect(new Set(NAV_ITEMS.map((i) => i.path)).size).toBe(NAV_ITEMS.length)
  })

  it('uses only declared groups and existing translations', () => {
    const groups = NAV_GROUPS.map((g) => g.id)
    for (const item of NAV_ITEMS) {
      expect(groups).toContain(item.group)
      expect(en).toHaveProperty(item.label)
    }
    for (const group of NAV_GROUPS) expect(en).toHaveProperty(group.label)
  })

  it('opens on a built screen', () => {
    const home = navByPath(HOME_PATH)
    expect(home).toBeDefined()
    expect(home?.plannedIn).toBeUndefined()
  })

  it('offers a small primary set for the mobile bar', () => {
    const primary = NAV_ITEMS.filter((i) => i.primary)
    expect(primary.length).toBeGreaterThanOrEqual(3)
    expect(primary.length).toBeLessThanOrEqual(5)
  })
})
