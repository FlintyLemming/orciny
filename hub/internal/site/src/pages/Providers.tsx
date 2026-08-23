import { useEffect, useState } from 'react'
import { useStore } from '@nanostores/react'
import { Trans, useLingui } from '@lingui/react/macro'
import { Pencil, Plus, Trash2 } from 'lucide-react'
import {
  $providers,
  $providersError,
  $providersLoading,
  reloadProviders,
  subscribeProviders,
} from '@/stores/providers'
import { $configSets, subscribeConfigSets } from '@/stores/configsets'
import { ApiError, deleteProvider, listProviderPresets } from '@/lib/api'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ProviderDialog } from '@/components/ProviderDialog'
import type { ProviderPreset, ProviderRecord } from '@/types/collections'

export function Providers() {
  const { t } = useLingui()
  const list = useStore($providers)
  const loading = useStore($providersLoading)
  const error = useStore($providersError)
  const configSets = useStore($configSets)

  const [presets, setPresets] = useState<ProviderPreset[]>([])
  const [showDialog, setShowDialog] = useState(false)
  const [editing, setEditing] = useState<ProviderRecord | undefined>()
  const [pendingDelete, setPendingDelete] = useState<ProviderRecord | null>(null)
  const [err, setErr] = useState('')

  useEffect(() => subscribeProviders(), [])
  useEffect(() => subscribeConfigSets(), [])
  useEffect(() => {
    void listProviderPresets()
      .then(setPresets)
      .catch(() => setPresets([]))
  }, [])

  /**
   * 「被 N 个配置集引用」直接从已订阅的配置集列表里算，不另开端点——
   * head_provider 与 draft_binding 都算，与后端 BoundBy 的口径一致。
   */
  function boundCount(id: string) {
    return configSets.filter(
      (s) => s.head_provider === id || s.draft_binding?.provider === id,
    ).length
  }

  async function handleDelete() {
    if (!pendingDelete) return
    setErr('')
    try {
      await deleteProvider(pendingDelete.id)
      setPendingDelete(null)
      await reloadProviders()
    } catch (e) {
      setErr(e instanceof ApiError || e instanceof Error ? e.message : String(e))
      setPendingDelete(null)
    }
  }

  return (
    <div className="p-6">
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-lg font-semibold">
          <Trans>AI 服务</Trans>
        </h1>
        <button
          type="button"
          className="flex items-center gap-1 rounded bg-accent px-3 py-1.5 text-sm text-white"
          onClick={() => {
            setEditing(undefined)
            setShowDialog(true)
          }}
        >
          <Plus size={14} />
          <Trans>新建</Trans>
        </button>
      </div>

      <p className="mb-4 text-sm text-ink2">
        <Trans>
          服务配置决定「用哪家的哪个模型」。配置集绑定它之后，settings.json 里只落占位符，
          真实值在 agent 落盘时注入。
        </Trans>
      </p>

      {(error || err) && <p className="mb-3 text-sm text-red-500">{error || err}</p>}
      {loading && (
        <p className="text-sm text-ink3">
          <Trans>加载中…</Trans>
        </p>
      )}

      {!loading && list.length === 0 && (
        <p className="text-sm text-ink3">
          <Trans>还没有任何 AI 服务配置。</Trans>
        </p>
      )}

      <ul className="space-y-2">
        {list.map((p) => (
          <ProviderRow
            key={p.id}
            provider={p}
            boundCount={boundCount(p.id)}
            onEdit={() => {
              setEditing(p)
              setShowDialog(true)
            }}
            onDelete={() => setPendingDelete(p)}
          />
        ))}
      </ul>

      {showDialog && (
        <ProviderDialog
          presets={presets}
          editing={editing}
          boundSetCount={editing ? boundCount(editing.id) : 0}
          onClose={() => setShowDialog(false)}
          onSaved={() => {
            setShowDialog(false)
            void reloadProviders()
          }}
        />
      )}

      {pendingDelete && (
        <ConfirmDialog
          title={t`删除 AI 服务配置`}
          message={t`确定删除「${pendingDelete.name}」吗？被配置集绑定时无法删除。`}
          confirmLabel={t`删除`}
          danger
          onConfirm={() => void handleDelete()}
          onClose={() => setPendingDelete(null)}
        />
      )}
    </div>
  )
}

/** 一条 = 一家平台，两个端点各一行（M1.6 spec §5.1）。 */
export function ProviderRow({
  provider: p,
  boundCount,
  onEdit,
  onDelete,
}: {
  provider: ProviderRecord
  boundCount: number
  onEdit: () => void
  onDelete: () => void
}) {
  const { t } = useLingui()
  return (
    <li className="flex items-start gap-3 rounded border border-line bg-surface px-4 py-3">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium">{p.name}</span>
          {p.preset && (
            <span className="rounded bg-wash px-1.5 py-0.5 text-[10px] text-ink3">
              {p.preset}
            </span>
          )}
        </div>

        <EndpointLine
          testid="endpoint-claude"
          label="Claude"
          endpoint={p.claude}
          detail={
            p.claude && (
              <>
                <span className="font-mono">{p.claude.auth_field}</span>
                {p.claude.key_last4 && <span> · ····{p.claude.key_last4}</span>}
                <span> · {t`${p.claude.models?.length ?? 0} 个模型`}</span>
              </>
            )
          }
        />
        <EndpointLine
          testid="endpoint-openai"
          label="OpenAI"
          endpoint={p.openai}
          detail={
            p.openai && (
              <>
                <span className="font-mono">{p.openai.auth_field}</span>
                {p.openai.key_last4 && <span> · ····{p.openai.key_last4}</span>}
                <span> · {p.openai.default_model || t`未指定默认模型`}</span>
              </>
            )
          }
        />

        <div className="mt-1 text-[11px] text-ink3">
          <Trans>被 {boundCount} 个配置集引用</Trans>
        </div>
      </div>
      <button
        type="button"
        aria-label={t`编辑 ${p.name}`}
        className="text-ink3 hover:text-ink"
        onClick={onEdit}
      >
        <Pencil size={14} />
      </button>
      <button
        type="button"
        aria-label={t`删除 ${p.name}`}
        className="text-ink3 hover:text-red-500"
        onClick={onDelete}
      >
        <Trash2 size={14} />
      </button>
    </li>
  )
}

/**
 * 未配置的端点显示为置灰的「未配置」行，而不是整行不显示——
 * 用户要能一眼看出「这条还有另一个口没填」，那正是本期新增的可操作项
 * （M1.6 spec §5.1）。
 */
function EndpointLine({
  testid,
  label,
  endpoint,
  detail,
}: {
  testid: string
  label: string
  endpoint: { base_url: string } | null
  detail: React.ReactNode
}) {
  const configured = Boolean(endpoint?.base_url)
  return (
    <div data-testid={testid} className="mt-1 flex gap-2 text-xs">
      <span className="w-14 shrink-0 text-ink3">{label}</span>
      {configured ? (
        <div className="min-w-0">
          <div className="truncate font-mono text-ink3">{endpoint!.base_url}</div>
          <div className="text-[11px] text-ink3">{detail}</div>
        </div>
      ) : (
        <span className="text-ink3/50">
          <Trans>未配置</Trans>
        </span>
      )}
    </div>
  )
}
