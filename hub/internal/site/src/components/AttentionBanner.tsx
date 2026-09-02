import { useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import type { MachineOverrideRecord, OverrideAttention } from '@/types/collections'

/**
 * 四段文案集中在一处，便于统一措辞（M1.8 spec R3）。
 * 机器详情页的「本机覆盖」区块复用同一份措辞。
 *
 * 做成 hook 而不是普通函数：Lingui 的宏只认**词法作用域里**的 t，
 * 把 t 当参数传进普通函数，模板字面量不会被转换，结果是空串。
 */
export function useAttentionText(): (a: OverrideAttention) => string {
  const { t } = useLingui()
  return (a: OverrideAttention) => {
    switch (a) {
      case 'hub_changed':
        return t`中台也改了同一处，本机的值挡下了它`
      case 'merge_conflict':
        return t`两边改法不同，已取本机的说法`
      case 'path_gone':
        return t`这个路径已不在中台的下发范围里，本轮没有生效`
      case 'unmergeable':
        return t`中台这份内容不是合法的 JSON 对象，本轮放弃合并`
      default:
        return ''
    }
  }
}

/**
 * 收件箱顶部的撞车横幅（M1.8 spec §6.3）。
 *
 * 中台赢不了，但必须说话：覆盖层一律取本机，这里只负责让用户知道
 * 有哪些中台更新被挡下了，以及给两个出口。
 */
export function AttentionBanner({
  items,
  machineName,
  busy,
  onDrop,
  onKeep,
  onCompare,
}: {
  items: MachineOverrideRecord[]
  machineName: Map<string, string>
  busy: boolean
  onDrop: (id: string) => void
  onKeep: (id: string) => void
  onCompare: (o: MachineOverrideRecord) => void
}) {
  const { t } = useLingui()
  const attentionText = useAttentionText()
  const [open, setOpen] = useState(false)
  if (items.length === 0) return null

  return (
    <section className="rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-3">
      <div className="flex flex-wrap items-center gap-3">
        <p className="flex-1 text-sm font-medium text-amber-800 dark:text-amber-200">
          <Trans>{items.length} 处本机覆盖挡下了中台更新</Trans>
        </p>
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          className="rounded bg-wash px-2 py-1 text-xs"
        >
          {open ? <Trans>收起</Trans> : <Trans>展开</Trans>}
        </button>
      </div>

      {open && (
        <ul className="mt-3 space-y-2">
          {items.map((o) => (
            <li
              key={o.id}
              className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded border border-line bg-surface px-3 py-2 text-xs"
            >
              <span className="text-ink2">{machineName.get(o.machine) ?? o.machine}</span>
              <span className="font-mono break-all">{o.path}</span>
              <span className="font-mono text-ink3">
                {o.kind === 'json_key' ? o.selector : t`整份文件`}
              </span>
              <span className="flex-1 text-ink3">{attentionText(o.attention)}</span>
              <button
                type="button"
                disabled={busy}
                onClick={() => onCompare(o)}
                className="rounded bg-wash px-2 py-1"
              >
                <Trans>三方对比</Trans>
              </button>
              <button
                type="button"
                disabled={busy}
                onClick={() => onDrop(o.id)}
                className="rounded bg-wash px-2 py-1"
              >
                <Trans>撤掉排除</Trans>
              </button>
              <button
                type="button"
                disabled={busy}
                onClick={() => onKeep(o.id)}
                className="rounded bg-wash px-2 py-1"
              >
                <Trans>保持</Trans>
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
