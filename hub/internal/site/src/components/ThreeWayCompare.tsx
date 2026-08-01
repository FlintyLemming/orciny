import { useMemo } from 'react'
import { Trans } from '@lingui/react/macro'
import { threeWayRows, type Side } from '@/lib/threeWay'

/**
 * 跨机器同路径冲突时的三方对比面板。
 * 基线居中参考，左右为两台机器的当前内容。
 */
export function ThreeWayCompare({
  base,
  left,
  right,
}: {
  base: Side
  left: Side
  right: Side
}) {
  const rows = useMemo(
    () => threeWayRows(base.content, left.content, right.content),
    [base.content, left.content, right.content],
  )

  const conflictCount = rows.filter((r) => r.conflicting).length

  return (
    <div className="space-y-2">
      {conflictCount > 0 && (
        <p className="text-sm text-rose-600">
          <Trans>发现 {conflictCount} 处两边改法不同的冲突行，请选定一侧后再收编。</Trans>
        </p>
      )}
      <div className="overflow-auto rounded border border-line">
        <table className="w-full min-w-[36rem] border-collapse font-mono text-xs">
          <thead className="bg-wash text-left text-ink2">
            <tr>
              <th className="border-b border-line px-2 py-1.5 font-medium">{base.label}</th>
              <th className="border-b border-line px-2 py-1.5 font-medium">{left.label}</th>
              <th className="border-b border-line px-2 py-1.5 font-medium">{right.label}</th>
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 && (
              <tr>
                <td colSpan={3} className="px-2 py-3 text-center text-ink3">
                  <Trans>无内容</Trans>
                </td>
              </tr>
            )}
            {rows.map((r, i) => (
              <tr
                key={i}
                className={
                  r.conflicting
                    ? 'bg-rose-500/10'
                    : r.same
                      ? ''
                      : 'bg-amber-500/5'
                }
              >
                <Cell text={r.base} />
                <Cell text={r.left} changed={r.left !== r.base} />
                <Cell text={r.right} changed={r.right !== r.base} />
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function Cell({ text, changed }: { text: string | null; changed?: boolean }) {
  if (text === null) {
    return (
      <td className="border-b border-line/60 px-2 py-0.5 text-ink3 italic">
        <Trans>∅</Trans>
      </td>
    )
  }
  return (
    <td
      className={`border-b border-line/60 px-2 py-0.5 whitespace-pre-wrap break-all ${
        changed ? 'text-accent' : 'text-ink2'
      }`}
    >
      {text}
    </td>
  )
}
