/**
 * 本机覆盖层 + realtime 订阅。
 *
 * 列表读 PB SDK；建 / 撤 / 保持走 /api/orciny/*（见 lib/api）。
 */

import { atom } from 'nanostores'
import { pb } from '@/lib/pb'
import {
  COLLECTION_MACHINE_OVERRIDES,
  type MachineOverrideRecord,
} from '@/types/collections'

export const $overrides = atom<MachineOverrideRecord[]>([])
export const $overridesLoading = atom(true)
export const $overridesError = atom('')
/** 收件箱横幅：需要用户看一眼的条数 */
export const $attentionCount = atom(0)

let subscription: Promise<() => void> | null = null
let refCount = 0

function byPathThenSelector(a: MachineOverrideRecord, b: MachineOverrideRecord) {
  return a.path.localeCompare(b.path) || a.selector.localeCompare(b.selector)
}

function recount(list: MachineOverrideRecord[]) {
  $attentionCount.set(list.filter((o) => o.attention !== '').length)
}

function commit(list: MachineOverrideRecord[]) {
  const sorted = list.slice().sort(byPathThenSelector)
  $overrides.set(sorted)
  recount(sorted)
}

async function load() {
  try {
    const list = await pb
      .collection(COLLECTION_MACHINE_OVERRIDES)
      .getFullList<MachineOverrideRecord>({
        sort: 'path,selector',
        // base_blob / mine_blob 是文本一路的两侧全文，收件箱的三方对比要它们；
        // json_key 一路两侧在 base_value / mine_value 字段里，不必 expand。
        expand: 'machine,base_blob,mine_blob,shadowed_blob',
      })
    commit(list)
    $overridesError.set('')
  } catch (e) {
    $overridesError.set(String(e))
  } finally {
    $overridesLoading.set(false)
  }
}

/** 订阅覆盖层。多个组件共用一份订阅。 */
export function subscribeOverrides() {
  refCount += 1
  if (refCount === 1 && !subscription) {
    void load()
    subscription = pb
      .collection(COLLECTION_MACHINE_OVERRIDES)
      .subscribe<MachineOverrideRecord>('*', (e) => {
        const cur = $overrides.get()
        if (e.action === 'delete') {
          commit(cur.filter((o) => o.id !== e.record.id))
          return
        }
        const idx = cur.findIndex((o) => o.id === e.record.id)
        if (idx === -1) commit([e.record, ...cur])
        else {
          const next = [...cur]
          next[idx] = e.record
          commit(next)
        }
      })
  }
  return () => {
    refCount -= 1
    if (refCount === 0 && subscription) {
      const pending = subscription
      subscription = null
      void pending.then((fn) => fn()).catch(() => {})
    }
  }
}

/** 按路径分组，路径升序；可选按机器过滤。 */
export function overridesByPath(
  list: MachineOverrideRecord[],
  machineId?: string,
): { path: string; items: MachineOverrideRecord[] }[] {
  const map = new Map<string, MachineOverrideRecord[]>()
  for (const o of list) {
    if (machineId && o.machine !== machineId) continue
    const items = map.get(o.path) ?? []
    items.push(o)
    map.set(o.path, items)
  }
  return [...map.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([path, items]) => ({ path, items }))
}
