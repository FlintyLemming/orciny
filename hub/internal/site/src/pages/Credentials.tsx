import { useEffect, useState } from 'react'
import { useStore } from '@nanostores/react'
import { Trans, useLingui } from '@lingui/react/macro'
import { Plus, RefreshCw, Trash2 } from 'lucide-react'
import {
  $credentials,
  $credentialsLoading,
  $credentialsError,
  subscribeCredentials,
  reloadCredentials,
} from '@/stores/credentials'
import { createCredential, rotateCredential, deleteCredential, ApiError } from '@/lib/api'
import { ConfirmDialog } from '@/components/ConfirmDialog'

const MIN_LEN = 8

export function Credentials() {
  const { t } = useLingui()
  const list = useStore($credentials)
  const loading = useStore($credentialsLoading)
  const error = useStore($credentialsError)

  const [showCreate, setShowCreate] = useState(false)
  const [name, setName] = useState('')
  const [value, setValue] = useState('')
  const [note, setNote] = useState('')
  const [rotateId, setRotateId] = useState<string | null>(null)
  const [rotateValue, setRotateValue] = useState('')
  const [pendingDelete, setPendingDelete] = useState<{ id: string; name: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [refsHint, setRefsHint] = useState('')

  useEffect(() => subscribeCredentials(), [])

  async function handleCreate() {
    if (value.length < MIN_LEN) {
      setErr(t`凭据值至少 ${MIN_LEN} 个字符`)
      return
    }
    setBusy(true)
    setErr('')
    try {
      await createCredential(name.trim(), value, note)
      setShowCreate(false)
      setName('')
      setValue('')
      setNote('')
      await reloadCredentials()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function handleRotate() {
    if (!rotateId) return
    if (rotateValue.length < MIN_LEN) {
      setErr(t`凭据值至少 ${MIN_LEN} 个字符`)
      return
    }
    setBusy(true)
    setErr('')
    try {
      await rotateCredential(rotateId, rotateValue)
      setRotateId(null)
      setRotateValue('')
      await reloadCredentials()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function handleDelete(id: string) {
    setBusy(true)
    setErr('')
    setRefsHint('')
    try {
      await deleteCredential(id)
      await reloadCredentials()
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      setErr(msg)
      if (e instanceof ApiError && e.status === 409) {
        setRefsHint(msg)
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">
          <Trans>凭据</Trans>
        </h1>
        <button
          type="button"
          onClick={() => setShowCreate(true)}
          className="flex items-center gap-1 rounded bg-accent px-3 py-1.5 text-sm text-white"
        >
          <Plus size={14} />
          <Trans>新建</Trans>
        </button>
      </div>

      <p className="text-sm text-ink3">
        <Trans>列表只显示末四位。值不会出现在版本历史或日志里。</Trans>
      </p>

      {(error || err) && (
        <div className="rounded border border-rose-300/50 bg-rose-500/5 p-3 text-sm text-rose-700">
          {error || err}
          {refsHint && (
            <p className="mt-1 text-xs">
              <Trans>该凭据仍被配置集引用，请先去掉引用再删。</Trans>
            </p>
          )}
        </div>
      )}

      {showCreate && (
        <div className="space-y-2 rounded border border-line bg-surface p-4">
          <input
            className="w-full rounded border border-line bg-wash px-2 py-1.5 text-sm"
            placeholder={t`名称（字母数字_-）`}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <input
            type="password"
            className="w-full rounded border border-line bg-wash px-2 py-1.5 text-sm"
            placeholder={t`值（至少 ${MIN_LEN} 字符）`}
            value={value}
            onChange={(e) => setValue(e.target.value)}
          />
          <input
            className="w-full rounded border border-line bg-wash px-2 py-1.5 text-sm"
            placeholder={t`备注（可选）`}
            value={note}
            onChange={(e) => setNote(e.target.value)}
          />
          <div className="flex gap-2">
            <button
              type="button"
              disabled={busy || !name.trim() || value.length < MIN_LEN}
              onClick={() => void handleCreate()}
              className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40"
            >
              <Trans>创建</Trans>
            </button>
            <button type="button" onClick={() => setShowCreate(false)} className="text-sm text-ink2">
              <Trans>取消</Trans>
            </button>
          </div>
        </div>
      )}

      {rotateId && (
        <div className="space-y-2 rounded border border-line bg-surface p-4">
          <p className="text-sm font-medium">
            <Trans>轮换凭据</Trans>
          </p>
          <input
            type="password"
            className="w-full rounded border border-line bg-wash px-2 py-1.5 text-sm"
            placeholder={t`新值（至少 ${MIN_LEN} 字符）`}
            value={rotateValue}
            onChange={(e) => setRotateValue(e.target.value)}
          />
          <div className="flex gap-2">
            <button
              type="button"
              disabled={busy || rotateValue.length < MIN_LEN}
              onClick={() => void handleRotate()}
              className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40"
            >
              <Trans>确认轮换</Trans>
            </button>
            <button
              type="button"
              onClick={() => {
                setRotateId(null)
                setRotateValue('')
              }}
              className="text-sm text-ink2"
            >
              <Trans>取消</Trans>
            </button>
          </div>
        </div>
      )}

      {loading && (
        <p className="text-sm text-ink3">
          <Trans>加载中…</Trans>
        </p>
      )}

      <ul className="divide-y divide-line rounded-lg border border-line bg-surface">
        {list.length === 0 && !loading && (
          <li className="px-4 py-8 text-center text-sm text-ink3">
            <Trans>还没有凭据。</Trans>
          </li>
        )}
        {list.map((c) => (
          <li key={c.id} className="flex items-center gap-3 px-4 py-3">
            <div className="flex-1">
              <div className="font-medium">{c.name}</div>
              <div className="font-mono text-xs text-ink3">…{c.last4}</div>
              {c.note && <div className="text-xs text-ink3">{c.note}</div>}
            </div>
            <button
              type="button"
              title={t`轮换`}
              disabled={busy}
              onClick={() => setRotateId(c.id)}
              className="rounded p-1.5 text-ink2 hover:bg-wash"
            >
              <RefreshCw size={14} />
            </button>
            <button
              type="button"
              title={t`删除`}
              disabled={busy}
              onClick={() => setPendingDelete({ id: c.id, name: c.name })}
              className="rounded p-1.5 text-rose-600 hover:bg-wash"
            >
              <Trash2 size={14} />
            </button>
          </li>
        ))}
      </ul>

      {pendingDelete && (
        <ConfirmDialog
          title={t`删除凭据`}
          message={t`确定删除凭据 ${pendingDelete.name}？`}
          confirmLabel={t`删除`}
          danger
          busy={busy}
          onConfirm={() => {
            const id = pendingDelete.id
            setPendingDelete(null)
            void handleDelete(id)
          }}
          onClose={() => setPendingDelete(null)}
        />
      )}
    </div>
  )
}
