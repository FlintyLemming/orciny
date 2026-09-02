import { Trans } from '@lingui/react/macro'

/**
 * 「有几处本机覆盖」的角标（M1.8 spec R1）。
 *
 * 机器列表与机器详情页共用一个组件，而不是各写一份 JSX：两处各写一遍
 * 会抽出两条 msgid（一条位置参数、一条具名参数），翻译要填两遍且容易走样。
 *
 * 为什么需要这枚角标：有覆盖层的机器即使「已对齐」也和别人不一样。
 * 不加的话，用户建了几十个覆盖层之后面板上全是「已对齐」，
 * 而机队实际配置各不相同。
 */
export function OverrideBadge({ count }: { count: number }) {
  if (count <= 0) return null
  return (
    <span className="ml-2 rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] text-amber-700 dark:text-amber-300">
      <Trans>+{count} 本机覆盖</Trans>
    </span>
  )
}
