/**
 * 收件箱的纯逻辑：分组、冲突判定、收编前置检查、unified diff 解析。
 *
 * 组件只负责渲染；能不能收编、哪些算冲突，全部在这里定。
 */

import { t } from '@lingui/core/macro'

export type DriftState = 'open' | 'adopted' | 'restored' | 'ignored' | 'superseded'
export type DriftKind = 'added' | 'modified' | 'deleted'

export interface DriftEvent {
  id: string
  machine: string
  config_set: string
  path: string
  kind: DriftKind
  state: DriftState
  diff: string
  truncated: boolean
  restore_partial: boolean
  /** 基线是 {{provider.*}} 占位符、机器上是字面值（M1.5 spec §6.1） */
  binding_drift: boolean
  /** 机器上那段字面 base_url，供反查用 */
  binding_url: string
  created: string
}

export interface Conflict {
  path: string
  events: DriftEvent[]
}

export interface DiffLine {
  type: 'add' | 'del' | 'ctx' | 'meta'
  text: string
}

/** 按机器分组，机器 id 升序；组内按路径升序。 */
export function groupByMachine(events: DriftEvent[]): { machine: string; events: DriftEvent[] }[] {
  const map = new Map<string, DriftEvent[]>()
  for (const e of events) {
    const list = map.get(e.machine) ?? []
    list.push(e)
    map.set(e.machine, list)
  }
  return [...map.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([machine, list]) => ({
      machine,
      events: list.slice().sort((a, b) => a.path.localeCompare(b.path)),
    }))
}

/** 按路径分组，路径升序；组内按机器升序。 */
export function groupByPath(events: DriftEvent[]): { path: string; events: DriftEvent[] }[] {
  const map = new Map<string, DriftEvent[]>()
  for (const e of events) {
    const list = map.get(e.path) ?? []
    list.push(e)
    map.set(e.path, list)
  }
  return [...map.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([path, list]) => ({
      path,
      events: list.slice().sort((a, b) => a.machine.localeCompare(b.machine)),
    }))
}

/**
 * 同路径且来自不同机器 → 冲突。
 * 同路径同机器（重复上报等）不算冲突。
 */
export function findConflicts(selected: DriftEvent[]): Conflict[] {
  const byPath = groupByPath(selected)
  const out: Conflict[] = []
  for (const g of byPath) {
    const machines = new Set(g.events.map((e) => e.machine))
    if (machines.size > 1) out.push(g)
  }
  return out
}

/** restore_partial 的条目要先人工确认，但不是硬阻止。 */
export function needsReview(selected: DriftEvent[]): DriftEvent[] {
  return selected.filter((e) => e.restore_partial)
}

/**
 * 返回阻止收编的中文理由（空数组 = 可以收编）。
 * 理由直接展示给用户，不是错误码。
 */
export function adoptBlockers(selected: DriftEvent[]): string[] {
  if (selected.length === 0) {
    return [t`请先选择要收编的漂移条目`]
  }

  const blockers: string[] = []

  const sets = new Set(selected.map((e) => e.config_set))
  if (sets.size > 1) {
    blockers.push(t`不能跨配置集收编：所选条目属于不同配置集`)
  }

  for (const c of findConflicts(selected)) {
    blockers.push(t`路径 ${c.path} 存在跨机器冲突，请先做三方对比`)
  }

  for (const e of selected.filter((x) => x.truncated)) {
    blockers.push(t`${e.path} 内容未能安全脱敏，无法自动收编`)
  }

  // 收编会把占位符拍平成硬编码地址，绑定当场失效（M1.5 spec §6.2）。
  for (const e of selected.filter((x) => x.binding_drift)) {
    blockers.push(
      t`${e.path} 是服务绑定漂移：收编会把占位符拍平成硬编码地址，绑定会当场失效。请改用「改绑定」或「恢复」`,
    )
  }

  return blockers
}

/** 解析 unified diff 文本为带类型的行。标记字符从 text 中剥掉。 */
export function parseUnifiedDiff(diff: string): DiffLine[] {
  if (!diff) return []
  const lines = diff.replace(/\n$/, '').split('\n')
  // 空字符串 split 得到 ['']，视为无内容
  if (lines.length === 1 && lines[0] === '') return []

  const out: DiffLine[] = []
  for (const raw of lines) {
    if (
      raw.startsWith('---') ||
      raw.startsWith('+++') ||
      raw.startsWith('@@') ||
      raw.startsWith('diff ') ||
      raw.startsWith('index ')
    ) {
      out.push({ type: 'meta', text: raw })
      continue
    }
    if (raw.startsWith('+')) {
      out.push({ type: 'add', text: raw.slice(1) })
      continue
    }
    if (raw.startsWith('-')) {
      out.push({ type: 'del', text: raw.slice(1) })
      continue
    }
    // 上下文行通常以空格开头；没有前缀时整行当上下文
    const text = raw.startsWith(' ') ? raw.slice(1) : raw
    out.push({ type: 'ctx', text })
  }
  return out
}
