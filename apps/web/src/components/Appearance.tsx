import { useState } from 'react'
import { LOCALES, isLocale } from '../lib/i18n'
import { useI18n } from '../lib/i18n/I18nContext'
import { THEMES, applyTheme, detectTheme, saveTheme, type ThemeId } from '../lib/theme'

/** Language and colour theme. Both apply at once and are remembered in this browser. */
export function Appearance() {
  const { t, locale, setLocale } = useI18n()
  const [theme, setTheme] = useState<ThemeId>(() => detectTheme())

  const chooseTheme = (id: ThemeId) => {
    saveTheme(id)
    applyTheme(id)
    setTheme(id)
  }

  return (
    <div className="appearance">
      <label className="appearance-label" htmlFor="language">
        {t('appearance.language')}
      </label>
      <select
        id="language"
        className="field select"
        value={locale}
        onChange={(e) => {
          if (isLocale(e.target.value)) setLocale(e.target.value)
        }}
      >
        {LOCALES.map((l) => (
          <option key={l.code} value={l.code} lang={l.tag}>
            {l.name}
          </option>
        ))}
      </select>

      <fieldset className="swatches">
        <legend className="appearance-label">{t('appearance.theme')}</legend>
        <div className="swatch-row">
          {THEMES.map((th) => (
            <label key={th.id} className="swatch" title={t(`theme.${th.id}`)}>
              <input
                type="radio"
                name="theme"
                value={th.id}
                checked={theme === th.id}
                onChange={() => chooseTheme(th.id)}
              />
              <span className="swatch-chip" style={{ background: th.background, borderColor: th.accent }}>
                <span className="swatch-dot" style={{ background: th.accent }} />
              </span>
              <span className="visually-hidden">{t(`theme.${th.id}`)}</span>
            </label>
          ))}
        </div>
      </fieldset>
    </div>
  )
}
