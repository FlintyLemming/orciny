import { Trans } from '@lingui/react/macro'
import { parseUnifiedDiff, type DriftEvent } from '@/lib/inbox'

const kindLabel: Record<DriftEvent['kind'], React.ReactNode> = {
  added: <Trans>新增</Trans>,
  modified: <Trans>修改</Trans>,
  deleted: <Trans>删除</Trans>,
}

export function DriftCard({
  event,
  selected,
  onToggle,
  machineLabel,
  setLabel,
}: {
  event: DriftEvent
  selected: boolean
  onToggle: () => void
  machineLabel?: string
  setLabel?: string
}) {
  // truncated 不能收编：内容没上来，勾选也没用
  const locked = event.truncated
  const lines = parseUnifiedDiff(event.diff)

  return (
    <article
      className={`rounded-lg border bg-surface ${
        selected ? 'border-accent' : 'border-line'
      }`}
    >
      <header className="flex items-start gap-2 border-b border-line px-3 py-2">
        <input
          type="checkbox"
          className="mt-1"
          checked={selected}
          disabled={locked}
          onChange={onToggle}
          aria-label={event.path}
        />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-sm break-all">{event.path}</span>
            <span className="rounded bg-wash px-1.5 py-0.5 text-[10px] uppercase text-ink3">
              {kindLabel[event.kind]}
            </span>
            {event.state === 'superseded' && (
              <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] text-amber-700 dark:text-amber-300">
                <Trans>已被覆盖</Trans>
              </span>
            )}
            {event.restore_partial && (
              <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] text-amber-700 dark:text-amber-300">
                <Trans>需人工复核</Trans>
              </span>
            )}
          </div>
          <p className="mt-0.5 text-xs text-ink3">
            {machineLabel ?? event.machine}
            {setLabel ? ` · ${setLabel}` : ''}
            {' · '}
            {new Date(event.created).toLocaleString()}
          </p>
        </div>
      </header>

      {event.truncated && (
        <p className="border-b border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-700 dark:text-rose-300">
          <Trans>该文件含未能安全脱敏的凭据，请在 Web 上手工处理</Trans>
        </p>
      )}

      {lines.length > 0 ? (
        <pre className="max-h-48 overflow-auto p-3 font-mono text-xs leading-5">
          {lines.map((l, i) => (
            <div
              key={i}
              className={
                l.type === 'add' ? 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300' :
                l.type === 'del' ? 'bg-rose-500/10 text-rose-700 dark:text-rose-300' :
                l.type === 'meta' ? 'text-ink3' :
                'text-ink2'
              }
            >
              {l.type !== 'meta' && (
                <span aria-hidden>
                  {l.type === 'add' ? '+' : l.type === 'del' ? '-' : ' '}
                </span>
              )}
              <span>{l.text}</span>
            </div>
          ))}
        </pre>
      ) : !event.truncated ? (
        <p className="px-3 py-2 text-xs text-ink3">
          <Trans>无内容 diff</Trans>
        </p>
      ) : null}
    </article>
  )
}
