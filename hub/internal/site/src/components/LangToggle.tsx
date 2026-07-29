import { useStore } from '@nanostores/react'
import { $locale, applyLocale } from '@/stores/theme'

export function LangToggle() {
  const locale = useStore($locale)
  return (
    <button
      type="button"
      onClick={() => applyLocale(locale === 'zh' ? 'en' : 'zh')}
      className="rounded px-2 py-1 text-sm hover:bg-wash"
      aria-label="language"
    >
      {locale === 'zh' ? 'EN' : '中'}
    </button>
  )
}
