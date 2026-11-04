// Colour themes. Each one is a set of CSS variables selected by
// <html data-theme="...">; see styles.css. The class colours keep their meaning
// in every theme: red stays critical.

export type ThemeId = 'attention' | 'harbour' | 'violet' | 'forest' | 'sunset' | 'night'

export interface ThemeInfo {
  id: ThemeId
  /** Page background, also used for the browser's address-bar colour. */
  background: string
  /** The theme's accent, shown as its swatch. */
  accent: string
}

export const THEMES: ThemeInfo[] = [
  { id: 'attention', background: '#0b1020', accent: '#8f7bff' },
  { id: 'harbour', background: '#e6ecee', accent: '#2058b8' },
  { id: 'violet', background: '#ece9f6', accent: '#5b3fd0' },
  { id: 'forest', background: '#e7efe8', accent: '#17734a' },
  { id: 'sunset', background: '#f4e9ee', accent: '#a3195b' },
  { id: 'night', background: '#0f1b26', accent: '#7fb0ff' },
]

const STORAGE_KEY = 'attention.theme'

export function isThemeId(value: unknown): value is ThemeId {
  return typeof value === 'string' && THEMES.some((t) => t.id === value)
}

/** Saved choice first, then light only if the system asks for it, otherwise the dark default. */
export function detectTheme(): ThemeId {
  try {
    const saved = localStorage.getItem(STORAGE_KEY)
    if (isThemeId(saved)) return saved
  } catch {
    // fall through
  }
  // Dark first: only a system that asks for light gets the light theme.
  if (typeof matchMedia === 'function' && matchMedia('(prefers-color-scheme: light)').matches) return 'harbour'
  return 'attention'
}

export function saveTheme(id: ThemeId): void {
  try {
    localStorage.setItem(STORAGE_KEY, id)
  } catch {
    // choice just will not persist
  }
}

/** Switches the page to a theme, including the browser chrome colour. */
export function applyTheme(id: ThemeId): void {
  document.documentElement.dataset.theme = id
  const info = THEMES.find((t) => t.id === id)
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', info?.background ?? '#0b1020')
}

export const THEME_EVENT = 'attention:theme'

/** Saves and applies a theme, and tells anything showing the current theme (the sidebar, the palette). */
export function changeTheme(id: ThemeId): void {
  saveTheme(id)
  applyTheme(id)
  window.dispatchEvent(new CustomEvent(THEME_EVENT, { detail: id }))
}
