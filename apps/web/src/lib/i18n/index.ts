// A small translation layer with no dependencies. English is the fallback for
// anything a language does not define, and a missing key shows the key itself
// so the gap is visible instead of silent.

import { en, type MessageKey, type Messages } from './en'
import { ar } from './ar'
import { de } from './de'
import { es } from './es'
import { fr } from './fr'
import { hi } from './hi'
import { ja } from './ja'
import { pt } from './pt'
import { zh } from './zh'

export type Locale = 'en' | 'es' | 'fr' | 'de' | 'pt' | 'hi' | 'ar' | 'ja' | 'zh'

export interface LocaleInfo {
  code: Locale
  /** The language's own name, so it is readable whatever language is active. */
  name: string
  /** BCP 47 tag for Intl formatting. */
  tag: string
  /** Reading direction. Arabic is right to left; the page mirrors itself. */
  dir: 'ltr' | 'rtl'
}

export const LOCALES: LocaleInfo[] = [
  { code: 'en', name: 'English', tag: 'en', dir: 'ltr' },
  { code: 'es', name: 'Español', tag: 'es', dir: 'ltr' },
  { code: 'fr', name: 'Français', tag: 'fr', dir: 'ltr' },
  { code: 'de', name: 'Deutsch', tag: 'de', dir: 'ltr' },
  { code: 'pt', name: 'Português (Brasil)', tag: 'pt-BR', dir: 'ltr' },
  { code: 'hi', name: 'हिन्दी', tag: 'hi', dir: 'ltr' },
  { code: 'ar', name: 'العربية', tag: 'ar', dir: 'rtl' },
  { code: 'ja', name: '日本語', tag: 'ja', dir: 'ltr' },
  { code: 'zh', name: '简体中文', tag: 'zh-CN', dir: 'ltr' },
]

const CATALOGS: Record<Locale, Partial<Messages>> = { en, es, fr, de, pt, hi, ar, ja, zh }
const STORAGE_KEY = 'attention.locale'

export type Params = Record<string, string | number>
export type Translator = (key: MessageKey, params?: Params) => string

export function isLocale(value: unknown): value is Locale {
  return typeof value === 'string' && LOCALES.some((l) => l.code === value)
}

export function localeDir(locale: Locale): 'ltr' | 'rtl' {
  return LOCALES.find((l) => l.code === locale)?.dir ?? 'ltr'
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
