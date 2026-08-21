/**
 * 漂移收件箱 + realtime 订阅。
 *
 * 列表读 PB SDK；收编/恢复/忽略走 /api/orciny/drift/*（见 lib/api）。
 */

import { atom } from 'nanostores'
import { pb } from '@/lib/pb'
import {
  COLLECTION_DRIFT_EVENTS,
  type DriftEventRecord,
  type DriftState,
} from '@/types/collections'
import type { DriftEvent } from '@/lib/inbox'

export const $drifts = atom<DriftEventRecord[]>([])
export const $driftsLoading = atom(true)
export const $driftsError = atom('')
/** 侧栏角标：open 条目数 */
export const $openDriftCount = atom(0)

let subscription: Promise<() => void> | null = null
let refCount = 0

function byCreatedDesc(a: DriftEventRecord, b: DriftEventRecord) {
  return b.created.localeCompare(a.created)
}

function recount(list: DriftEventRecord[]) {
  $openDriftCount.set(list.filter((d) => d.state === 'open').length)
}

async function load() {
  try {
    const list = await pb.collection(COLLECTION_DRIFT_EVENTS).getFullList<DriftEventRecord>({
      sort: '-created',
      expand: 'machine,config_set,current_blob',
    })
    const sorted = list.slice().sort(byCreatedDesc)
    $drifts.set(sorted)
    recount(sorted)
    $driftsError.set('')
  } catch (e) {
    $driftsError.set(String(e))
  } finally {
    $driftsLoading.set(false)
  }
}

/** 订阅漂移事件。多个组件共用一份订阅。 */
export function subscribeDrifts() {
  refCount += 1
  if (refCount === 1 && !subscription) {
    void load()
    subscription = pb.collection(COLLECTION_DRIFT_EVENTS).subscribe<DriftEventRecord>('*', (e) => {
      const cur = $drifts.get()
      if (e.action === 'delete') {
        const next = cur.filter((d) => d.id !== e.record.id)
        $drifts.set(next)
        recount(next)
        return
      }
      const idx = cur.findIndex((d) => d.id === e.record.id)
      let next: DriftEventRecord[]
      if (idx === -1) next = [e.record, ...cur]
      else {
        next = [...cur]
        next[idx] = e.record
      }
      next.sort(byCreatedDesc)
      $drifts.set(next)
      recount(next)
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

/** 把 PB 记录收成 inbox 纯逻辑用的视图模型。 */
export function toDriftEvent(r: DriftEventRecord): DriftEvent {
  return {
    id: r.id,
    machine: r.machine,
    config_set: r.config_set,
    path: r.path,
    kind: r.kind,
    state: r.state,
    diff: r.diff ?? '',
    truncated: !!r.truncated,
    binding_drift: !!r.binding_drift,
    binding_url: r.binding_url ?? '',
    restore_partial: !!r.restore_partial,
    created: r.created,
  }
}

export type DriftFilter = DriftState | 'all'

export function filterDrifts(
  list: DriftEventRecord[],
  filter: DriftFilter,
  machineId?: string,
): DriftEventRecord[] {
  return list.filter((d) => {
    if (machineId && d.machine !== machineId) return false
    if (filter === 'all') return true
    return d.state === filter
  })
}
