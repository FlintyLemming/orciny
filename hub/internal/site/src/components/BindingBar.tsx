import { useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { ChevronDown, ChevronRight } from 'lucide-react'
import {
  emptySlots,
  fillAllSlots,
  hasOneM,
  hasProviderRefs,
  isPassthrough,
  setOneM,
  setSlotsOneM,
  stripOneM,
} from '@/lib/binding'
import type { Binding, ModelSlots, ProviderRecord } from '@/types/collections'

/**
 * 配置集的「服务绑定」区（M1.5 spec §8.2）。
 *
 * 默认只露主模型下拉，**选定即四槽同填**——cc-switch 的 34 个带模型的预设
 * 全是这么干的。分开设置与透传模式收进「高级」。
 */
export function BindingBar({
  providers,
  binding,
  settingsText,
  onChange,
  onInsertSnippet,
}: {
  providers: ProviderRecord[]
  /** null = 未绑定 */
  binding: Binding | null
  /** 草稿里 .claude/settings.json 的当前文本；决定要不要显示「插入 env 片段」 */
  settingsText: string
  onChange: (b: Binding | null) => void
  onInsertSnippet: () => void
}) {
  const { t } = useLingui()
  const [advanced, setAdvanced] = useState(false)

  const provider = providers.find((p) => p.id === binding?.provider)
  // 模型清单取 claude 端点：绑定本期恒指 claude（M1.6 spec §1.3）。
  const models = provider?.claude?.models ?? []
  const slots = binding?.models ?? emptySlots()
  // [1m] 是槽位上的声明而不是独立模型：下拉一律列基名，标记单独一个复选框，
  // 否则同一个模型会在下拉里并排出现两次。
  const modelOptions = [...new Set(models.map(stripOneM))]
  const mainBase = stripOneM(slots.main)
  const oneM = hasOneM(slots.main)

  function setSlot(key: keyof ModelSlots, value: string) {
    if (!binding) return
    onChange({ ...binding, models: { ...binding.models, [key]: value } })
  }

  return (
    <div className="mb-3 rounded border border-line bg-surface px-3 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs text-ink3">
          <Trans>服务绑定</Trans>
        </span>

        <select
          aria-label={t`服务绑定`}
          className="rounded border border-line bg-wash px-2 py-1 text-sm"
          value={binding?.provider ?? ''}
          onChange={(e) => {
            const id = e.target.value
            if (!id) onChange(null)
            else onChange({ provider: id, models: emptySlots() })
          }}
        >
          <option value="">{t`未绑定`}</option>
          {providers.map((p) => {
            // 没配 claude 端点的 provider 绑不上：让不可能成功的操作在点下去
            // 之前就说明原因，而不是点完弹一个发布校验错误（M1.6 spec §5.3）。
            const usable = Boolean(p.claude?.base_url)
            return (
              <option
                key={p.id}
                value={p.id}
                disabled={!usable}
                title={usable ? undefined : t`这条服务配置还没有 Claude 端点`}
              >
                {p.name}
                {usable ? '' : t` （无 Claude 端点）`}
              </option>
            )
          })}
        </select>

        {binding && (
          <>
            <select
              aria-label={t`主模型`}
              className="rounded border border-line bg-wash px-2 py-1 font-mono text-sm"
              value={mainBase}
              onChange={(e) =>
                onChange({ ...binding, models: fillAllSlots(setOneM(e.target.value, oneM)) })
              }
            >
              <option value="">{t`透传（不指定模型）`}</option>
              {modelOptions.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </select>

            <label className="flex items-center gap-1 text-xs text-ink3">
              <input
                type="checkbox"
                aria-label={t`声明 1M 上下文`}
                checked={oneM}
                disabled={mainBase === ''}
                onChange={(e) =>
                  onChange({ ...binding, models: setSlotsOneM(slots, mainBase, e.target.checked) })
                }
              />
              <Trans>声明 1M</Trans>
            </label>

            <button
              type="button"
              className="flex items-center gap-1 rounded border border-line px-2 py-1 text-xs text-ink2"
              onClick={() => setAdvanced((v) => !v)}
            >
              {advanced ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
              <Trans>高级</Trans>
            </button>

            {!hasProviderRefs(settingsText) && (
              <button
                type="button"
                className="rounded bg-accent px-2 py-1 text-xs text-white"
                onClick={onInsertSnippet}
              >
                <Trans>插入 env 片段</Trans>
              </button>
            )}
          </>
        )}
      </div>

      {binding && advanced && (
        <div className="mt-2 flex flex-wrap items-center gap-3 border-t border-line pt-2">
          {(['opus', 'sonnet', 'haiku'] as const).map((k) => (
            <label key={k} className="flex items-center gap-1 text-xs text-ink3">
              {k}
              <select
                aria-label={k}
                className="rounded border border-line bg-wash px-2 py-1 font-mono text-sm"
                value={stripOneM(slots[k])}
                onChange={(e) => setSlot(k, setOneM(e.target.value, hasOneM(slots[k])))}
              >
                <option value="">{t`（空）`}</option>
                {modelOptions.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
            </label>
          ))}

          {/* 透传 = 四槽清空，对应那 35 个不设模型变量的中转预设。 */}
          <label className="flex items-center gap-1 text-xs text-ink3">
            <input
              type="checkbox"
              aria-label={t`透传模式`}
              checked={isPassthrough(slots)}
              onChange={(e) => {
                if (e.target.checked) onChange({ ...binding, models: emptySlots() })
              }}
            />
            <Trans>透传模式</Trans>
          </label>
        </div>
      )}
    </div>
  )
}
