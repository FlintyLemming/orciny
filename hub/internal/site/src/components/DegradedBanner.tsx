import { useState } from 'react'
import { Trans } from '@lingui/react/macro'
import type { AssignmentState } from '@/types/collections'

/**
 * degraded 红色告警：apply 失败且回滚也失败后，hub 停止向该机下发。
 * 解除会把模式打回 survey，差异进收件箱而不是直接覆盖。
 */
export function DegradedBanner({
  state,
  lastError,
  onClear,
  busy,
}: {
  state: AssignmentState | string
  lastError: string
  onClear: () => void
  busy?: boolean
}) {
  const [confirming, setConfirming] = useState(false)

  if (state !== 'degraded') return null

  return (
    <div
      role="alert"
      className="rounded-lg border border-rose-500/40 bg-rose-500/10 p-4 text-sm text-rose-900 dark:text-rose-100"
    >
      <p className="mb-1 font-semibold">
        <Trans>本机处于降级状态</Trans>
      </p>
      {lastError && (
        <p className="mb-2 font-mono text-xs opacity-90">{lastError}</p>
      )}
      <p className="mb-3 leading-relaxed">
        <Trans>
          本机的一次配置应用失败，且自动回滚也未成功。为避免进一步破坏，orciny 已停止向这台机器应用任何配置。请先在机器上确认 ~/.claude 的状态，再点击解除——解除后本机会转为「先看看」模式，差异会进收件箱而不是直接覆盖。
        </Trans>
      </p>

      {!confirming ? (
        <button
          type="button"
          disabled={busy}
          onClick={() => setConfirming(true)}
          className="rounded bg-rose-600 px-3 py-1.5 text-xs text-white disabled:opacity-40"
        >
          <Trans>解除降级</Trans>
        </button>
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-xs">
            <Trans>确认已检查本机文件，并接受转为 survey？</Trans>
          </span>
          <button
            type="button"
            disabled={busy}
            onClick={onClear}
            className="rounded bg-rose-600 px-3 py-1.5 text-xs text-white disabled:opacity-40"
          >
            <Trans>确认解除</Trans>
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={() => setConfirming(false)}
            className="rounded bg-wash px-3 py-1.5 text-xs text-ink2"
          >
            <Trans>取消</Trans>
          </button>
        </div>
      )}
    </div>
  )
}
