import { useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { login } from '@/stores/auth'

export function Login() {
  const { t } = useLingui()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await login(email, password)
    } catch {
      // 不回显后端原文：登录失败的原因对攻击者比对用户更有价值。
      setError(t`邮箱或密码不正确`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-full items-center justify-center px-4">
      <form
        onSubmit={onSubmit}
        className="w-full max-w-sm rounded-lg border border-line bg-surface p-8"
      >
        <h1 className="mb-1 text-xl font-semibold">Orciny</h1>
        <p className="mb-6 text-sm text-ink2">
          <Trans>用管理员账号登录</Trans>
        </p>

        <label className="mb-1 block text-sm" htmlFor="email">
          <Trans>邮箱</Trans>
        </label>
        <input
          id="email"
          type="email"
          required
          autoFocus
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          className="mb-4 w-full rounded border border-line bg-page px-3 py-2"
        />

        <label className="mb-1 block text-sm" htmlFor="password">
          <Trans>密码</Trans>
        </label>
        <input
          id="password"
          type="password"
          required
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          className="mb-6 w-full rounded border border-line bg-page px-3 py-2"
        />

        {error && (
          <p role="alert" className="mb-4 text-sm text-crit">
            {error}
          </p>
        )}

        <button
          type="submit"
          disabled={busy}
          className="w-full rounded bg-[var(--pri-bg)] px-4 py-2 text-[var(--pri-ink)] disabled:opacity-60"
        >
          {busy ? <Trans>登录中…</Trans> : <Trans>登录</Trans>}
        </button>
      </form>
    </div>
  )
}
