import { useStore } from '@nanostores/react'
import { Trans } from '@lingui/react/macro'
import { Monitor, Moon, Sun } from 'lucide-react'
import { $theme, applyTheme, type Theme } from '@/stores/theme'

const options: { value: Theme; icon: typeof Sun }[] = [
  { value: 'light', icon: Sun },
  { value: 'dark', icon: Moon },
  { value: 'system', icon: Monitor },
]

export function ThemeToggle() {
  const theme = useStore($theme)
  return (
    <div role="group" aria-label="theme" className="flex gap-1">
      {options.map(({ value, icon: Icon }) => (
        <button
          key={value}
          type="button"
          aria-pressed={theme === value}
          onClick={() => applyTheme(value)}
          className={`rounded p-1.5 ${theme === value ? 'bg-wash2' : 'hover:bg-wash'}`}
        >
          <Icon size={16} aria-hidden />
          <span className="sr-only">
            {value === 'light' ? (
              <Trans>浅色</Trans>
            ) : value === 'dark' ? (
              <Trans>深色</Trans>
            ) : (
              <Trans>跟随系统</Trans>
            )}
          </span>
        </button>
      ))}
    </div>
  )
}
