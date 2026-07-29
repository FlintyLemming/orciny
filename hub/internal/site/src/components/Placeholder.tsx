import { Trans } from '@lingui/react/macro'

/**
 * 未实现页面的统一空状态（spec §10.2）。
 * 各页面不要自己编空状态——统一在这里，将来批量替换也方便。
 */
export function Placeholder({ milestone }: { milestone: 'M1' | 'M2' | 'M3' }) {
  return (
    <div className="flex h-full items-center justify-center">
      <p className="rounded border border-dashed border-line px-6 py-8 text-sm text-ink3">
        <Trans>该功能将在 {milestone} 提供</Trans>
      </p>
    </div>
  )
}
