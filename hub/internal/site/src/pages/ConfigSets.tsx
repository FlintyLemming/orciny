import { useEffect, useState } from 'react'
import { useStore } from '@nanostores/react'
import { Trans, useLingui } from '@lingui/react/macro'
import { Plus, Copy, Trash2 } from 'lucide-react'
import {
  $configSets,
  $configSetsLoading,
  $configSetsError,
  subscribeConfigSets,
  setConfigSetPaused,
} from '@/stores/configsets'
import { createConfigSet, cloneConfigSet, deleteConfigSet } from '@/lib/api'
import { navigate } from '@/router'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { PromptDialog } from '@/components/PromptDialog'

export function ConfigSets() {
  const { t } = useLingui()
  const sets = useStore($configSets)
  const loading = useStore($configSetsLoading)
  const error = useStore($configSetsError)
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [cloneTarget, setCloneTarget] = useState<{ id: string; name: string } | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)

  useEffect(() => subscribeConfigSets(), [])

  async function handleCreate() {
    if (!name.trim()) return
    setBusy(true)
    setErr('')
    try {
      const res = await createConfigSet(name.trim())
      setCreating(false)
      setName('')
      navigate('configsets', res.id)
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function handleClone(id: string, n: string) {
    setBusy(true)
    setErr('')
    try {
      const res = await cloneConfigSet(id, n)
      setCloneTarget(null)
      navigate('configsets', res.id)
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function handleDelete(id: string) {
    setBusy(true)
    setErr('')
    try {
      await deleteConfigSet(id)
      setDeleteTarget(null)
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">
          <Trans>配置集</Trans>
        </h1>
        <button
          type="button"
          onClick={() => setCreating(true)}
          className="flex items-center gap-1 rounded bg-accent px-3 py-1.5 text-sm text-white"
        >
          <Plus size={14} />
          <Trans>新建</Trans>
        </button>
      </div>

      {(error || err) && <p className="text-sm text-rose-600">{error || err}</p>}
      {loading && (
        <p className="text-sm text-ink3">
          <Trans>加载中…</Trans>
        </p>
      )}

      {creating && (
        <div className="flex items-center gap-2 rounded border border-line bg-surface p-3">
          <input
            autoFocus
            className="flex-1 rounded border border-line bg-wash px-2 py-1.5 text-sm"
            placeholder={t`配置集名称`}
            value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && void handleCreate()}
          />
          <button
            type="button"
            disabled={busy || !name.trim()}
            onClick={() => void handleCreate()}
            className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40"
          >
            <Trans>创建</Trans>
          </button>
          <button type="button" onClick={() => setCreating(false)} className="text-sm text-ink2">
            <Trans>取消</Trans>
          </button>
        </div>
      )}

      <ul className="divide-y divide-line rounded-lg border border-line bg-surface">
        {sets.length === 0 && !loading && (
          <li className="px-4 py-8 text-center text-sm text-ink3">
            <Trans>还没有配置集。从一台机器导入，或点右上角新建。</Trans>
          </li>
        )}
        {sets.map((s) => (
          <li key={s.id} className="flex items-center gap-3 px-4 py-3">
            <button
              type="button"
              onClick={() => navigate('configsets', s.id)}
              className="flex-1 text-left"
            >
              <div className="font-medium">{s.name}</div>
              <div className="text-xs text-ink3">
                {s.expand?.head ? (
                  <Trans>v{s.expand.head.seq} · {s.note || '—'}</Trans>
                ) : (
                  <Trans>未发布 · {s.note || '—'}</Trans>
                )}
              </div>
            </button>
            <label className="flex items-center gap-1.5 text-xs text-ink2">
              <input
                type="checkbox"
                checked={s.paused}
                disabled={busy}
                onChange={(e) => void setConfigSetPaused(s.id, e.target.checked)}
              />
              <Trans>暂停下发</Trans>
            </label>
            <button
              type="button"
              title={t`克隆`}
              disabled={busy}
              onClick={() => setCloneTarget({ id: s.id, name: s.name })}
              className="rounded p-1.5 text-ink2 hover:bg-wash"
            >
              <Copy size={14} />
            </button>
            <button
              type="button"
              title={t`删除`}
              disabled={busy}
              onClick={() => setDeleteTarget(s.id)}
              className="rounded p-1.5 text-rose-600 hover:bg-wash"
            >
              <Trash2 size={14} />
            </button>
          </li>
        ))}
      </ul>

      {cloneTarget && (
        <PromptDialog
          title={t`克隆配置集`}
          label={t`新配置集名称`}
          defaultValue={`${cloneTarget.name}-copy`}
          confirmLabel={t`克隆`}
          busy={busy}
          onSubmit={(n) => void handleClone(cloneTarget.id, n)}
          onClose={() => setCloneTarget(null)}
        />
      )}

      {deleteTarget && (
        <ConfirmDialog
          title={t`删除配置集`}
          message={t`该配置集的全部版本历史将被删除，且无法恢复；已指派的机器会失去指派，但磁盘上的文件不会被动。确定删除？`}
          confirmLabel={t`删除`}
          danger
          busy={busy}
          onConfirm={() => void handleDelete(deleteTarget)}
          onClose={() => setDeleteTarget(null)}
        />
      )}
    </div>
  )
}
