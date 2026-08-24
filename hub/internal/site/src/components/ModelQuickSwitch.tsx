import { useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import type { ConfigSetRecord, ProviderRecord } from '@/types/collections'
import { diffAgainstHead } from '@/lib/draftState'
import {
  emptySlots,
  fillAllSlots,
  isPassthrough,
  isSlotsSplit,
  stripOneM,
} from '@/lib/binding'
import {
  publishConfigSet,
  rollbackConfigSet,
  setBinding,
  validateConfigSet,
} from '@/lib/api'
import { reloadConfigSets } from '@/stores/configsets'
import { showToast } from '@/stores/toast'
import { navigate } from '@/router'

export function ModelQuickSwitch({
  set,
  providers,
  affectedMachines,
  onOpenPublishDialog,
}: {
  set: ConfigSetRecord
  providers: ProviderRecord[]
  affectedMachines: number
  onOpenPublishDialog?: () => void
}) {
  const { t } = useLingui()
  const [busy, setBusy] = useState(false)

  // 护栏 1 (§5.1): 草稿有其它未发布改动（文件增删改）→ 快切禁用
  const diff = diffAgainstHead(set)
  if (diff.files) {
    return (
      <button
        type="button"
        onClick={() => navigate('configsets', set.id)}
        className="text-xs text-amber-600 hover:underline"
      >
        <Trans>有未发布改动 →</Trans>
      </button>
    )
  }

  // 护栏 2 (§5.2): 四槽分设过 → 快切禁用
  const draftBinding = set.draft_binding
  if (isSlotsSplit(draftBinding?.models)) {
    return (
      <button
        type="button"
        onClick={() => navigate('configsets', set.id)}
        className="text-xs text-ink3 hover:underline"
      >
        <Trans>已分设 →</Trans>
      </button>
    )
  }

  const providerId = draftBinding?.provider || ''
  const provider = providers.find((p) => p.id === providerId)

  if (!provider || !provider.claude?.base_url) {
    return (
      <span className="text-xs text-ink3">
        {providerId ? <Trans>未配置 Claude 端点</Trans> : <Trans>未绑定</Trans>}
      </span>
    )
  }

  const models = provider.claude.models ?? []
  const currentVal = isPassthrough(draftBinding?.models ?? emptySlots())
    ? ''
    : stripOneM(draftBinding?.models?.main ?? '')

  async function handleSelect(modelName: string) {
    setBusy(true)
    try {
      let modelVal = ''
      if (modelName !== '') {
        const targetModel = models.find((m) => m.name === modelName)
        const targetOneM = Boolean(targetModel?.one_m)
        modelVal = targetOneM ? `${modelName}[1m]` : modelName
      }

      const nextSlots = modelVal ? fillAllSlots(modelVal) : emptySlots()
      await setBinding(set.id, {
        provider: providerId,
        models: nextSlots,
      })

      // 校验是否有阻断项
      const problems = await validateConfigSet(set.id)
      const hasBlocking = problems.some((p) => !p.warning)
      if (hasBlocking) {
        onOpenPublishDialog?.()
        return
      }

      const headBefore = set.head
      const note = modelVal ? t`快切模型至 ${modelVal}` : t`快切模型至 透传`
      await publishConfigSet(set.id, note)
      void reloadConfigSets()

      showToast(
        modelVal
          ? t`已切到 ${modelVal} · 影响 ${affectedMachines} 台`
          : t`已切到透传 · 影响 ${affectedMachines} 台`,
        headBefore
          ? async () => {
              await rollbackConfigSet(set.id, headBefore)
              void reloadConfigSets()
            }
          : undefined,
        8000,
      )
    } finally {
      setBusy(false)
    }
  }

  return (
    <select
      aria-label={t`快切模型`}
      disabled={busy}
      value={currentVal}
      onChange={(e) => void handleSelect(e.target.value)}
      className="rounded border border-line bg-wash px-2 py-1 font-mono text-xs text-ink disabled:opacity-50"
    >
      <option value="">{t`透传`}</option>
      {models.map((m) => (
        <option key={m.name} value={m.name}>
          {m.name}
        </option>
      ))}
    </select>
  )
}
