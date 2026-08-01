import { useEffect, useState } from 'react'
import { useStore } from '@nanostores/react'
import { Trans, useLingui } from '@lingui/react/macro'
import { $variables, subscribeVariables } from '@/stores/credentials'
import { setMachineVariables } from '@/lib/api'

export function VariablesEditor({ machineId }: { machineId: string }) {
  const { t } = useLingui()
  const vars = useStore($variables)
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [newKey, setNewKey] = useState('')
  const [newVal, setNewVal] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [dirty, setDirty] = useState(false)

  useEffect(() => subscribeVariables(machineId), [machineId])

  useEffect(() => {
    const m: Record<string, string> = {}
    for (const v of vars) m[v.key] = v.value
    setDraft(m)
    setDirty(false)
  }, [vars])

  async function handleSave() {
    setBusy(true)
    setError('')
    try {
      await setMachineVariables(machineId, draft)
      setDirty(false)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  function addRow() {
    if (!newKey.trim()) return
    setDraft((d) => ({ ...d, [newKey.trim()]: newVal }))
    setNewKey('')
    setNewVal('')
    setDirty(true)
  }

  return (
    <div className="space-y-2">
      {error && <p className="text-xs text-rose-600">{error}</p>}
      <ul className="space-y-1">
        {Object.entries(draft).map(([k, v]) => (
          <li key={k} className="flex gap-1">
            <span className="w-28 shrink-0 truncate font-mono text-xs text-ink2">{k}</span>
            <input
              className="min-w-0 flex-1 rounded border border-line bg-wash px-1.5 py-0.5 font-mono text-xs"
              value={v}
              onChange={(e) => {
                setDraft((d) => ({ ...d, [k]: e.target.value }))
                setDirty(true)
              }}
            />
            <button
              type="button"
              className="text-xs text-rose-600"
              onClick={() => {
                setDraft((d) => {
                  const n = { ...d }
                  delete n[k]
                  return n
                })
                setDirty(true)
              }}
            >
              ×
            </button>
          </li>
        ))}
      </ul>
      <div className="flex gap-1">
        <input
          className="w-28 rounded border border-line bg-wash px-1.5 py-0.5 font-mono text-xs"
          placeholder={t`键`}
          value={newKey}
          onChange={(e) => setNewKey(e.target.value)}
        />
        <input
          className="min-w-0 flex-1 rounded border border-line bg-wash px-1.5 py-0.5 font-mono text-xs"
          placeholder={t`值`}
          value={newVal}
          onChange={(e) => setNewVal(e.target.value)}
        />
        <button type="button" onClick={addRow} className="text-xs text-accent">
          <Trans>添加</Trans>
        </button>
      </div>
      <button
        type="button"
        disabled={!dirty || busy}
        onClick={() => void handleSave()}
        className="rounded bg-wash px-2 py-1 text-xs disabled:opacity-40"
      >
        <Trans>保存变量</Trans>
      </button>
    </div>
  )
}
