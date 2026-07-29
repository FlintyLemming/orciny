import { useStore } from '@nanostores/react'
import { $authed } from '@/stores/auth'
import { Login } from '@/pages/Login'
import { ThemeToggle } from '@/components/ThemeToggle'
import { LangToggle } from '@/components/LangToggle'

export function App() {
  const authed = useStore($authed)
  if (!authed) return <Login />
  return (
    <div className="p-8">
      <div className="flex gap-2">
        <ThemeToggle />
        <LangToggle />
      </div>
      <p className="mt-4">已登录（布局壳见下一任务）</p>
    </div>
  )
}
