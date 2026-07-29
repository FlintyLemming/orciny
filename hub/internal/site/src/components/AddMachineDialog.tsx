import { useEffect, useRef, useState } from 'react'
import { useStore } from '@nanostores/react'
import { Trans, useLingui } from '@lingui/react/macro'
import { Check, Copy, X } from 'lucide-react'
import { pb } from '@/lib/pb'
import { $machines, $machinesLoading } from '@/stores/machines'

interface TokenResponse {
  token: string
  expiresAt: string
  installCommand: string
}

export function AddMachineDialog({ onClose }: { onClose: () => void }) {
  const { t } = useLingui()
  const [data, setData] = useState<TokenResponse | null>(null)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const [remaining, setRemaining] = useState(0)
  const machines = useStore($machines)
  const loading = useStore($machinesLoading)
  const dialogRef = useRef<HTMLDivElement>(null)

  /**
   * 「哪些机器是本来就有的」这条基线必须等列表加载完再取。
   * 弹窗可能在首屏 getFullList 还没回来时就打开，那一刻列表是空的，
   * 基线取空集会让随后加载进来的每一台都算「新机器」，弹窗自己就关了。
   */
  const baseline = useRef<Set<string> | null>(null)
  if (baseline.current === null && !loading) {
    baseline.current = new Set(machines.map((m) => m.id))
  }

  // 签发 token。用 ref 挡住 StrictMode 的二次调用——否则每开一次弹窗
  // 就白签一枚 token，还多写一条 token.issued 事件。
  const issued = useRef(false)
  useEffect(() => {
    if (issued.current) return
    issued.current = true
    pb.send<TokenResponse>('/api/orciny/enroll-tokens', { method: 'POST' })
      .then(setData)
      .catch((e) => setError(String(e)))
  }, [])

  // 15 分钟倒计时
  useEffect(() => {
    if (!data) return
    const deadline = new Date(data.expiresAt).getTime()
    const tick = () => setRemaining(Math.max(0, Math.floor((deadline - Date.now()) / 1000)))
    tick()
    const id = setInterval(tick, 1000)
    return () => clearInterval(id)
  }, [data])

  // 新机器上线时自动关闭 —— 用户不需要猜什么时候刷新页面（spec §10.1）
  useEffect(() => {
    const known = baseline.current
    if (!known) return
    const fresh = machines.find((m) => !known.has(m.id))
    if (!fresh) return
    const id = window.setTimeout(onClose, 800)
    return () => window.clearTimeout(id)
  }, [machines, onClose])

  useEffect(() => {
    dialogRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  async function copy() {
    if (!data) return
    try {
      await navigator.clipboard.writeText(data.installCommand)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      // 非安全上下文（http 访问非 localhost）下 clipboard API 不可用。
      // 命令本身就在屏幕上，手动选中即可，不该因此弹错误。
      setError(t`无法写入剪贴板，请手动复制上面的命令。`)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-label={t`添加机器`}
        tabIndex={-1}
        className="w-full max-w-2xl rounded-lg border border-line bg-surface p-6"
      >
        <div className="mb-4 flex items-center">
          <h2 className="flex-1 text-base font-semibold">
            <Trans>添加机器</Trans>
          </h2>
          <button
            type="button"
            onClick={onClose}
            aria-label={t`关闭`}
            className="rounded p-1 hover:bg-wash"
          >
            <X size={16} aria-hidden />
          </button>
        </div>

        {error && (
          <p role="alert" className="text-sm text-crit">
            {error}
          </p>
        )}
        {!data && !error && (
          <p className="text-sm text-ink3">
            <Trans>正在签发注册 token…</Trans>
          </p>
        )}

        {data && (
          <>
            <p className="mb-3 text-sm text-ink2">
              <Trans>在目标机器上执行这行命令即可接入：</Trans>
            </p>
            <div className="mb-3 flex items-start gap-2 rounded border border-line bg-page p-3">
              <code className="min-w-0 flex-1 break-all font-mono text-xs">{data.installCommand}</code>
              <button
                type="button"
                onClick={copy}
                aria-label={t`复制安装命令`}
                className="shrink-0 rounded p-1.5 hover:bg-wash"
              >
                {copied ? <Check size={14} aria-hidden /> : <Copy size={14} aria-hidden />}
              </button>
            </div>
            <p className="text-xs text-ink3">
              <Trans>
                该 token 一次性使用，{formatCountdown(remaining)} 后过期。新机器上线后本窗口会自动关闭。
              </Trans>
            </p>
          </>
        )}
      </div>
    </div>
  )
}

function formatCountdown(sec: number) {
  const m = Math.floor(sec / 60)
  const s = sec % 60
  return `${m}:${String(s).padStart(2, '0')}`
}
