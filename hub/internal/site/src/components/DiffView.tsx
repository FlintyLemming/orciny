import { useMemo } from 'react'
import { Trans } from '@lingui/react/macro'
import { groupChanges, inlineDiff, type FileChange } from '@/lib/diffView'

export function DiffView({
  changes,
  diffs,
  localBefore,
  localAfter,
}: {
  changes?: FileChange[]
  /** hub 返回的 path → unified diff 文本 */
  diffs?: Record<string, string>
  /** 编辑器本地草稿 diff */
  localBefore?: string
  localAfter?: string
}) {
  const localRows = useMemo(() => {
    if (localBefore === undefined || localAfter === undefined) return null
    return inlineDiff(localBefore, localAfter)
  }, [localBefore, localAfter])

  const groups = useMemo(() => groupChanges(changes ?? []), [changes])

  if (localRows) {
    return (
      <pre className="overflow-auto rounded border border-line bg-wash p-3 font-mono text-xs leading-5">
        {localRows.map((r, i) => (
          <div
            key={i}
            className={
              r.type === 'add' ? 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300' :
              r.type === 'del' ? 'bg-rose-500/10 text-rose-700 dark:text-rose-300' :
              'text-ink3'
            }
          >
            {r.type === 'add' ? '+' : r.type === 'del' ? '-' : ' '}
            {r.text}
          </div>
        ))}
      </pre>
    )
  }

  if (groups.length === 0) {
    return (
      <p className="text-sm text-ink3">
        <Trans>无差异</Trans>
      </p>
    )
  }

  return (
    <div className="space-y-4">
      {groups.map((g) => (
        <section key={g.kind}>
          <h3 className="mb-1 text-xs font-semibold uppercase tracking-wide text-ink3">
            {g.label} ({g.files.length})
          </h3>
          <ul className="space-y-2">
            {g.files.map((f) => (
              <li key={f.path} className="rounded border border-line bg-surface">
                <div className="border-b border-line px-3 py-1.5 font-mono text-xs">{f.path}</div>
                {diffs?.[f.path] ? (
                  <pre className="overflow-auto p-3 font-mono text-xs leading-5 text-ink2">
                    {diffs[f.path]}
                  </pre>
                ) : (
                  <p className="px-3 py-2 text-xs text-ink3">
                    <Trans>无内容 diff</Trans>
                  </p>
                )}
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  )
}
