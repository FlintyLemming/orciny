/**
 * collection 的类型定义。
 *
 * 这是前端与数据库 schema 的**唯一契约点**（spec §10.2）：前端直连 PB SDK
 * 换来了实时上下线零后端代码，代价是 schema 改名会打到前端。把结构集中
 * 声明在这里，改动至少能被类型检查抓到。
 */

export type MachineStatus = 'online' | 'offline' | 'paused'

export interface MachineRecord {
  id: string
  name: string
  fingerprint: string
  pub_key: string
  hostname: string
  os: string
  arch: string
  agent_version: string
  tool_versions: Record<string, string> | null
  status: MachineStatus
  /** 仅在状态变化时写入（spec §6.5）：online 时不代表「刚刚心跳过」 */
  last_seen: string
  created: string
  updated: string
}

export type EventKind =
  | 'machine.enrolled'
  | 'machine.re-enrolled'
  | 'machine.connected'
  | 'machine.disconnected'
  | 'machine.removed'
  | 'token.issued'
  | 'auth.failed'
  | 'configset.published'
  | 'configset.rolled_back'
  | 'assign.changed'
  | 'apply.ok'
  | 'apply.failed'
  | 'apply.rollback_failed'
  | 'drift.reported'
  | 'drift.adopted'
  | 'drift.restored'
  | 'drift.ignored'
  | 'drift.superseded'
  | 'credential.created'
  | 'credential.rotated'
  | 'credential.deleted'
  | 'import.completed'

export interface EventRecord {
  id: string
  kind: EventKind
  machine: string
  detail: Record<string, unknown> | null
  created: string
}

// ---------- M1 collections ----------

export interface FileEntry {
  path: string
  hash: string
  size: number
  mode: number
  keys?: string[]
}

export interface ManifestInclude {
  path: string
  mode: 'file' | 'tree' | 'keys'
  keys?: string[]
}

export interface Manifest {
  version: number
  include: ManifestInclude[]
  exclude?: string[]
}

export interface ConfigSetRecord {
  id: string
  name: string
  note: string
  manifest: Manifest | null
  paused: boolean
  head: string
  draft: FileEntry[] | null
  draft_refs: { creds: string[]; vars: string[] } | null
  created: string
  updated: string
  /** expand=head 时带上 */
  expand?: { head?: RevisionRecord }
}

export interface RevisionRecord {
  id: string
  config_set: string
  seq: number
  files: FileEntry[] | null
  manifest: Manifest | null
  checksum: string
  refs: { creds: string[]; vars: string[] } | null
  note: string
  source: 'publish' | 'adopt' | 'rollback' | 'import'
  created: string
}

export type AssignmentState =
  | 'pending'
  | 'applying'
  | 'aligned'
  | 'failed'
  | 'degraded'
  | 'paused'

export type AssignmentMode = 'apply' | 'survey'

export interface AssignmentRecord {
  id: string
  machine: string
  config_set: string
  mode: AssignmentMode
  state: AssignmentState
  applied_revision: string
  applied_at: string
  last_error: string
  created: string
  updated: string
  expand?: {
    config_set?: ConfigSetRecord
    applied_revision?: RevisionRecord
    machine?: MachineRecord
  }
}

export interface CredentialRecord {
  id: string
  name: string
  /** 永不下发明文；前端只能看见 last4 */
  last4: string
  note: string
  created: string
  updated: string
}

export interface VariableRecord {
  id: string
  machine: string
  key: string
  value: string
  created: string
  updated: string
}

export type DriftKind = 'added' | 'modified' | 'deleted'
export type DriftState = 'open' | 'adopted' | 'restored' | 'ignored' | 'superseded'

export interface DriftEventRecord {
  id: string
  machine: string
  config_set: string
  path: string
  kind: DriftKind
  base_hash: string
  current_blob: string
  mode: number
  diff: string
  restore_partial: boolean
  truncated: boolean
  state: DriftState
  resolved_revision: string
  resolved_at: string
  created: string
  updated: string
}

export interface IgnoreRuleRecord {
  id: string
  machine: string
  path: string
  note: string
  created: string
}

export interface Finding {
  path: string
  location: string
  key: string
  masked: string
  suggested: string
  rule: string
}

export interface ValidateProblem {
  path: string
  kind: string
  detail: string
}

export const COLLECTION_MACHINES = 'machines'
export const COLLECTION_EVENTS = 'events'
export const COLLECTION_CONFIG_SETS = 'config_sets'
export const COLLECTION_REVISIONS = 'revisions'
export const COLLECTION_ASSIGNMENTS = 'assignments'
export const COLLECTION_CREDENTIALS = 'credentials'
export const COLLECTION_VARIABLES = 'variables'
export const COLLECTION_DRIFT_EVENTS = 'drift_events'
export const COLLECTION_IGNORE_RULES = 'ignore_rules'
export const COLLECTION_BLOBS = 'blobs'
