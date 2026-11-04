import { useEffect, useState } from 'react'
import { useI18n } from '../lib/i18n/I18nContext'
import { THEME_EVENT, changeTheme, detectTheme, type ThemeId } from '../lib/theme'

const DARK: ThemeId[] = ['attention', 'night']

/** One button that flips between the dark and light Attention themes. The full theme list lives in Settings. */
export function ThemeToggle() {
  const { t } = useI18n()
  const [theme, setTheme] = useState<ThemeId>(() => detectTheme())

  // The palette and Settings can change the theme too, so follow them.
  useEffect(() => {
    const onTheme = (e: Event) => setTheme((e as CustomEvent<ThemeId>).detail)
    window.addEventListener(THEME_EVENT, onTheme)
    return () => window.removeEventListener(THEME_EVENT, onTheme)
  }, [])

  const dark = DARK.includes(theme)
  return (
    <button
      type="button"
      className="theme-toggle"
      role="switch"
      aria-checked={dark}
      aria-label={dark ? t('theme.toLight') : t('theme.toDark')}
      title={dark ? t('theme.toLight') : t('theme.toDark')}
      onClick={() => changeTheme(dark ? 'daylight' : 'attention')}
    >
      <span className="theme-toggle-knob" aria-hidden="true">
        {dark ? '☾' : '☀'}
      </span>
    </button>
  )
}
