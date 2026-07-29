import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import './styles/tokens.css'
import { App } from './App'
import { $locale, $theme, applyLocale, applyTheme } from '@/stores/theme'

applyTheme($theme.get())
await applyLocale($locale.get())

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <I18nProvider i18n={i18n}>
      <App />
    </I18nProvider>
  </StrictMode>,
)
