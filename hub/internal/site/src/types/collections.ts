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

export interface EventRecord {
  id: string
  kind: EventKind
  machine: string
  detail: Record<string, unknown> | null
  created: string
}

export const COLLECTION_MACHINES = 'machines'
export const COLLECTION_EVENTS = 'events'
