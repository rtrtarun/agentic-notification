import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { describeHour, eventLabel, eventTitle, reasonText, relativeTime, statusLabel } from './events'
import { LOCALES, detectLocale, isLocale, localeDir, makeTranslator, saveLocale, tEn } from './i18n'
import { ar } from './i18n/ar'
import { de } from './i18n/de'
import { en } from './i18n/en'
import { es } from './i18n/es'
import { fr } from './i18n/fr'
import { hi } from './i18n/hi'
import { ja } from './i18n/ja'
import { pt } from './i18n/pt'
import { zh } from './i18n/zh'

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
  ['fr', fr],
  ['de', de],
  ['pt', pt],
  ['hi', hi],
  ['ar', ar],
  ['ja', ja],
  ['zh', zh],
] as const

// Screens added from milestone 16 on are written in English first. Translators can add
// these namespaces to a language at any time; until then English is shown. Everything
// older must stay fully translated.
const ENGLISH_ONLY_PREFIXES = ['manage.', 'all.', 'ov.', 'act.page.', 'ana.', 'rules.', 'pol.', 'ch.', 'int.', 'pref.', 'help.', 'live.', 'dl.', 'auth.', 'users.', 'nav.users', 'nav.digests', 'ovr.', 'dg.', 'learn.']
const isEnglishOnly = (key: string) => ENGLISH_ONLY_PREFIXES.some((p) => key.startsWith(p))

describe('catalogs', () => {
  const keys = Object.keys(en).filter((k) => !isEnglishOnly(k)).sort()

  it.each(catalogs)('%s defines every translated English key and nothing unknown', (_name, catalog) => {
    const own = Object.keys(catalog).filter((k) => !isEnglishOnly(k)).sort()
    expect(own).toEqual(keys)
    for (const key of Object.keys(catalog)) expect(key in en, key).toBe(true)
  })

  it.each(catalogs)('%s keeps every {placeholder} the English text has', (_name, catalog) => {
    const holders = (text: string) => (text.match(/\{\w+\}/g) ?? []).sort()
    for (const key of Object.keys(catalog) as (keyof typeof en)[]) {
      expect(holders(catalog[key] ?? ''), key).toEqual(holders(en[key]))
    }
  })

  it('has no empty text', () => {
    for (const catalog of [en, es, hi]) {
      for (const [key, text] of Object.entries(catalog)) expect(text.trim(), key).not.toBe('')
    }
  })

  it('lists every supported language once', () => {
    expect(LOCALES.map((l) => l.code).sort()).toEqual(['ar', 'de', 'en', 'es', 'fr', 'hi', 'ja', 'pt', 'zh'])
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
    vi.stubGlobal('navigator', { languages: ['it-IT'], language: 'it-IT' })
    expect(detectLocale()).toBe('en')
  })

  it('matches regional browser codes to the base language', () => {
    for (const [browser, expected] of [['fr-CA', 'fr'], ['de-AT', 'de'], ['pt-BR', 'pt'], ['pt-PT', 'pt'], ['ar-EG', 'ar'], ['ja-JP', 'ja'], ['zh-CN', 'zh'], ['zh-TW', 'zh']] as const) {
      vi.stubGlobal('navigator', { languages: [browser], language: browser })
      expect(detectLocale(), browser).toBe(expected)
    }
  })

  it('reads Arabic right to left and everything else left to right', () => {
    expect(localeDir('ar')).toBe('rtl')
    for (const l of LOCALES.filter((x) => x.code !== 'ar')) expect(l.dir, l.code).toBe('ltr')
  })

  it('gives every language a distinct name written in that language', () => {
    expect(new Set(LOCALES.map((l) => l.name)).size).toBe(LOCALES.length)
  })
})

describe('translated helpers', () => {
  const tEs = makeTranslator('es')
  const tHi = makeTranslator('hi')

  it('translates event labels, titles and statuses', () => {
    expect(eventTitle({ eventType: 'security_advisory', repository: 'acme/api' }, tEs)).toBe(
      'Aviso de seguridad en acme/api',
    )
    expect(eventTitle({ eventType: 'push', repository: 'acme/api' }, tHi)).toBe(`acme/api ${hi['ev.titleIn']!.split(' ')[1]} ${hi['ev.push']}`)
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
