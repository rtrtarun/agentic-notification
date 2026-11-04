// A small translation layer with no dependencies. English is the fallback for
// anything a language does not define, and a missing key shows the key itself
// so the gap is visible instead of silent.

import { en, type MessageKey, type Messages } from './en'
import { es } from './es'
import { hi } from './hi'

export type Locale = 'en' | 'es' | 'hi'

export interface LocaleInfo {
  code: Locale
  /** The language's own name, so it is readable whatever language is active. */
  name: string
  /** BCP 47 tag for Intl formatting. */
  tag: string
}

export const LOCALES: LocaleInfo[] = [
  { code: 'en', name: 'English', tag: 'en' },
  { code: 'es', name: 'Español', tag: 'es' },
  { code: 'hi', name: 'हिन्दी', tag: 'hi' },
]

const CATALOGS: Record<Locale, Messages> = { en, es, hi }
const STORAGE_KEY = 'attention.locale'

export type Params = Record<string, string | number>
export type Translator = (key: MessageKey, params?: Params) => string

export function isLocale(value: unknown): value is Locale {
  return typeof value === 'string' && LOCALES.some((l) => l.code === value)
}

export function localeTag(locale: Locale): string {
  return LOCALES.find((l) => l.code === locale)?.tag ?? 'en'
}

export function makeTranslator(locale: Locale): Translator {
  const catalog = CATALOGS[locale]
  return (key, params) => {
    const template = catalog[key] ?? en[key] ?? key
    if (!params) return template
    return template.replace(/\{(\w+)\}/g, (whole, name: string) => (name in params ? String(params[name]) : whole))
  }
}

/** The English translator, used as the default by pure helpers and tests. */
export const tEn: Translator = makeTranslator('en')

/** Saved choice first, then the browser's language, then English. */
export function detectLocale(): Locale {
  try {
    const saved = localStorage.getItem(STORAGE_KEY)
    if (isLocale(saved)) return saved
  } catch {
    // storage unavailable: fall through to the browser language
  }
  const preferred = typeof navigator !== 'undefined' ? (navigator.languages ?? [navigator.language]) : []
  for (const tag of preferred) {
    const base = tag.toLowerCase().split('-')[0]
    if (isLocale(base)) return base
  }
  return 'en'
}

export function saveLocale(locale: Locale): void {
  try {
    localStorage.setItem(STORAGE_KEY, locale)
  } catch {
    // choice just will not persist
  }
}

export type { MessageKey, Messages }
