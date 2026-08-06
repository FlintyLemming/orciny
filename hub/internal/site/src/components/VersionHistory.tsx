import { useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import type { RevisionRecord } from '@/types/collections'
import { diffConfigSet, rollbackConfigSet } from '@/lib/api'
import { DiffView } from '@/components/DiffView'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import type { FileChange } from '@/lib/diffView'

export function VersionHistory({
  setId,
  revisions,
  onRolledBack,
}: {
  setId: string
  revisions: RevisionRecord[]
  onRolledBack: () => void
}) {
  const { t } = useLingui()
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [changes, setChanges] = useState<FileChange[]>([])
  const [diffs, setDiffs] = useState<Record<string, string>>({})
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [pendingRollback, setPendingRollback] = useState<RevisionRecord | null>(null)

  async function runDiff() {
    if (!from || !to) return
    setBusy(true)
    setError('')
    try {
      const res = await diffConfigSet(setId, from, to)
      setChanges(res.changes ?? [])
      setDiffs(res.diffs ?? {})
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function handleRollback(rev: RevisionRecord) {
    setBusy(true)
    setError('')
    try {
      await rollbackConfigSet(setId, rev.id)
      setPendingRollback(null)
      onRolledBack()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (revisions.length === 0) {
    return (
      <p className="text-sm text-ink3">
        <Trans>尚未发布任何版本。</Trans>
      </p>
    )
  }

  return (
    <div className="space-y-4">
      <ul className="divide-y divide-line rounded border border-line">
        {revisions.map((r) => (
          <li key={r.id} className="flex items-center gap-3 px-3 py-2 text-sm">
            <span className="font-mono font-semibold">v{r.seq}</span>
            <span className="flex-1 truncate text-ink2">{r.note || r.source}</span>
            <time className="text-xs text-ink3">{new Date(r.created).toLocaleString()}</time>
            <button
              type="button"
              disabled={busy}
              onClick={() => setPendingRollback(r)}
              className="text-xs text-accent hover:underline disabled:opacity-40"
            >
              <Trans>回滚</Trans>
            </button>
          </li>
        ))}
      </ul>

      <div className="flex flex-wrap items-end gap-2">
        <label className="text-xs text-ink3">
          <Trans>对比</Trans>
          <select
            className="ml-1 rounded border border-line bg-wash px-2 py-1 text-sm"
            value={from}
            onChange={(e) => setFrom(e.target.value)}
          >
            <option value="">—</option>
            <option value="draft">draft</option>
            {revisions.map((r) => (
              <option key={r.id} value={r.id}>v{r.seq}</option>
            ))}
          </select>
        </label>
        <span className="text-ink3">→</span>
        <select
          className="rounded border border-line bg-wash px-2 py-1 text-sm"
          value={to}
          onChange={(e) => setTo(e.target.value)}
        >
          <option value="">—</option>
          <option value="draft">draft</option>
          {revisions.map((r) => (
            <option key={r.id} value={r.id}>v{r.seq}</option>
          ))}
        </select>
        <button
          type="button"
          disabled={!from || !to || busy}
          onClick={() => void runDiff()}
          className="rounded bg-wash px-3 py-1 text-sm disabled:opacity-40"
        >
          <Trans>查看 diff</Trans>
        </button>
      </div>

      {error && <p className="text-sm text-rose-600">{error}</p>}
      {(changes.length > 0 || Object.keys(diffs).length > 0) && (
        <DiffView changes={changes} diffs={diffs} />
      )}

      {pendingRollback && (
        <ConfirmDialog
          title={t`回滚`}
          message={t`回滚到 v${pendingRollback.seq} 会生成新版本 v${(revisions[0]?.seq ?? 0) + 1}，历史保留。确定？`}
          confirmLabel={t`回滚`}
          busy={busy}
          onConfirm={() => void handleRollback(pendingRollback)}
          onClose={() => setPendingRollback(null)}
        />
      )}
    </div>
  )
}
