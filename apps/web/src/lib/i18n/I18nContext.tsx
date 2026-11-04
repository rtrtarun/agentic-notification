import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { detectLocale, localeDir, localeTag, makeTranslator, saveLocale, type Locale, type Translator } from './index'

interface I18n {
  locale: Locale
  /** BCP 47 tag for Intl formatting. */
  tag: string
  t: Translator
  setLocale: (locale: Locale) => void
}

const Context = createContext<I18n | null>(null)

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(detectLocale)

  const setLocale = useCallback((next: Locale) => {
    saveLocale(next)
    setLocaleState(next)
  }, [])

  // Screen readers and the browser's own controls follow the page language.
  useEffect(() => {
    document.documentElement.lang = localeTag(locale)
    // Right-to-left languages mirror the whole layout through the dir attribute.
    document.documentElement.dir = localeDir(locale)
  }, [locale])

  const value = useMemo<I18n>(
    () => ({ locale, tag: localeTag(locale), t: makeTranslator(locale), setLocale }),
    [locale, setLocale],
  )
  return <Context.Provider value={value}>{children}</Context.Provider>
}

export function useI18n(): I18n {
  const value = useContext(Context)
  if (!value) throw new Error('useI18n must be used inside I18nProvider')
  return value
}
