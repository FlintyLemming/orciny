import { atom } from 'nanostores'
import { pb } from '@/lib/pb'
import { COLLECTION_VARIABLES, type VariableRecord } from '@/types/collections'

// ---------- 机器变量 ----------
//
// 这个文件原来叫 credentials.ts，凭据那一半随 M1.6 的凭据实体一起删掉
// （spec §1.1 第二条）。机器变量留下——它管的是 {{var.*}}，与凭据无关。

export const $variables = atom<VariableRecord[]>([])
let varTicket = 0
let varSub: Promise<() => void> | null = null

export function subscribeVariables(machineId: string) {
  const mine = ++varTicket
  $variables.set([])

  void pb
    .collection(COLLECTION_VARIABLES)
    .getFullList<VariableRecord>({
      filter: pb.filter('machine = {:m}', { m: machineId }),
      sort: 'key',
    })
    .then((list) => {
      if (mine !== varTicket) return
      $variables.set(list)
    })

  if (varSub) {
    const prev = varSub
    varSub = null
    void prev.then((fn) => fn()).catch(() => {})
  }

  varSub = pb.collection(COLLECTION_VARIABLES).subscribe<VariableRecord>('*', (e) => {
    if (mine !== varTicket) return
    if (e.record.machine !== machineId && e.action !== 'delete') return
    const cur = $variables.get()
    if (e.action === 'delete') {
      $variables.set(cur.filter((v) => v.id !== e.record.id))
      return
    }
    const idx = cur.findIndex((v) => v.id === e.record.id)
    if (idx === -1) $variables.set([...cur, e.record].sort((a, b) => a.key.localeCompare(b.key)))
    else {
      const next = [...cur]
      next[idx] = e.record
      $variables.set(next.sort((a, b) => a.key.localeCompare(b.key)))
    }
  })

  return () => {
    if (mine === varTicket) $variables.set([])
    if (varSub) {
      const pending = varSub
      varSub = null
      void pending.then((fn) => fn()).catch(() => {})
    }
  }
}
