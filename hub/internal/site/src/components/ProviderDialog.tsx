import { useEffect, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { ChevronDown, ChevronRight, X } from 'lucide-react'
import { ApiError, createProvider, updateProvider, type ProviderBody } from '@/lib/api'
import { emptySlots, fillAllSlots, isPassthrough } from '@/lib/binding'
import type {
  ClaudeEndpointRecord,
  ModelSlots,
  OpenAIEndpointRecord,
  PresetEndpoint,
  ProviderPreset,
  ProviderRecord,
} from '@/types/collections'

/** 一个端点分区的编辑状态。两个端点共用这一个结构，字段各取所需。 */
interface EndpointDraft {
  baseURL: string
  authField: string
  models: string[]
  /** '' = 没填（编辑态即「不修改」） */
  key: string
  /** 用户点过「清除」→ 提交 key: '' */
  cleared: boolean
  defaults: ModelSlots // 仅 claude
  defaultModel: string // 仅 openai
}

function draftOfClaude(e: ClaudeEndpointRecord | null | undefined): EndpointDraft {
  return {
    baseURL: e?.base_url ?? '',
    authField: e?.auth_field || 'ANTHROPIC_AUTH_TOKEN',
    models: e?.models ?? [],
    key: '',
    cleared: false,
    defaults: e?.defaults ?? emptySlots(),
    defaultModel: '',
  }
}

function draftOfOpenAI(e: OpenAIEndpointRecord | null | undefined): EndpointDraft {
  return {
    baseURL: e?.base_url ?? '',
    authField: e?.auth_field || 'OPENAI_API_KEY',
    models: e?.models ?? [],
    key: '',
    cleared: false,
    defaults: emptySlots(),
    defaultModel: e?.default_model ?? '',
  }
}

function draftOfPreset(e: PresetEndpoint, fallbackAuth: string): EndpointDraft {
  return {
    baseURL: e.base_url,
    authField: e.auth_field || fallbackAuth,
    models: e.models ?? [],
    key: '',
    cleared: false,
    defaults: e.defaults ?? emptySlots(),
    defaultModel: e.default_model ?? '',
  }
}

/**
 * key 的三态（M1.6 spec §5.2）：
 *   没填且没点清除 → 字段**不出现在请求体里** = 不修改
 *   点了清除       → key: '' = 清空
 *   填了           → key: 值 = 替换
 *
 * 新建态没有「不修改」可言，但同一套编码照样成立：没填就不传，
 * 后端的「配了 base_url 必须有 key」会把该拦的拦下。
 */
function keyField(d: { key: string; cleared: boolean }): { key?: string } {
  if (d.key !== '') return { key: d.key }
  if (d.cleared) return { key: '' }
  return {}
}

export function ProviderDialog({
  presets,
  editing,
  boundSetCount = 0,
  onClose,
  onSaved,
  onSubmit,
}: {
  presets: ProviderPreset[]
  /** 非空 = 编辑既有服务配置 */
  editing?: ProviderRecord
  /** 编辑时显示「会立即重注入到 N 个配置集所属的机器」 */
  boundSetCount?: number
  onClose: () => void
  onSaved: () => void
  /** 注入用。默认走 createProvider / updateProvider。 */
  onSubmit?: (body: ProviderBody) => Promise<void>
}) {
  const { t } = useLingui()

  const [preset, setPreset] = useState(editing?.preset ?? '')
  const [name, setName] = useState(editing?.name ?? '')
  const [note, setNote] = useState(editing?.note ?? '')
  const [platformKey, setPlatformKey] = useState('')
  const [platformCleared, setPlatformCleared] = useState(false)

  const [claude, setClaude] = useState<EndpointDraft>(() => draftOfClaude(editing?.claude))
  const [openai, setOpenai] = useState<EndpointDraft>(() => draftOfOpenAI(editing?.openai))
  // 选了预设但该平台没有 openai 口时，给一行说明。
  const [presetHasNoOpenAI, setPresetHasNoOpenAI] = useState(false)

  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  /** 选预设时**两个端点一起带出**（M1.6 spec §5.6）：用户只需要粘一次 key。 */
  function applyPreset(p: ProviderPreset) {
    setPreset(p.id)
    if (!name) setName(p.name)
    setClaude(draftOfPreset(p.claude, 'ANTHROPIC_AUTH_TOKEN'))
    setOpenai(draftOfPreset(p.openai, 'OPENAI_API_KEY'))
    setPresetHasNoOpenAI(p.openai.base_url === '')
  }

  /** 自定义平台 = preset 留空，两个端点都清空由用户自己填（M1.5 spec §2.3）。 */
  function applyCustom() {
    setPreset('')
    setClaude(draftOfClaude(null))
    setOpenai(draftOfOpenAI(null))
    setPresetHasNoOpenAI(false)
  }

  // 平台级 key 拿不拿得到：填了、或（编辑态且没点清除且原本就有）。
  const platformKeyAvailable =
    platformKey !== '' || (!platformCleared && Boolean(editing?.key_last4))

  /** 与后端 providers.validate 同一条规则，在按钮上先挡一次（M1.6 spec §2.3）。 */
  function endpointOK(d: EndpointDraft, existingLast4?: string): boolean {
    if (d.baseURL.trim() === '') return true // 没配的端点不要求 key
    if (d.key !== '') return true
    if (!d.cleared && Boolean(existingLast4)) return true
    return platformKeyAvailable
  }

  const canSave =
    name.trim() !== '' &&
    endpointOK(claude, editing?.claude?.key_last4) &&
    endpointOK(openai, editing?.openai?.key_last4) &&
    !busy

  async function handleSave() {
    setBusy(true)
    setError('')
    try {
      const body: ProviderBody = {
        name: name.trim(),
        preset,
        note,
        ...keyField({ key: platformKey, cleared: platformCleared }),
        claude: {
          base_url: claude.baseURL.trim(),
          auth_field: claude.authField,
          models: claude.models,
          defaults: claude.defaults,
          ...keyField(claude),
        },
        openai: {
          base_url: openai.baseURL.trim(),
          auth_field: openai.authField,
          models: openai.models,
          default_model: openai.defaultModel,
          ...keyField(openai),
        },
      }
      if (onSubmit) await onSubmit(body)
      else if (editing) await updateProvider(editing.id, body)
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

        {/* ---------- 平台信息 ---------- */}
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
              preset === ''
                ? 'border-accent bg-accent-soft text-accent'
                : 'border-line text-ink2 hover:bg-wash'
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
          aria-label={t`名称`}
          className="mb-3 w-full rounded border border-line bg-wash px-2 py-1.5 text-sm"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />

        <label className="mb-1 block text-xs text-ink3" htmlFor="provider-key">
          <Trans>API key</Trans>
        </label>
        <div className="mb-1 flex items-center gap-2">
          <input
            id="provider-key"
            aria-label="API key"
            type="password"
            className="flex-1 rounded border border-line bg-wash px-2 py-1.5 font-mono text-sm"
            placeholder={editing ? t`留空则不修改，填写即替换` : t`两个端点默认都用它`}
            value={platformKey}
            onChange={(e) => {
              setPlatformKey(e.target.value)
              if (e.target.value !== '') setPlatformCleared(false)
            }}
          />
          {editing?.key_last4 && !platformCleared && (
            <span className="text-xs text-ink3">····{editing.key_last4}</span>
          )}
        </div>
        <p className="mb-3 text-[11px] text-ink3">
          {platformCleared ? (
            <Trans>保存后清空平台级 key。</Trans>
          ) : (
            <Trans>平台级 key，两个端点默认都用它。</Trans>
          )}
          {editing?.key_last4 && !platformCleared && (
            <button
              type="button"
              className="ml-2 text-accent"
              onClick={() => setPlatformCleared(true)}
            >
              <Trans>清除</Trans>
            </button>
          )}
        </p>

        <label className="mb-1 block text-xs text-ink3" htmlFor="provider-note">
          <Trans>备注</Trans>
        </label>
        <input
          id="provider-note"
          className="mb-4 w-full rounded border border-line bg-wash px-2 py-1.5 text-sm"
          value={note}
          onChange={(e) => setNote(e.target.value)}
        />

        {/* ---------- Claude 端点 ---------- */}
        {/* key 挂 preset：选预设是「把这一侧整个换掉」，分区跟着重挂，
            折叠状态按新的 base_url 重新算（有值的展开）。 */}
        <EndpointSection
          key={`claude-${preset}`}
          id="claude"
          label="Claude"
          draft={claude}
          setDraft={setClaude}
          existingLast4={editing?.claude?.key_last4}
          editing={Boolean(editing)}
        >
          <label className="mb-1 block text-xs text-ink3" htmlFor="claude-auth-field">
            <Trans>鉴权字段</Trans>
          </label>
          <select
            id="claude-auth-field"
            aria-label="claude auth_field"
            className="mb-3 w-full rounded border border-line bg-wash px-2 py-1.5 font-mono text-sm"
            value={claude.authField}
            onChange={(e) => setClaude({ ...claude, authField: e.target.value })}
          >
            <option value="ANTHROPIC_AUTH_TOKEN">ANTHROPIC_AUTH_TOKEN</option>
            <option value="ANTHROPIC_API_KEY">ANTHROPIC_API_KEY</option>
          </select>

          <ModelList
            idPrefix="claude"
            models={claude.models}
            onChange={(models) => setClaude({ ...claude, models })}
          />

          {/* 四槽：要么全空（透传）要么全满（M1.5 spec §2.3）。
              主模型下拉一次填满四个，避免半填这种没有正确处理方式的中间态。 */}
          <label className="mb-1 block text-xs text-ink3" htmlFor="claude-main-model">
            <Trans>默认模型（四槽同填）</Trans>
          </label>
          <select
            id="claude-main-model"
            aria-label={t`claude 默认模型`}
            className="mb-3 w-full rounded border border-line bg-wash px-2 py-1.5 font-mono text-sm"
            value={isPassthrough(claude.defaults) ? '' : claude.defaults.main}
            onChange={(e) =>
              setClaude({
                ...claude,
                defaults: e.target.value === '' ? emptySlots() : fillAllSlots(e.target.value),
              })
            }
          >
            <option value="">{t`透传（不设模型变量）`}</option>
            {claude.models.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        </EndpointSection>

        {/* ---------- OpenAI 端点 ---------- */}
        <EndpointSection
          key={`openai-${preset}`}
          id="openai"
          label="OpenAI"
          draft={openai}
          setDraft={setOpenai}
          existingLast4={editing?.openai?.key_last4}
          editing={Boolean(editing)}
          notice={
            <>
              <p data-testid="openai-inert-note" className="mb-2 text-xs text-ink3">
                <Trans>
                  OpenAI 端点本期只是「记下来的配置」：可以建、可以管，但还不会注入到任何机器。
                  受管范围目前只有 .claude/**。
                </Trans>
              </p>
              {presetHasNoOpenAI && (
                <p className="mb-2 text-xs text-ink3">
                  <Trans>该平台未提供 OpenAI 端点。你仍然可以手填一个。</Trans>
                </p>
              )}
            </>
          }
        >
          <label className="mb-1 block text-xs text-ink3" htmlFor="openai-auth-field">
            <Trans>鉴权字段</Trans>
          </label>
          <input
            id="openai-auth-field"
            aria-label="openai auth_field"
            className="mb-3 w-full rounded border border-line bg-wash px-2 py-1.5 font-mono text-sm"
            value={openai.authField}
            onChange={(e) => setOpenai({ ...openai, authField: e.target.value })}
          />

          <ModelList
            idPrefix="openai"
            models={openai.models}
            onChange={(models) => setOpenai({ ...openai, models })}
          />

          <label className="mb-1 block text-xs text-ink3" htmlFor="openai-default-model">
            <Trans>默认模型</Trans>
          </label>
          <select
            id="openai-default-model"
            aria-label={t`openai 默认模型`}
            className="mb-3 w-full rounded border border-line bg-wash px-2 py-1.5 font-mono text-sm"
            value={openai.defaultModel}
            onChange={(e) => setOpenai({ ...openai, defaultModel: e.target.value })}
          >
            <option value="">{t`未指定`}</option>
            {openai.models.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        </EndpointSection>

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

/**
 * 一个端点分区：折叠头 + base_url + 端点专属字段 + 单独的 key。
 *
 * 头部右侧的「已配置 / 未配置」是**状态显示，不是开关**——清空 base_url
 * 就是取消配置（M1.6 spec §2.2）。刻意不放 checkbox / switch：两个字段表达
 * 同一件事只会产生「开着但没填」这种没有正确处理方式的中间态。
 */
function EndpointSection({
  id,
  label,
  draft,
  setDraft,
  existingLast4,
  editing,
  notice,
  children,
}: {
  id: 'claude' | 'openai'
  label: string
  draft: EndpointDraft
  setDraft: (d: EndpointDraft) => void
  existingLast4?: string
  editing: boolean
  notice?: React.ReactNode
  children: React.ReactNode
}) {
  const { t } = useLingui()
  // 默认折叠，base_url 非空的展开。
  const [open, setOpen] = useState(() => draft.baseURL !== '')
  const configured = draft.baseURL.trim() !== ''

  return (
    <div className="mb-4 rounded border border-line">
      <button
        type="button"
        className="flex w-full items-center gap-2 px-3 py-2 text-sm"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
      >
        {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        <span className="flex-1 text-left font-medium">
          <Trans>{label} 端点</Trans>
        </span>
        <span data-testid={`${id}-status`} className="text-xs text-ink3">
          {configured ? <Trans>已配置</Trans> : <Trans>未配置</Trans>}
        </span>
      </button>

      {open && (
        <div className="border-t border-line px-3 py-3">
          {notice}

          <label className="mb-1 block text-xs text-ink3" htmlFor={`${id}-base-url`}>
            base_url
          </label>
          <input
            id={`${id}-base-url`}
            aria-label={`${id} base_url`}
            className="mb-3 w-full rounded border border-line bg-wash px-2 py-1.5 font-mono text-sm"
            value={draft.baseURL}
            onChange={(e) => setDraft({ ...draft, baseURL: e.target.value })}
          />

          {children}

          <label className="mb-1 block text-xs text-ink3" htmlFor={`${id}-key`}>
            <Trans>单独的 key</Trans>
          </label>
          <div className="flex items-center gap-2">
            <input
              id={`${id}-key`}
              aria-label={`${id} 单独的 key`}
              type="password"
              className="flex-1 rounded border border-line bg-wash px-2 py-1.5 font-mono text-sm"
              placeholder={editing ? t`留空则不修改` : t`留空则用平台级`}
              value={draft.key}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  key: e.target.value,
                  cleared: e.target.value === '' ? draft.cleared : false,
                })
              }
            />
            {existingLast4 && !draft.cleared && (
              <span className="text-xs text-ink3">····{existingLast4}</span>
            )}
          </div>
          <p className="mt-1 text-[11px] text-ink3">
            {draft.cleared ? (
              <Trans>保存后回落平台级。</Trans>
            ) : (
              <Trans>留空则用平台级。</Trans>
            )}
            {existingLast4 && !draft.cleared && (
              <button
                type="button"
                className="ml-2 text-accent"
                onClick={() => setDraft({ ...draft, key: '', cleared: true })}
              >
                <Trans>清除</Trans>
              </button>
            )}
          </p>
        </div>
      )}
    </div>
  )
}

/** 模型清单的 tag 输入。两个端点各一份，模型 id 常常不同（M1.6 spec §1.3）。 */
function ModelList({
  idPrefix,
  models,
  onChange,
}: {
  idPrefix: string
  models: string[]
  onChange: (models: string[]) => void
}) {
  const { t } = useLingui()
  const [draft, setDraft] = useState('')

  return (
    <>
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
              onClick={() => onChange(models.filter((x) => x !== m))}
            >
              <X size={10} />
            </button>
          </span>
        ))}
      </div>
      <div className="mb-3">
        <input
          aria-label={`${idPrefix} 添加模型`}
          placeholder={t`模型 id`}
          className="w-full rounded border border-line bg-wash px-2 py-1.5 font-mono text-sm"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key !== 'Enter') return
            e.preventDefault()
            const v = draft.trim()
            if (v && !models.includes(v)) onChange([...models, v])
            setDraft('')
          }}
        />
      </div>
    </>
  )
}
