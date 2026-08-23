import { useMemo, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import type { Finding } from '@/types/collections'
import { ConfirmDialog } from '@/components/ConfirmDialog'

export type FindingAction = 'extract' | 'keep' | 'exclude'

export function FindingList({
  findings,
  onAction,
}: {
  findings: Finding[]
  onAction: (action: FindingAction, finding: Finding) => void
}) {
  const sorted = useMemo(() => {
    return findings.slice().sort((a, b) => {
      const as = a.rule === 'structured' ? 0 : 1
      const bs = b.rule === 'structured' ? 0 : 1
      if (as !== bs) return as - bs
      return a.path.localeCompare(b.path) || a.location.localeCompare(b.location)
    })
  }, [findings])

  const { t } = useLingui()
  const [confirmKeep, setConfirmKeep] = useState<Finding | null>(null)

  return (
    <>
      <ul className="divide-y divide-line rounded border border-line" role="list">
        {sorted.map((f, i) => (
          <li key={`${f.path}:${f.location}:${i}`} role="listitem" className="flex flex-wrap items-center gap-2 px-3 py-2 text-sm">
            <div className="min-w-0 flex-1">
              <div className="font-mono text-xs text-ink2">{f.path}</div>
              <div className="text-xs">
                <span className="text-ink3">{f.location}</span>
                {' · '}
                <span className="font-mono">{f.masked}</span>
                {' · '}
                <span className="text-ink3">{f.rule}</span>
              </div>
            </div>
            <button
              type="button"
              className="rounded bg-accent px-2 py-1 text-xs text-white"
              onClick={() => onAction('extract', f)}
            >
              <Trans>抽成服务配置的 key</Trans>
            </button>
            <button
              type="button"
              className="rounded bg-wash px-2 py-1 text-xs"
              onClick={() => setConfirmKeep(f)}
            >
              <Trans>保留明文</Trans>
            </button>
            <button
              type="button"
              className="rounded px-2 py-1 text-xs text-ink2 hover:bg-wash"
              onClick={() => onAction('exclude', f)}
            >
              <Trans>移出纳管</Trans>
            </button>
          </li>
        ))}
      </ul>

      {confirmKeep && (
        <ConfirmDialog
          message={t`该值将进入不可变的版本历史。非 AI 平台的密钥没有加密去处——可以改用机器变量（不加密），或把这个文件移出纳管范围。确定保留明文？`}
          onConfirm={() => {
            onAction('keep', confirmKeep)
            setConfirmKeep(null)
          }}
          onClose={() => setConfirmKeep(null)}
        />
      )}
    </>
  )
}
