import { Trans } from '@lingui/react/macro'
import type { BindingMatchResult } from '@/types/collections'

/**
 * 绑定漂移的反查三档动作（M1.5 spec §6.3）。
 *
 * 措辞纪律：match.exact === false 时降级为「可能是」，且动作仍需用户确认，
 * 不自动执行——host 匹配可能把个人版认成团队版（spec §13）。
 */
export function BindingDriftActions({
  match,
  onRebind,
  onCreateProvider,
}: {
  /** undefined = 反查还没回来 */
  match?: BindingMatchResult
  onRebind: (providerId: string) => void
  onCreateProvider: (presetId: string, keyLocation: string) => void
}) {
  if (!match) return null

  // 第一档：命中已有 Provider → 改绑定，产生新 Revision，全机队跟着走。
  if (match.match.kind === 'provider' && match.match.provider_id) {
    const name = match.match.provider_name ?? ''
    return (
      <div className="mt-1">
        <p>
          {match.match.exact ? (
            <Trans>这台机器改用了「{name}」。把配置集的绑定改成它？</Trans>
          ) : (
            <Trans>这台机器用的地址可能是「{name}」。把配置集的绑定改成它？</Trans>
          )}
        </p>
        <button
          type="button"
          className="mt-1 rounded bg-accent px-2 py-1 text-xs text-white"
          onClick={() => onRebind(match.match.provider_id!)}
        >
          <Trans>改成它</Trans>
        </button>
      </div>
    )
  }

  // 第二档：命中内置预设 → 新建向导，顺手把机器上手写的 key 抽成凭据。
  if (match.match.kind === 'preset' && match.match.preset_id) {
    const presetName = match.match.preset_name ?? ''
    return (
      <div className="mt-1">
        <p>
          {match.match.exact ? (
            <Trans>识别为「{presetName}」。新建服务配置？</Trans>
          ) : (
            <Trans>可能是「{presetName}」。新建服务配置？</Trans>
          )}
        </p>
        {match.key_masked && (
          <p className="mt-1 text-ink3">
            <Trans>机器上那把 key（{match.key_masked}）会被抽成凭据。</Trans>
          </p>
        )}
        <button
          type="button"
          className="mt-1 rounded bg-accent px-2 py-1 text-xs text-white"
          onClick={() => onCreateProvider(match.match.preset_id!, match.key_location ?? '')}
        >
          <Trans>新建服务配置</Trans>
        </button>
      </div>
    )
  }

  // 第三档：都不命中的兜底文案（子计划 06 已就位，这里保持一处）。
  return (
    <p className="mt-1 text-ink3">
      <Trans>无法识别这个 base_url 属于哪个平台。可以「恢复」把它拉回基线，或者「忽略」。</Trans>
    </p>
  )
}
