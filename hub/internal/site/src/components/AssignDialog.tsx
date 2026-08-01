import { useState } from 'react'
import { Trans } from '@lingui/react/macro'

export interface AssignDialogProps {
  machineId: string
  configSets: { id: string; name: string }[]
  /** 选 apply 时预估会覆盖的文件数（可选） */
  applyFileCount?: number
  onSubmit: (args: { configSet: string; mode: 'apply' | 'survey' }) => void
  onClose: () => void
}

export function AssignDialog({
  configSets,
  applyFileCount,
  onSubmit,
  onClose,
}: AssignDialogProps) {
  const [setId, setSetId] = useState(configSets[0]?.id ?? '')
  const [mode, setMode] = useState<'apply' | 'survey' | ''>('')

  const canSubmit = !!setId && (mode === 'apply' || mode === 'survey')

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="dialog">
      <div className="w-full max-w-md rounded-lg border border-line bg-surface p-5 shadow-xl">
        <h2 className="mb-3 text-base font-semibold">
          <Trans>指派配置集</Trans>
        </h2>

        <label className="mb-1 block text-xs text-ink3">
          <Trans>配置集</Trans>
        </label>
        <select
          className="mb-4 w-full rounded border border-line bg-wash px-2 py-1.5 text-sm"
          value={setId}
          onChange={(e) => setSetId(e.target.value)}
        >
          {configSets.map((s) => (
            <option key={s.id} value={s.id}>{s.name}</option>
          ))}
        </select>

        <fieldset className="mb-3 space-y-2">
          <legend className="mb-1 text-xs text-ink3">
            <Trans>模式（必选）</Trans>
          </legend>
          <label className="flex items-start gap-2 text-sm">
            <input
              type="radio"
              name="mode"
              value="apply"
              checked={mode === 'apply'}
              onChange={() => setMode('apply')}
              aria-label="Apply"
            />
            <span>
              <Trans>应用配置集</Trans>
              <span className="block text-xs text-ink3">
                <Trans>将覆盖本机受管文件</Trans>
                {mode === 'apply' && applyFileCount != null && (
                  <> · <Trans>将覆盖本机 {applyFileCount} 个文件</Trans></>
                )}
              </span>
            </span>
          </label>
          <label className="flex items-start gap-2 text-sm">
            <input
              type="radio"
              name="mode"
              value="survey"
              checked={mode === 'survey'}
              onChange={() => setMode('survey')}
              aria-label="Survey"
            />
            <span>
              <Trans>仅对账（survey）</Trans>
              <span className="block text-xs text-ink3">
                <Trans>不写盘，差异进收件箱</Trans>
              </span>
            </span>
          </label>
        </fieldset>

        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded px-3 py-1.5 text-sm text-ink2 hover:bg-wash">
            <Trans>取消</Trans>
          </button>
          <button
            type="button"
            disabled={!canSubmit}
            onClick={() => {
              if (mode !== 'apply' && mode !== 'survey') return
              onSubmit({ configSet: setId, mode })
            }}
            className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40"
          >
            <Trans>确定</Trans>
          </button>
        </div>
      </div>
    </div>
  )
}
