import { useEffect, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { X } from 'lucide-react'
import type { ProviderRecord } from '@/types/collections'

/**
 * 抽取对话框：把扫出来的明文值写进某条 provider 的某个端点 key
 * （M1.6 spec §5.5）。
 *
 * 它取代了 M1 那个「输入一个凭据名」的 PromptDialog——凭据实体没了，
 * 现在要选的是**哪条 provider 的哪个端点**。
 */
export function ExtractDialog({
  providers,
  suggestedProviderId,
  masked,
  busy = false,
  onSubmit,
  onClose,
}: {
  providers: ProviderRecord[]
  /** 反查命中时预选它（spec §5.5 第 2 步）。反查不中就让用户自己选。 */
  suggestedProviderId?: string
  /** 被抽取值的掩码。明文不显示——它正是要被藏起来的东西。 */
  masked: string
  busy?: boolean
  onSubmit: (providerId: string, endpoint: 'claude' | 'openai') => void
  onClose: () => void
}) {
  const { t } = useLingui()
  const [provider, setProvider] = useState(suggestedProviderId ?? providers[0]?.id ?? '')
  // 端点默认 claude：受管范围目前只有 .claude/**，绝大多数抽取都落在那一侧。
  const [endpoint, setEndpoint] = useState<'claude' | 'openai'>('claude')

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const empty = providers.length === 0

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      role="dialog"
    >
      <div
        className="w-full max-w-md rounded-lg border border-line bg-surface p-5 shadow-xl"
        aria-modal="true"
        aria-label={t`抽成服务配置的 key`}
      >
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-base font-semibold">
            <Trans>抽成服务配置的 key</Trans>
          </h2>
          <button type="button" onClick={onClose} aria-label={t`关闭`}>
            <X size={16} />
          </button>
        </div>

        <div className="mb-4 rounded bg-wash px-3 py-2 font-mono text-sm text-ink2">{masked}</div>

        <label className="mb-1 block text-xs text-ink3" htmlFor="extract-provider">
          <Trans>服务配置</Trans>
        </label>
        <select
          id="extract-provider"
          aria-label={t`服务配置`}
          className="mb-3 w-full rounded border border-line bg-wash px-2 py-1.5 text-sm"
          value={provider}
          onChange={(e) => setProvider(e.target.value)}
          disabled={empty}
        >
          {providers.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>

        <label className="mb-1 block text-xs text-ink3" htmlFor="extract-endpoint">
          <Trans>端点</Trans>
        </label>
        <select
          id="extract-endpoint"
          aria-label={t`端点`}
          className="mb-3 w-full rounded border border-line bg-wash px-2 py-1.5 font-mono text-sm"
          value={endpoint}
          onChange={(e) => setEndpoint(e.target.value as 'claude' | 'openai')}
          disabled={empty}
        >
          <option value="claude">claude</option>
          <option value="openai">openai</option>
        </select>

        {empty && (
          <p className="mb-3 text-xs text-ink3">
            <Trans>先到「AI 服务」页建一条，再回来抽取。</Trans>
          </p>
        )}

        <div className="flex justify-end gap-2">
          <button
            type="button"
            className="rounded border border-line px-3 py-1.5 text-sm text-ink2"
            onClick={onClose}
          >
            <Trans>取消</Trans>
          </button>
          <button
            type="button"
            className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40"
            disabled={empty || busy || provider === ''}
            onClick={() => onSubmit(provider, endpoint)}
          >
            <Trans>抽取</Trans>
          </button>
        </div>
      </div>
    </div>
  )
}
