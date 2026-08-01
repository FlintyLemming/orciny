import { useEffect, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import type { ValidateProblem } from '@/types/collections'
import { canPublish, type DraftState } from '@/lib/draftState'
import { validateConfigSet, publishConfigSet } from '@/lib/api'

export function PublishDialog({
  setId,
  draftState,
  affectedMachines,
  onClose,
  onPublished,
}: {
  setId: string
  draftState: DraftState
  affectedMachines: number
  onClose: () => void
  onPublished: () => void
}) {
  const { t } = useLingui()
  const [note, setNote] = useState('')
  const [problems, setProblems] = useState<ValidateProblem[]>([])
  const [loading, setLoading] = useState(true)
  const [publishing, setPublishing] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    void validateConfigSet(setId)
      .then((p) => {
        if (!cancelled) setProblems(p ?? [])
      })
      .catch((e: Error) => {
        if (!cancelled) setError(e.message)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [setId])

  const ok = canPublish(draftState, problems.length) && !publishing && !loading

  async function handlePublish() {
    setPublishing(true)
    setError('')
    try {
      await publishConfigSet(setId, note)
      onPublished()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setPublishing(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="dialog">
      <div className="w-full max-w-lg rounded-lg border border-line bg-surface p-5 shadow-xl">
        <h2 className="mb-3 text-base font-semibold">
          <Trans>发布配置集</Trans>
        </h2>
        <p className="mb-3 text-sm text-ink2">
          <Trans>将影响 {affectedMachines} 台机器</Trans>
        </p>
        <label className="mb-1 block text-xs text-ink3">
          <Trans>版本说明</Trans>
        </label>
        <input
          className="mb-3 w-full rounded border border-line bg-wash px-2 py-1.5 text-sm"
          value={note}
          onChange={(e) => setNote(e.target.value)}
          placeholder={t`例如：同步 CLAUDE.md 更新`}
        />

        {loading && (
          <p className="mb-3 text-sm text-ink3">
            <Trans>正在校验…</Trans>
          </p>
        )}
        {!loading && problems.length > 0 && (
          <div className="mb-3 max-h-40 overflow-auto rounded border border-rose-300/50 bg-rose-500/5 p-2">
            <p className="mb-1 text-xs font-semibold text-rose-600">
              <Trans>校验未通过，无法发布</Trans>
            </p>
            <ul className="space-y-1 text-xs text-ink2">
              {problems.map((p, i) => (
                <li key={i}>
                  <span className="font-mono">{p.path}</span> · {p.kind}: {p.detail}
                </li>
              ))}
            </ul>
          </div>
        )}
        {error && <p className="mb-3 text-sm text-rose-600">{error}</p>}

        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded px-3 py-1.5 text-sm text-ink2 hover:bg-wash">
            <Trans>取消</Trans>
          </button>
          <button
            type="button"
            disabled={!ok}
            onClick={() => void handlePublish()}
            className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40"
          >
            {publishing ? <Trans>发布中…</Trans> : <Trans>发布</Trans>}
          </button>
        </div>
      </div>
    </div>
  )
}
