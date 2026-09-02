import { Trans, useLingui } from '@lingui/react/macro'
import type { OverridePoint } from '@/lib/overridePoints'
import type { OverrideSelection } from '@/lib/api'

/**
 * 一条待转成覆盖层的漂移。
 * points 非空 = JSON 一路（勾 selector）；points 为 null = 文本一路（勾 hunk）。
 */
export interface OverrideTarget {
  id: string
  path: string
  /** hub 算好的 unified diff，文本一路按 @@ 块切开显示 */
  diff: string
  points: OverridePoint[] | null
  hunkCount: number
}

/** 文本一路的勾选键：h:<hunk 下标>。JSON 一路直接用 selector。 */
function hunkKey(i: number): string {
  return `h:${i}`
}

/** 默认全选（M1.8 spec §6.2）：用户来这里是为了保留，不是为了逐个挑。 */
export function defaultChecked(targets: OverrideTarget[]): Record<string, Set<string>> {
  const out: Record<string, Set<string>> = {}
  for (const t of targets) {
    out[t.id] = t.points
      ? new Set(t.points.map((p) => p.selector))
      : new Set(Array.from({ length: t.hunkCount }, (_, i) => hunkKey(i)))
  }
  return out
}

export function checkedCount(checked: Record<string, Set<string>>): number {
  return Object.values(checked).reduce((n, s) => n + s.size, 0)
}

/** 把勾选翻译成 API 的 points。空集合的 target 直接不出现 = 该条不做。 */
export function selectionsOf(
  targets: OverrideTarget[],
  checked: Record<string, Set<string>>,
): Record<string, OverrideSelection> {
  const out: Record<string, OverrideSelection> = {}
  for (const t of targets) {
    const set = checked[t.id]
    if (!set || set.size === 0) continue
    if (t.points) {
      out[t.id] = {
        selectors: t.points.map((p) => p.selector).filter((s) => set.has(s)),
      }
    } else {
      out[t.id] = {
        hunks: Array.from({ length: t.hunkCount }, (_, i) => i).filter((i) =>
          set.has(hunkKey(i)),
        ),
      }
    }
  }
  return out
}

/** 把 unified diff 按 @@ 切成块，块序号与后端的 hunk 下标一一对应。 */
function diffBlocks(diff: string): string[][] {
  const blocks: string[][] = []
  let cur: string[] | null = null
  for (const line of diff.split('\n')) {
    if (line.startsWith('@@')) {
      cur = []
      blocks.push(cur)
      continue
    }
    if (cur && (line.startsWith('+') || line.startsWith('-') || line.startsWith(' '))) {
      cur.push(line)
    }
  }
  return blocks
}

/**
 * 「本机保留」的勾选弹窗（M1.8 spec §6.2）。
 *
 * 单独成文件并具名导出：整页要连 store 与 api 一起 mock，
 * 组件单独导出才测得动（与 ReviewDialog 同一个理由）。
 */
export function OverrideDialog({
  targets,
  checked,
  busy,
  onToggle,
  onCancel,
  onConfirm,
}: {
  targets: OverrideTarget[]
  checked: Record<string, Set<string>>
  busy: boolean
  onToggle: (targetId: string, key: string) => void
  onCancel: () => void
  onConfirm: () => void
}) {
  const { t } = useLingui()
  const total = checkedCount(checked)

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      role="dialog"
    >
      <div className="max-h-[85vh] w-full max-w-3xl overflow-auto rounded-lg border border-line bg-surface p-5 shadow-xl">
        <h2 className="mb-2 text-base font-semibold">
          <Trans>本机保留</Trans>
        </h2>
        <p className="mb-4 text-sm text-ink3">
          <Trans>
            勾上的差异点归这台机器所有，中台以后改到别处照旧同步过来。
            默认全选，取消勾选的那些会退回中台的值。
          </Trans>
        </p>

        <ul className="mb-4 space-y-4">
          {targets.map((target) => (
            <li key={target.id} className="rounded border border-line">
              <div className="border-b border-line px-3 py-1.5 font-mono text-xs break-all">
                {target.path}
              </div>
              {target.points
                ? renderPoints(target, checked[target.id], onToggle)
                : renderHunks(target, checked[target.id], onToggle)}
            </li>
          ))}
        </ul>

        <div className="flex justify-end gap-2">
          <button
            type="button"
            className="rounded bg-wash px-3 py-1.5 text-sm"
            onClick={onCancel}
          >
            <Trans>取消</Trans>
          </button>
          <button
            type="button"
            disabled={busy || total === 0}
            className="rounded bg-emerald-600 px-3 py-1.5 text-sm text-white disabled:opacity-40"
            onClick={onConfirm}
          >
            {t`保留选中的 ${total} 处`}
          </button>
        </div>
      </div>
    </div>
  )
}

function renderPoints(
  target: OverrideTarget,
  checked: Set<string> | undefined,
  onToggle: (targetId: string, key: string) => void,
) {
  return (
    <table className="w-full border-collapse font-mono text-xs">
      <tbody>
        {target.points?.map((p) => (
          <tr key={p.selector} className="border-b border-line/60 last:border-0">
            <td className="w-8 px-3 py-1.5 align-top">
              <input
                type="checkbox"
                aria-label={p.selector}
                checked={checked?.has(p.selector) ?? false}
                onChange={() => onToggle(target.id, p.selector)}
              />
            </td>
            <td className="px-1 py-1.5 align-top break-all">{p.selector}</td>
            <td className="px-1 py-1.5 align-top break-all text-ink3">
              {p.base === '' ? <Trans>（无此键）</Trans> : p.base}
            </td>
            <td className="w-4 px-1 py-1.5 align-top text-ink3">→</td>
            <td className="px-3 py-1.5 align-top break-all text-accent">
              {p.mine === '' ? <Trans>（删掉这个键）</Trans> : p.mine}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function renderHunks(
  target: OverrideTarget,
  checked: Set<string> | undefined,
  onToggle: (targetId: string, key: string) => void,
) {
  const blocks = diffBlocks(target.diff)
  return (
    <ul className="divide-y divide-line/60">
      {blocks.map((block, i) => (
        <li key={i} className="flex gap-2 px-3 py-2">
          <input
            type="checkbox"
            className="mt-1"
            aria-label={`${target.path} #${i + 1}`}
            checked={checked?.has(hunkKey(i)) ?? false}
            onChange={() => onToggle(target.id, hunkKey(i))}
          />
          <pre className="min-w-0 flex-1 overflow-auto font-mono text-xs leading-5">
            {block.map((line, j) => (
              <div
                key={j}
                className={
                  line.startsWith('+')
                    ? 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
                    : line.startsWith('-')
                      ? 'bg-rose-500/10 text-rose-700 dark:text-rose-300'
                      : 'text-ink3'
                }
              >
                {line}
              </div>
            ))}
          </pre>
        </li>
      ))}
    </ul>
  )
}
