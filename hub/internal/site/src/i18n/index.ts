import { i18n } from '@lingui/core'

export type Locale = 'zh' | 'en'

export async function activateLocale(locale: Locale) {
  const { messages } = await import(`../locales/${locale}.po`)
  i18n.load(locale, messages)
  i18n.activate(locale)
}

/** 浏览器语言里带 zh 就用中文，否则英文。 */
export function detectLocale(): Locale {
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh' : 'en'
}
