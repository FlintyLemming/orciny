import { atom } from 'nanostores'
import { pb } from '@/lib/pb'
import { COLLECTION_MACHINES, type MachineRecord } from '@/types/collections'

/**
 * 机器列表 + realtime 订阅。
 *
 * 组件不直接调 SDK（spec §10.2）：订阅统一收在这里，一处管理生命周期，
 * 也避免每个组件各自维护一份可能不一致的列表。
 */
export const $machines = atom<MachineRecord[]>([])
export const $machinesLoading = atom(true)
export const $machinesError = atom<string>('')

/**
 * 存的是订阅的 Promise 而不是解析后的退订函数。
 *
 * subscribe() 是异步的，而 StrictMode 在开发下会「挂载 → 卸载 → 再挂载」。
 * 若只在 then 里存退订函数，卸载那一刻它还是 null，退订就被跳过，
 * 再挂载又订一次——第一条订阅永久泄漏，每个事件被处理两遍。
 * 记住 Promise 就能在任何时刻可靠地退订。
 */
let subscription: Promise<() => void> | null = null
let refCount = 0

function byName(a: MachineRecord, b: MachineRecord) {
  return (a.name || a.hostname).localeCompare(b.name || b.hostname)
}

async function load() {
  try {
    const list = await pb.collection(COLLECTION_MACHINES).getFullList<MachineRecord>({
      sort: 'name',
    })
    // 客户端再排一次：realtime 更新走的是 byName（name 为空时退回 hostname），
    // 两处排序规则必须一致，否则任一更新到达时整张表会重新洗牌。
    $machines.set(list.sort(byName))
    $machinesError.set('')
  } catch (e) {
    $machinesError.set(String(e))
  } finally {
    $machinesLoading.set(false)
  }
}

/** 订阅机器变化。返回退订函数；多个组件共用一份订阅。 */
export function subscribeMachines() {
  refCount += 1
  if (refCount === 1 && !subscription) {
    void load()
    subscription = pb.collection(COLLECTION_MACHINES).subscribe<MachineRecord>('*', (e) => {
      const cur = $machines.get()
      if (e.action === 'delete') {
        $machines.set(cur.filter((m) => m.id !== e.record.id))
        return
      }
      const idx = cur.findIndex((m) => m.id === e.record.id)
      if (idx === -1) $machines.set([...cur, e.record].sort(byName))
      else {
        const next = [...cur]
        next[idx] = e.record
        $machines.set(next.sort(byName))
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

export async function renameMachine(id: string, name: string) {
  await pb.collection(COLLECTION_MACHINES).update(id, { name })
}

export async function deleteMachine(id: string) {
  // 删除语义简单，走 PB SDK；hub 侧的 record hook 负责踢掉活跃连接（spec §5.2）。
  await pb.collection(COLLECTION_MACHINES).delete(id)
}
