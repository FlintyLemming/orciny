import { atom } from 'nanostores'
import { activateLocale, detectLocale, type Locale } from '@/i18n'

export type Theme = 'light' | 'dark' | 'system'

const THEME_KEY = 'orciny.theme'
const LOCALE_KEY = 'orciny.locale'

export const $theme = atom<Theme>((localStorage.getItem(THEME_KEY) as Theme) ?? 'system')
export const $locale = atom<Locale>((localStorage.getItem(LOCALE_KEY) as Locale) ?? detectLocale())

/** system 时移除 data-theme，交回给 prefers-color-scheme。 */
export function applyTheme(t: Theme) {
  const root = document.documentElement
  if (t === 'system') root.removeAttribute('data-theme')
  else root.setAttribute('data-theme', t)
  localStorage.setItem(THEME_KEY, t)
  $theme.set(t)
}

export async function applyLocale(l: Locale) {
  await activateLocale(l)
  document.documentElement.lang = l
  localStorage.setItem(LOCALE_KEY, l)
  $locale.set(l)
}
