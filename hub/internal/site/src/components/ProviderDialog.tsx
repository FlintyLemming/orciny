import { useEffect, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { X } from 'lucide-react'
import {
  ApiError,
  createCredential,
  createProvider,
  updateProvider,
  type ProviderBody,
} from '@/lib/api'
import { emptySlots } from '@/lib/binding'
import type {
  AuthField,
  CredentialRecord,
  ModelSlots,
  ProviderPreset,
  ProviderRecord,
} from '@/types/collections'

const MIN_CRED_LEN = 8

export function ProviderDialog({
  presets,
  credentials,
  editing,
  boundSetCount = 0,
  onClose,
  onSaved,
}: {
  presets: ProviderPreset[]
  credentials: CredentialRecord[]
  /** 非空 = 编辑既有服务配置 */
  editing?: ProviderRecord
  /** 编辑时显示「会立即重注入到 N 个配置集所属的机器」 */
  boundSetCount?: number
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useLingui()

  const [preset, setPreset] = useState(editing?.preset ?? '')
  const [name, setName] = useState(editing?.name ?? '')
  const [baseURL, setBaseURL] = useState(editing?.base_url ?? '')
  const [authField, setAuthField] = useState<AuthField>(
    editing?.auth_field ?? 'ANTHROPIC_AUTH_TOKEN',
  )
  const [models, setModels] = useState<string[]>(editing?.models ?? [])
  const [defaults, setDefaults] = useState<ModelSlots>(editing?.defaults ?? emptySlots())
  const [note, setNote] = useState(editing?.note ?? '')

  // 凭据：选已有 或 现场新建。
  const [credential, setCredential] = useState(editing?.credential ?? '')
  const [newCred, setNewCred] = useState(false)
  const [newCredName, setNewCredName] = useState('')
  const [newCredValue, setNewCredValue] = useState('')

  const [modelDraft, setModelDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  function applyPreset(p: ProviderPreset) {
    setPreset(p.id)
    setBaseURL(p.base_url)
    setAuthField(p.auth_field)
    setModels(p.models)
    setDefaults(p.defaults)
    if (!name) setName(p.name)
  }

  /** 自定义平台 = preset 留空，其余字段清空由用户自己填（M1.5 spec §2.3）。 */
  function applyCustom() {
    setPreset('')
    setBaseURL('')
    setAuthField('ANTHROPIC_AUTH_TOKEN')
    setModels([])
    setDefaults(emptySlots())
  }

  const credentialReady = newCred
    ? newCredName.trim() !== '' && newCredValue.length >= MIN_CRED_LEN
    : credential !== ''
  const canSave = name.trim() !== '' && baseURL.trim() !== '' && credentialReady && !busy

  async function handleSave() {
    setBusy(true)
    setError('')
    try {
      let credID = credential
      if (newCred) {
        await createCredential(newCredName.trim(), newCredValue, t`由 AI 服务配置创建`)
        // createCredential 不回 id，按名字取回——凭据名有唯一索引。
        const { pb } = await import('@/lib/pb')
        const rec = await pb
          .collection('credentials')
          .getFirstListItem(pb.filter('name = {:n}', { n: newCredName.trim() }))
        credID = rec.id
      }
      const body: ProviderBody = {
        name: name.trim(),
        preset,
        base_url: baseURL.trim(),
        auth_field: authField,
        credential: credID,
        models,
        defaults,
        note,
      }
      if (editing) await updateProvider(editing.id, body)
      else await createProvider(body)
      onSaved()
    } catch (e) {
      setError(e instanceof ApiError || e instanceof Error ? e.message : String(e))
      setBusy(false)
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      role="dialog"
    >
      <div
        className="max-h-[90vh] w-full max-w-2xl overflow-y-auto rounded-lg border border-line bg-surface p-5 shadow-xl"
        aria-modal="true"
        aria-label={editing ? t`编辑 AI 服务` : t`新建 AI 服务`}
      >
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-base font-semibold">
            {editing ? <Trans>编辑 AI 服务</Trans> : <Trans>新建 AI 服务</Trans>}
          </h2>
          <button type="button" onClick={onClose} aria-label={t`关闭`}>
            <X size={16} />
          </button>
        </div>

        {/* 编辑态必须让「不产生新版本」这件事对用户可见（M1.5 spec §8.1）。 */}
        {editing && (
          <p
            data-testid="reinject-hint"
            className="mb-4 rounded border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-ink2"
          >
            <Trans>
              保存后会立即重注入到绑定了这个服务配置的 {boundSetCount} 个配置集所属的机器，不产生新版本。
            </Trans>
          </p>
        )}

        {/* 1. 平台网格 */}
        <div className="mb-1 text-xs text-ink3">
          <Trans>平台</Trans>
        </div>
        <div className="mb-4 flex flex-wrap gap-2">
          {presets.map((p) => (
            <button
              key={p.id}
              type="button"
              onClick={() => applyPreset(p)}
              className={`rounded border px-3 py-1.5 text-sm ${
                preset === p.id
                  ? 'border-accent bg-accent-soft text-accent'
                  : 'border-line text-ink2 hover:bg-wash'
              }`}
            >
              {p.name}
            </button>
          ))}
          <button
            type="button"
            onClick={applyCustom}
            className={`rounded border px-3 py-1.5 text-sm ${
              preset === '' ? 'border-accent bg-accent-soft text-accent' : 'border-line text-ink2 hover:bg-wash'
            }`}
          >
            <Trans>自定义</Trans>
          </button>
        </div>

        <label className="mb-1 block text-xs text-ink3" htmlFor="provider-name">
          <Trans>名称</Trans>
        </label>
        <input
          id="provider-name"
          className="mb-3 w-full rounded border border-line bg-wash px-2 py-1.5 text-sm"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />

        {/* 2. base_url */}
        <label className="mb-1 block text-xs text-ink3" htmlFor="provider-base-url">
          base_url
        </label>
        <input
          id="provider-base-url"
          aria-label="base_url"
          className="mb-3 w-full rounded border border-line bg-wash px-2 py-1.5 font-mono text-sm"
          value={baseURL}
          onChange={(e) => setBaseURL(e.target.value)}
        />

        {/* 3. 鉴权字段 */}
        <label className="mb-1 block text-xs text-ink3" htmlFor="provider-auth-field">
          <Trans>鉴权字段</Trans>
        </label>
        <select
          id="provider-auth-field"
          aria-label="auth_field"
          className="mb-3 w-full rounded border border-line bg-wash px-2 py-1.5 font-mono text-sm"
          value={authField}
          onChange={(e) => setAuthField(e.target.value as AuthField)}
        >
          <option value="ANTHROPIC_AUTH_TOKEN">ANTHROPIC_AUTH_TOKEN</option>
          <option value="ANTHROPIC_API_KEY">ANTHROPIC_API_KEY</option>
        </select>

        {/* 4. 凭据 */}
        <div className="mb-1 flex items-center justify-between">
          <span className="text-xs text-ink3">
            <Trans>凭据</Trans>
          </span>
          <button
            type="button"
            className="text-xs text-accent"
            onClick={() => setNewCred((v) => !v)}
          >
            {newCred ? <Trans>选已有凭据</Trans> : <Trans>新建凭据</Trans>}
          </button>
        </div>
        {newCred ? (
          <div className="mb-3 space-y-2">
            <input
              aria-label={t`凭据名`}
              placeholder={t`凭据名`}
              className="w-full rounded border border-line bg-wash px-2 py-1.5 text-sm"
              value={newCredName}
              onChange={(e) => setNewCredName(e.target.value)}
            />
            <input
              aria-label={t`凭据值`}
              placeholder={t`凭据值`}
              type="password"
              className="w-full rounded border border-line bg-wash px-2 py-1.5 font-mono text-sm"
              value={newCredValue}
              onChange={(e) => setNewCredValue(e.target.value)}
            />
          </div>
        ) : (
          <select
            aria-label={t`凭据`}
            className="mb-3 w-full rounded border border-line bg-wash px-2 py-1.5 text-sm"
            value={credential}
            onChange={(e) => setCredential(e.target.value)}
          >
            <option value="">{t`请选择`}</option>
            {credentials.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name} ····{c.last4}
              </option>
            ))}
          </select>
        )}

        {/* 5. 模型清单 */}
        <div className="mb-1 text-xs text-ink3">
          <Trans>模型清单</Trans>
        </div>
        <div className="mb-2 flex flex-wrap gap-1.5">
          {models.map((m) => (
            <span
              key={m}
              className="flex items-center gap-1 rounded bg-wash px-2 py-0.5 font-mono text-xs"
            >
              {m}
              <button
                type="button"
                aria-label={t`移除 ${m}`}
                onClick={() => setModels(models.filter((x) => x !== m))}
              >
                <X size={10} />
              </button>
            </span>
          ))}
        </div>
        <div className="mb-4 flex gap-2">
          <input
            aria-label={t`添加模型`}
            placeholder={t`模型 id`}
            className="flex-1 rounded border border-line bg-wash px-2 py-1.5 font-mono text-sm"
            value={modelDraft}
            onChange={(e) => setModelDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key !== 'Enter') return
              e.preventDefault()
              const v = modelDraft.trim()
              if (v && !models.includes(v)) setModels([...models, v])
              setModelDraft('')
            }}
          />
        </div>

        <label className="mb-1 block text-xs text-ink3" htmlFor="provider-note">
          <Trans>备注</Trans>
        </label>
        <input
          id="provider-note"
          className="mb-4 w-full rounded border border-line bg-wash px-2 py-1.5 text-sm"
          value={note}
          onChange={(e) => setNote(e.target.value)}
        />

        {error && <p className="mb-3 text-sm text-red-500">{error}</p>}

        <div className="flex justify-end gap-2">
          <button
            type="button"
            className="rounded border border-line px-3 py-1.5 text-sm text-ink2"
            onClick={onClose}
          >
            <Trans>取消</Trans>
          </button>
          <button
            type="button"
            className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40"
            disabled={!canSave}
            onClick={() => void handleSave()}
          >
            <Trans>保存</Trans>
          </button>
        </div>
      </div>
    </div>
  )
}
