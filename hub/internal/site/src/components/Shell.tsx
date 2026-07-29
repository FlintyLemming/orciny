import { useStore } from '@nanostores/react'
import { Trans } from '@lingui/react/macro'
import { LogOut } from 'lucide-react'
import { $authEmail, logout } from '@/stores/auth'
import { Sidebar } from './Sidebar'
import { ThemeToggle } from './ThemeToggle'
import { LangToggle } from './LangToggle'

export function Shell({ children }: { children: React.ReactNode }) {
  const email = useStore($authEmail)
  return (
    <div className="flex h-full">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center gap-3 border-b border-line px-6 py-3">
          <div className="flex-1" />
          <ThemeToggle />
          <LangToggle />
          <span className="text-sm text-ink3">{email}</span>
          <button
            type="button"
            onClick={logout}
            className="flex items-center gap-1 rounded px-2 py-1 text-sm hover:bg-wash"
          >
            <LogOut size={14} aria-hidden />
            <Trans>退出</Trans>
          </button>
        </header>
        <main className="min-h-0 flex-1 overflow-auto p-6">{children}</main>
      </div>
    </div>
  )
}
