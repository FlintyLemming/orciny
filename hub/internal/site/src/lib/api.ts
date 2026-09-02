/**
 * /api/orciny/* 的调用收口。
 *
 * 组件不直接 fetch：错误消息提取与鉴权头在这里统一处理。
 * 读列表走 PocketBase SDK（见 stores/*）；写动作与校验走这里。
 */

import { pb } from '@/lib/pb'
import type {
  Binding,
  BindingMatchResult,
  ClaudeModel,
  FileEntry,
  Finding,
  ModelSlots,
  ProviderPreset,
  ValidateProblem,
} from '@/types/collections'
import type { FileChange } from '@/lib/diffView'
import type { Manifest } from '@/types/collections'

export class ApiError extends Error {
  status: number
  constructor(message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

function authHeaders(): HeadersInit {
  const token = pb.authStore.token
  return token
    ? { Authorization: token, 'Content-Type': 'application/json' }
    : { 'Content-Type': 'application/json' }
}

async function parseError(res: Response): Promise<string> {
  try {
    const body = (await res.json()) as { message?: string; data?: unknown }
    if (body.message) return body.message
  } catch {
    /* ignore */
  }
  return res.statusText || `HTTP ${res.status}`
}

export async function postJSON<T = unknown>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method: 'POST',
    headers: authHeaders(),
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) throw new ApiError(await parseError(res), res.status)
  if (res.status === 204) return undefined as T
  const text = await res.text()
  return text ? (JSON.parse(text) as T) : (undefined as T)
}

export async function putJSON<T = unknown>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method: 'PUT',
    headers: authHeaders(),
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) throw new ApiError(await parseError(res), res.status)
  if (res.status === 204) return undefined as T
  const text = await res.text()
  return text ? (JSON.parse(text) as T) : (undefined as T)
}

export async function deleteJSON<T = unknown>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method: 'DELETE',
    headers: authHeaders(),
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) throw new ApiError(await parseError(res), res.status)
  if (res.status === 204) return undefined as T
  const text = await res.text()
  return text ? (JSON.parse(text) as T) : (undefined as T)
}

export async function getJSON<T = unknown>(path: string): Promise<T> {
  const res = await fetch(path, { headers: authHeaders() })
  if (!res.ok) throw new ApiError(await parseError(res), res.status)
  return (await res.json()) as T
}

export async function getText(path: string): Promise<string> {
  const res = await fetch(path, { headers: authHeaders() })
  if (!res.ok) throw new ApiError(await parseError(res), res.status)
  return res.text()
}

// ---------- 业务封装 ----------

export function createConfigSet(name: string, note = '') {
  return postJSON<{ id: string; name: string }>('/api/orciny/config-sets', { name, note })
}

export function cloneConfigSet(id: string, name: string) {
  return postJSON<{ id: string }>(`/api/orciny/config-sets/${id}/clone`, { name })
}

export function deleteConfigSet(id: string) {
  return deleteJSON(`/api/orciny/config-sets/${id}`)
}

export function setDraftFile(
  setId: string,
  path: string,
  content: string,
  mode = 0o644,
  keys?: string[],
) {
  return postJSON<FileEntry>(`/api/orciny/config-sets/${setId}/files`, {
    path,
    content,
    mode,
    keys,
  })
}

export function removeDraftFile(setId: string, path: string) {
  return deleteJSON(`/api/orciny/config-sets/${setId}/files`, { path })
}

export function setManifest(setId: string, manifest: Manifest) {
  return putJSON(`/api/orciny/config-sets/${setId}/manifest`, manifest)
}

export function validateConfigSet(setId: string) {
  return postJSON<ValidateProblem[]>(`/api/orciny/config-sets/${setId}/validate`)
}

export function publishConfigSet(setId: string, note: string) {
  return postJSON<{ revision: string }>(`/api/orciny/config-sets/${setId}/publish`, { note })
}

export function rollbackConfigSet(setId: string, revision: string) {
  return postJSON<{ revision: string }>(`/api/orciny/config-sets/${setId}/rollback`, { revision })
}

