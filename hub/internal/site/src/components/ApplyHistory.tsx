import { Trans } from '@lingui/react/macro'
import type { EventRecord } from '@/types/collections'

const APPLY_KINDS = new Set(['apply.ok', 'apply.failed', 'apply.rollback_failed'])

/**
 * 从事件流里筛 apply.* 回执，单独展示。
 * 完整事件流仍在下方「事件流」区块。
 */
export function ApplyHistory({ events }: { events: EventRecord[] }) {
  const items = events.filter((e) => APPLY_KINDS.has(e.kind))

  if (items.length === 0) {
    return (
      <p className="text-sm text-ink3">
        <Trans>暂无 apply 回执。</Trans>
      </p>
    )
  }

  return (
    <ul className="space-y-1.5">
      {items.map((e) => (
        <li key={e.id} className="flex gap-3 text-sm">
          <time className="w-40 shrink-0 font-mono text-xs text-ink3">
            {new Date(e.created).toLocaleString()}
          </time>
          <span className={kindClass(e.kind)}>
            {e.kind === 'apply.ok' && <Trans>应用成功</Trans>}
            {e.kind === 'apply.failed' && <Trans>应用失败</Trans>}
            {e.kind === 'apply.rollback_failed' && <Trans>回滚失败</Trans>}
          </span>
          {typeof e.detail?.error === 'string' && e.detail.error && (
            <span className="truncate font-mono text-xs text-rose-600">{e.detail.error}</span>
          )}
        </li>
      ))}
    </ul>
  )
}

function kindClass(kind: string): string {
  if (kind === 'apply.ok') return 'text-emerald-700 dark:text-emerald-300'
  if (kind === 'apply.rollback_failed') return 'font-medium text-rose-700 dark:text-rose-300'
  return 'text-rose-700 dark:text-rose-300'
}
