import { useEffect, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { ChevronDown, ChevronRight, X } from 'lucide-react'
import {
  ApiError,
  createProvider,
  probeEndpoint,
  updateProvider,
  type ProbeInput,
  type ProbeResult,
  type ProviderBody,
} from '@/lib/api'
import {
  emptySlots,
  fillAllSlots,
  hasOneM,
  isPassthrough,
  setOneM,
  setSlotsOneM,
  stripOneM,
} from '@/lib/binding'
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
  onProbe,
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
  /** 注入用。默认走 probeEndpoint。 */
  onProbe?: (input: ProbeInput) => Promise<ProbeResult>
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

  // 1M 是槽位上的声明而非独立模型：下拉与选项都用基名，标记单独一个复选框。
  const claudeMainBase = isPassthrough(claude.defaults) ? '' : stripOneM(claude.defaults.main)
  const claudeOneM = hasOneM(claude.defaults.main)
  const claudeModelOptions = [...new Set(claude.models.map(stripOneM))]

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
          probeKey={claude.key || platformKey}
          onProbe={onProbe ?? probeEndpoint}
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
              主模型下拉一次填满四个，避免半填这种没有正确处理方式的中间态。

              [1m] 不进下拉：它是槽位上的上下文声明，不是一个独立模型。混在
              选项里会让同一个模型并排出现两次，用户看不出区别。 */}
          <label className="mb-1 block text-xs text-ink3" htmlFor="claude-main-model">
            <Trans>默认模型（四槽同填）</Trans>
          </label>
          <select
            id="claude-main-model"
            aria-label={t`claude 默认模型`}
            className="mb-2 w-full rounded border border-line bg-wash px-2 py-1.5 font-mono text-sm"
            value={claudeMainBase}
            onChange={(e) =>
              setClaude({
                ...claude,
                defaults:
                  e.target.value === ''
                    ? emptySlots()
                    : fillAllSlots(setOneM(e.target.value, claudeOneM)),
              })
            }
          >
            <option value="">{t`透传（不设模型变量）`}</option>
            {claudeModelOptions.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>

          <label className="mb-1 flex items-center gap-2 text-xs text-ink3">
            <input
              type="checkbox"
              aria-label={t`claude 声明 1M 上下文`}
              checked={claudeOneM}
              disabled={claudeMainBase === ''}
              onChange={(e) =>
                setClaude({
                  ...claude,
                  defaults: setSlotsOneM(claude.defaults, claudeMainBase, e.target.checked),
                })
              }
            />
            <Trans>声明 1M 上下文</Trans>
          </label>
          <p className="mb-3 text-xs text-ink3">
            {/* 用 t 而不是多行 <Trans>：JSX 会把换行折成空格，中文里那是个错字。 */}
            {t`给模型名追加 [1m]：只告诉 Claude Code 按 100 万上下文计算 auto-compact 阈值与 /context 占用，不改变上游实际能力。上游窗口不足 1M 时误勾会导致压缩不触发、请求直接报错。只影响基名相同的槽，单独指到别的模型的槽不受波及。`}
          </p>
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
          probeKey={openai.key || platformKey}
          onProbe={onProbe ?? probeEndpoint}
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
  probeKey,
  onProbe,
  notice,
  children,
}: {
  id: 'claude' | 'openai'
  label: string
  draft: EndpointDraft
  setDraft: (d: EndpointDraft) => void
  existingLast4?: string
  editing: boolean
  probeKey: string
  onProbe: (input: ProbeInput) => Promise<ProbeResult>
  notice?: React.ReactNode
  children: React.ReactNode
}) {
  const { t } = useLingui()
  // 默认折叠，base_url 非空的展开。
  const [open, setOpen] = useState(() => draft.baseURL !== '')
  const configured = draft.baseURL.trim() !== ''

  const [probe, setProbe] = useState<ProbeResult | null>(null)
  const [probing, setProbing] = useState(false)
  const [probeErr, setProbeErr] = useState('')

  async function runProbe() {
    setProbing(true)
    setProbeErr('')
    setProbe(null)
    try {
      setProbe(
        await onProbe({ endpoint: id, base_url: draft.baseURL.trim(), key: probeKey }),
      )
    } catch (e) {
      setProbeErr(e instanceof ApiError ? e.message : String(e))
    } finally {
      setProbing(false)
    }
  }

  /** 采用是显式动作：探测只给结论，改不改由用户决定。 */
  function adopt() {
    if (!probe?.base_url) return
    setDraft({
      ...draft,
      baseURL: probe.base_url,
      models: probe.models?.length ? probe.models : draft.models,
    })
    setProbe(null)
  }

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
          <div className="mb-2 flex items-center gap-2">
            <input
              id={`${id}-base-url`}
              aria-label={`${id} base_url`}
              className="flex-1 rounded border border-line bg-wash px-2 py-1.5 font-mono text-sm"
              value={draft.baseURL}
              onChange={(e) => setDraft({ ...draft, baseURL: e.target.value })}
            />
            <button
              type="button"
              aria-label={`${id} 探测`}
              className="shrink-0 rounded border border-line px-2 py-1.5 text-xs disabled:opacity-40"
              disabled={!configured || probing}
              onClick={() => void runProbe()}
            >
              {probing ? <Trans>探测中…</Trans> : <Trans>探测</Trans>}
            </button>
          </div>

          {probeErr && <p className="mb-3 text-xs text-red-500">{probeErr}</p>}
          {probe && (
            <ProbeResultPanel id={id} result={probe} current={draft.baseURL.trim()} onAdopt={adopt} />
          )}

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

/**
 * 探测结论面板。
 *
 * 只报告、不改写——采用是用户的显式动作。这与 NormalizeURL 的立场一致：
 * 吞掉用户输入比留着一个奇怪的字符串更糟。
 *
 * base_url 为空表示一个候选都没确认，此时不给「采用」，只把试过的地址列出来
 * 供用户自己核对。
 */
function ProbeResultPanel({
  id,
  result,
  current,
  onAdopt,
}: {
  id: string
  result: ProbeResult
  current: string
  onAdopt: () => void
}) {
  const confirmed = result.base_url !== ''
  const unchanged = confirmed && result.base_url === current
  const models = result.models ?? []
  const tried = result.tried ?? []

  return (
    <div
      data-testid={`${id}-probe-result`}
      className="mb-3 rounded border border-line bg-wash px-2 py-2 text-xs"
    >
      {result.status === 'ok' && (
        <p>
          {unchanged ? (
            <Trans>地址原样可用。</Trans>
          ) : (
            <Trans>
              地址应为 <span className="font-mono">{result.base_url}</span>。
            </Trans>
          )}{' '}
          {models.length > 0 && <Trans>返回 {models.length} 个模型。</Trans>}
        </p>
      )}

      {/* 401 是有用的结果：路径确认了，问题只在 key 上。 */}
      {result.status === 'auth_failed' && (
        <p>
          <Trans>
            地址 <span className="font-mono">{result.base_url}</span> 上有这个 API，
            但这个 key 没通过。地址可以先采用，key 另外核对。
          </Trans>
        </p>
      )}

      {result.status === 'not_api' && (
        <p>
          <Trans>
            试过的地址返回的是网页而不是 JSON——多半是路径少了一段，
            中转站把它交给前端兜底了。
          </Trans>
        </p>
      )}

      {result.status === 'no_route' && (
        <p>
          <Trans>试过的地址上都没有这个 API。请核对路径。</Trans>
        </p>
      )}

      {result.status === 'unreachable' && (
        <p>
          <Trans>连不上——检查地址、网络，或者 hub 到这个站的出网。</Trans>
        </p>
      )}

      {tried.length > 0 && (
        <p className="mt-1 text-ink3">
          <Trans>试过：</Trans>
          <span className="font-mono">{tried.join('、')}</span>
        </p>
      )}

      {confirmed && (
        <button type="button" className="mt-2 text-accent" onClick={onAdopt}>
          <Trans>采用</Trans>
        </button>
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