export function diffConfigSet(setId: string, from: string, to: string) {
  return getJSON<{ changes: FileChange[]; diffs: Record<string, string> }>(
    `/api/orciny/config-sets/${setId}/diff?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
  )
}

export function getBlob(hash: string) {
  return getText(`/api/orciny/blobs/${hash}`)
}

export function assignConfigSet(machine: string, config_set: string, mode: 'apply' | 'survey') {
  return postJSON('/api/orciny/assignments', { machine, config_set, mode })
}

export function setMachineVariables(machineId: string, vars: Record<string, string>) {
  return putJSON(`/api/orciny/machines/${machineId}/variables`, vars)
}

export function startImport(machineId: string) {
  return postJSON<{ token: string; config_set: string }>(
    `/api/orciny/machines/${machineId}/import`,
  )
}

export function importFindings(setId: string) {
  return getJSON<Finding[]>(`/api/orciny/config-sets/${setId}/findings`)
}

/** 把草稿里某处的值抽成某个 provider 端点的 key（M1.6 spec §5.5）。 */
export function extractKey(
  setId: string,
  path: string,
  location: string,
  provider: string,
  endpoint: 'claude' | 'openai',
) {
  return postJSON(`/api/orciny/config-sets/${setId}/extract`, {
    path,
    location,
    provider,
    endpoint,
  })
}

/** 反查文件里的 base_url，供抽取向导预选 provider。 */
export function matchProviderIn(setId: string, path: string) {
  return getJSON<ProviderMatch>(
    `/api/orciny/config-sets/${setId}/provider-match?path=${encodeURIComponent(path)}`,
  )
}

export function adoptDrift(events: string[], reviewed: string[] = []) {
  return postJSON<{ revision: string }>('/api/orciny/drift/adopt', {
    events,
    reviewed: reviewed.length > 0 ? reviewed : undefined,
  })
}

export function restoreDrift(events: string[]) {
  return postJSON('/api/orciny/drift/restore', { events })
}

export function ignoreDrift(events: string[], global = false) {
  return postJSON('/api/orciny/drift/ignore', { events, global })
}

/** 一个 event 的勾选结果。缺席 = 全选（M1.8 spec §7） */
export interface OverrideSelection {
  selectors?: string[]
  hunks?: number[]
}

/** 本机保留：把选中的漂移转成覆盖层。 */
export function overrideDrift(
  events: string[],
  points: Record<string, OverrideSelection> = {},
  reviewed: string[] = [],
) {
  return postJSON('/api/orciny/drift/override', { events, points, reviewed })
}

/** 撤掉排除：下次快照这台机器就拿到中台的值。 */
export function dropOverride(id: string) {
  return deleteJSON(`/api/orciny/overrides/${id}`)
}

/** 保持：清掉提醒，横幅上消失。 */
export function keepOverride(id: string) {
  return postJSON(`/api/orciny/overrides/${id}/keep`)
}

/** 恢复受管：原子地置 survey 并删掉该路径的忽略规则。 */
export function remanage(machineId: string, path: string) {
  return postJSON(`/api/orciny/machines/${machineId}/remanage`, { path })
}

export function clearDegraded(machineId: string) {
  return postJSON(`/api/orciny/machines/${machineId}/clear-degraded`)
}

// ---------- M1.5 AI 服务配置与绑定 ----------

export interface ClaudeEndpointBody {
  base_url: string
  auth_field?: string
  models: ClaudeModel[]
  /** 省略 = 不修改；'' = 清空；有值 = 替换（M1.6 spec §5.2） */
  key?: string
  defaults?: ModelSlots
}

export interface OpenAIEndpointBody {
  base_url: string
  auth_field?: string
  models: string[]
  /** 省略 = 不修改；'' = 清空；有值 = 替换（M1.6 spec §5.2） */
  key?: string
  default_model?: string
}

export interface ProviderBody {
  name: string
  preset: string
  note: string
  /** 平台级 key，三态同 EndpointBody.key */
  key?: string
  claude: ClaudeEndpointBody
  openai: OpenAIEndpointBody
}

/** providers.MatchBaseURL 的反查结果（M1.5 spec §6.3 的三档）。 */
export interface ProviderMatch {
  kind: 'provider' | 'preset' | 'none'
  exact: boolean
  provider_id?: string
  provider_name?: string
  preset_id?: string
  preset_name?: string
}

export function listProviderPresets() {
  return getJSON<ProviderPreset[]>('/api/orciny/provider-presets')
}

export function createProvider(body: ProviderBody) {
  return postJSON<{ id: string }>('/api/orciny/providers', body)
}

export function updateProvider(id: string, body: ProviderBody) {
  return putJSON(`/api/orciny/providers/${id}`, body)
}

/** 一次端点探测的输入。key 随请求走：主场景是新建，那会儿 key 还没落库。 */
export interface ProbeInput {
  endpoint: 'claude' | 'openai'
  base_url: string
  key: string
}

/**
 * status 是后端的信号阶梯（providers.Signal.Status），UI 按它分支措辞：
 *   ok          地址与 key 都对，models 可用
 *   auth_failed 地址对、key 不对——地址仍可采用
 *   not_api     撞上了中转站的前端兜底页（200 + HTML），路径不对
 *   no_route    API 认得域名但没有这条路由
 *   unreachable 连不上
 *
 * base_url 只在确认到路径时非空；空串表示一个候选都没确认，此时**保留用户的输入**。
 */
export interface ProbeResult {
  status: 'ok' | 'auth_failed' | 'not_api' | 'no_route' | 'unreachable'
  base_url: string
  models: string[] | null
  tried: string[] | null
}

export function probeEndpoint(input: ProbeInput) {
  return postJSON<ProbeResult>('/api/orciny/providers/probe', input)
}

export function deleteProvider(id: string) {
  return deleteJSON(`/api/orciny/providers/${id}`)
}

/** provider 传空串即解绑——与「设置」同一个端点。 */
export function setBinding(setId: string, binding: Binding | null) {
  return putJSON(
    `/api/orciny/config-sets/${setId}/binding`,
    binding ?? { provider: '', models: { main: '', opus: '', sonnet: '', haiku: '' } },
  )
}

export function fixAuthField(setId: string) {
  return postJSON(`/api/orciny/config-sets/${setId}/fix-auth-field`)
}

export function matchBindingDrift(eventId: string) {
  return getJSON<BindingMatchResult>(`/api/orciny/drift/${eventId}/binding-match`)
}

export function rebindDrift(eventId: string, provider: string) {
  return postJSON<{ revision: string }>(`/api/orciny/drift/${eventId}/rebind`, { provider })
}

export function createProviderFromDrift(
  body: ProviderBody & {
    from_drift: { event: string; location: string; endpoint: 'claude' | 'openai' }
  },
) {
  return postJSON<{ id: string }>('/api/orciny/providers', body)
}

/** 把 PB 草稿条目规范化成小写键（兼容旧数据的 Path/Hash 大写）。 */
export function normalizeFileEntry(raw: Record<string, unknown>): FileEntry {
  return {
    path: String(raw.path ?? raw.Path ?? ''),
    hash: String(raw.hash ?? raw.Hash ?? ''),
    size: Number(raw.size ?? raw.Size ?? 0),
    mode: Number(raw.mode ?? raw.Mode ?? 0),
    keys: (raw.keys ?? raw.Keys) as string[] | undefined,
  }
}
