/**
 * /api/orciny/* 的调用收口。
 *
 * 组件不直接 fetch：错误消息提取与鉴权头在这里统一处理。
 * 读列表走 PocketBase SDK（见 stores/*）；写动作与校验走这里。
 */

import { pb } from '@/lib/pb'
import type {
  AuthField,
  Binding,
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

export function createCredential(name: string, value: string, note = '') {
  return postJSON('/api/orciny/credentials', { name, value, note })
}

export function rotateCredential(id: string, value: string) {
  return postJSON(`/api/orciny/credentials/${id}/rotate`, { value })
}

export function deleteCredential(id: string) {
  return deleteJSON(`/api/orciny/credentials/${id}`)
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

export function extractCredential(setId: string, path: string, location: string, name: string) {
  return postJSON(`/api/orciny/config-sets/${setId}/extract`, { path, location, name })
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

export function clearDegraded(machineId: string) {
  return postJSON(`/api/orciny/machines/${machineId}/clear-degraded`)
}

// ---------- M1.5 AI 服务配置与绑定 ----------

export interface ProviderBody {
  name: string
  preset: string
  base_url: string
  auth_field: AuthField
  credential: string
  models: string[]
  defaults: ModelSlots
  note: string
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
