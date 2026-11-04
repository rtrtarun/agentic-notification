import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { describeHour, eventLabel, eventTitle, reasonText, relativeTime, statusLabel } from './events'
import { LOCALES, detectLocale, isLocale, makeTranslator, saveLocale, tEn } from './i18n'
import { en } from './i18n/en'
import { es } from './i18n/es'
import { hi } from './i18n/hi'

function fakeStorage() {
  const data = new Map<string, string>()
  return {
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
  }
}

beforeEach(() => {
  vi.stubGlobal('localStorage', fakeStorage())
})

afterEach(() => {
  vi.unstubAllGlobals()
})

const catalogs = [
  ['es', es],
  ['hi', hi],
] as const

describe('catalogs', () => {
  const keys = Object.keys(en).sort()

  it.each(catalogs)('%s defines exactly the English keys', (_name, catalog) => {
    expect(Object.keys(catalog).sort()).toEqual(keys)
  })

  it.each(catalogs)('%s keeps every {placeholder} the English text has', (_name, catalog) => {
    const holders = (text: string) => (text.match(/\{\w+\}/g) ?? []).sort()
    for (const key of Object.keys(en) as (keyof typeof en)[]) {
      expect(holders(catalog[key]), key).toEqual(holders(en[key]))
    }
  })

  it('has no empty text', () => {
    for (const catalog of [en, es, hi]) {
      for (const [key, text] of Object.entries(catalog)) expect(text.trim(), key).not.toBe('')
    }
  })

  it('lists every supported language once', () => {
    expect(LOCALES.map((l) => l.code).sort()).toEqual(['en', 'es', 'hi'])
  })
})

describe('makeTranslator', () => {
  it('fills in values', () => {
    expect(tEn('pager.page', { page: 2, pages: 5 })).toBe('Page 2 of 5')
    expect(makeTranslator('es')('pager.page', { page: 2, pages: 5 })).toBe('Página 2 de 5')
  })

  it('leaves an unknown placeholder visible', () => {
    expect(tEn('pager.page', { page: 1 })).toBe('Page 1 of {pages}')
  })

  it('translates into the chosen language', () => {
    expect(makeTranslator('hi')('welcome.demo')).toBe(hi['welcome.demo'])
    expect(hi['welcome.demo']).not.toBe(en['welcome.demo'])
  })
})

describe('locale choice', () => {
  it('recognises supported codes only', () => {
    expect(isLocale('es')).toBe(true)
    expect(isLocale('xx')).toBe(false)
    expect(isLocale(5)).toBe(false)
  })

  it('prefers the saved choice', () => {
    saveLocale('hi')
    expect(detectLocale()).toBe('hi')
  })

  it('falls back to the browser language, then English', () => {
    vi.stubGlobal('navigator', { languages: ['es-MX', 'en'], language: 'es-MX' })
    expect(detectLocale()).toBe('es')
    vi.stubGlobal('navigator', { languages: ['fr-FR'], language: 'fr-FR' })
    expect(detectLocale()).toBe('en')
  })
})

describe('translated helpers', () => {
  const tEs = makeTranslator('es')
  const tHi = makeTranslator('hi')

  it('translates event labels, titles and statuses', () => {
    expect(eventTitle({ eventType: 'security_advisory', repository: 'acme/api' }, tEs)).toBe(
      'Aviso de seguridad en acme/api',
    )
    expect(eventTitle({ eventType: 'push', repository: 'acme/api' }, tHi)).toBe(`acme/api ${hi['ev.titleIn'].split(' ')[1]} ${hi['ev.push']}`)
    expect(statusLabel('queued', tEs)).toBe('Retenido')
    expect(statusLabel(undefined, tHi)).toBe(hi['status.none'])
  })

  it('keeps unknown events and statuses readable instead of failing', () => {
    expect(eventLabel('merge_group', tEs)).toBe('Merge group')
    expect(statusLabel('something_new', tEs)).toBe('something_new')
  })

  it('translates known reasons and passes unknown ones through', () => {
    expect(reasonText('security vulnerability detected', tEs)).toBe('Vulnerabilidad de seguridad detectada')
    expect(reasonText('duplicate of github-star-d3', tEs)).toBe('Duplicado de github-star-d3')
    expect(reasonText('custom rule fired', tEs)).toBe('Custom rule fired')
    expect(reasonText(undefined, tEs)).toBe('')
  })

  it('describes the last hour in the chosen language', () => {
    expect(describeHour({ sent: 1, heldBack: 2, merged: 0, total: 3 }, tEs)).toBe(
      'En la última hora: 1 enviados a ti, 2 retenidos.',
    )
    expect(describeHour({ sent: 0, heldBack: 0, merged: 0, total: 0 }, tEs)).toBe('No llegó nada en la última hora.')
  })

  it('formats relative time with Intl for the language', () => {
    const now = new Date('2026-11-04T12:00:00Z')
    expect(relativeTime('2026-11-04T11:55:00Z', now, 'es', tEs)).toMatch(/5\s*min/)
    expect(relativeTime('2026-11-04T11:59:40Z', now, 'es', tEs)).toBe('ahora mismo')
  })
})
