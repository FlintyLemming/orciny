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

export type AuthField = 'ANTHROPIC_AUTH_TOKEN' | 'ANTHROPIC_API_KEY'

export interface ModelSlots {
  main: string
  opus: string
  sonnet: string
  haiku: string
}

/** 配置集的服务绑定。单数，不是数组（M1.5 spec §2.2）。 */
export interface Binding {
  provider: string
  models: ModelSlots
}

export interface ProviderRecord {
  id: string
  name: string
  preset: string
  base_url: string
  auth_field: AuthField
  /** credentials 记录 id；key 本身永不下发到前端 */
  credential: string
  models: string[] | null
  defaults: ModelSlots | null
  note: string
  created: string
  updated: string
  expand?: { credential?: CredentialRecord }
}

/** 内置预设。编译进 hub 二进制，只读（M1.5 spec §2.3）。 */
export interface ProviderPreset {
  id: string
  name: string
  base_url: string
  auth_field: AuthField
  models: string[]
  defaults: ModelSlots
  website_url?: string
  api_key_url?: string
  icon?: string
  icon_color?: string
  collector_type?: string
  collector_mode?: string
}

/** refs / draft_refs 的形状，与 Go 侧 configsets.Refs 一致。 */
export interface RefsField {
  creds: string[]
  vars: string[]
  provider_keys: string[]
}

export interface ConfigSetRecord {
  id: string
  name: string
  note: string
  manifest: Manifest | null
  paused: boolean
  head: string
  draft: FileEntry[] | null
  draft_refs: RefsField | null
  /** null = 未绑定 */
  draft_binding: Binding | null
  /** 冗余字段，唯一写入点在发布路径（M1.5 spec §2.2） */
  head_provider: string
  created: string
  updated: string
  /** expand=head,head_provider 时带上 */
  expand?: { head?: RevisionRecord; head_provider?: ProviderRecord }
}

export interface RevisionRecord {
  id: string
  config_set: string
  seq: number
  files: FileEntry[] | null
  manifest: Manifest | null
  checksum: string
  refs: RefsField | null
  /** 冻结的绑定；null = 无绑定 */
  binding: Binding | null
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

export interface BlobRecord {
  id: string
  hash: string
  size: number
  content: string
  created: string
}

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
  /** 基线是 {{provider.*}} 占位符、机器上是字面值（M1.5 spec §6.1） */
  binding_drift: boolean
  /** 机器上那段字面 base_url，供反查用 */
  binding_url: string
  state: DriftState
  resolved_revision: string
  resolved_at: string
  created: string
  updated: string
  expand?: {
    machine?: MachineRecord
    config_set?: ConfigSetRecord
    current_blob?: BlobRecord
  }
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

export interface ValidateProblemFix {
  kind: 'replace_env_key'
  from: string
  to: string
}

export interface ValidateProblem {
  path: string
  kind: string
  detail: string
  /** 真 = 展示但不阻断发布（M1.5 spec §7 第 2 条） */
  warning?: boolean
  /** 非空 = 给「一键修复」 */
  fix?: ValidateProblemFix
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
export const COLLECTION_PROVIDERS = 'providers'
