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

  const usableProviders = providers.filter((p) => Boolean(p.claude?.base_url))
  if (usableProviders.length === 0) {
    return (
      <span className="text-xs text-ink3">
        <Trans>未配置 Claude 端点</Trans>
      </span>
    )
  }

  const currentProviderId = draftBinding?.provider || ''
  const currentMainBase = stripOneM(draftBinding?.models?.main ?? '')
  const isPass = isPassthrough(draftBinding?.models ?? emptySlots())
  const currentVal = !isPass && currentProviderId && currentMainBase
    ? `${currentProviderId}:${currentMainBase}`
    : ''

  async function handleSelect(val: string) {
    if (val === currentVal) return
    setBusy(true)
    try {
      let nextBinding = null
      let modelVal = ''

      if (val !== '') {
        const [pId, modelName] = val.split(':')
        const targetProv = providers.find((p) => p.id === pId)
        const targetModel = targetProv?.claude?.models?.find((m) => m.name === modelName)
        const targetOneM = Boolean(targetModel?.one_m)
        modelVal = targetOneM ? `${modelName}[1m]` : modelName
        nextBinding = {
          provider: pId,
          models: fillAllSlots(modelVal),
        }
      } else {
        // 透传模式：保留当前 provider 或首个可用 provider，四槽置空
        const pId = currentProviderId || usableProviders[0]?.id || ''
        nextBinding = {
          provider: pId,
          models: emptySlots(),
        }
      }

      await setBinding(set.id, nextBinding)

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
      <option value="">{t`透传（不指定模型）`}</option>
      {usableProviders.map((p) => (
        <optgroup key={p.id} label={p.name}>
          {(p.claude?.models ?? []).map((m) => (
            <option key={m.name} value={`${p.id}:${m.name}`}>
              {m.name}
              {m.one_m ? ' · 1M' : ''}
            </option>
          ))}
        </optgroup>
      ))}
    </select>
  )
}
