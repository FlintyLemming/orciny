import { atom } from 'nanostores'
import { pb } from '@/lib/pb'
import {
  COLLECTION_CONFIG_SETS,
  COLLECTION_REVISIONS,
  type ConfigSetRecord,
  type RevisionRecord,
} from '@/types/collections'

export const $configSets = atom<ConfigSetRecord[]>([])
export const $configSetsLoading = atom(true)
export const $configSetsError = atom('')

export const $currentSet = atom<ConfigSetRecord | null>(null)
export const $revisions = atom<RevisionRecord[]>([])

let listSub: Promise<() => void> | null = null
let listRef = 0

function byName(a: ConfigSetRecord, b: ConfigSetRecord) {
  return a.name.localeCompare(b.name)
}

async function loadList() {
  try {
    const list = await pb.collection(COLLECTION_CONFIG_SETS).getFullList<ConfigSetRecord>({
      sort: 'name',
      expand: 'head',
    })
    $configSets.set(list.sort(byName))
    $configSetsError.set('')
  } catch (e) {
    $configSetsError.set(String(e))
  } finally {
    $configSetsLoading.set(false)
  }
}

/** 订阅配置集列表。多个组件共用一份订阅。 */
export function subscribeConfigSets() {
  listRef += 1
  if (listRef === 1 && !listSub) {
    void loadList()
    listSub = pb.collection(COLLECTION_CONFIG_SETS).subscribe<ConfigSetRecord>('*', (e) => {
      const cur = $configSets.get()
      if (e.action === 'delete') {
        $configSets.set(cur.filter((s) => s.id !== e.record.id))
        return
      }
      const idx = cur.findIndex((s) => s.id === e.record.id)
      if (idx === -1) $configSets.set([...cur, e.record].sort(byName))
      else {
        const next = [...cur]
        next[idx] = e.record
        $configSets.set(next.sort(byName))
      }
    })
  }
  return () => {
    listRef -= 1
    if (listRef === 0 && listSub) {
      const pending = listSub
      listSub = null
      void pending.then((fn) => fn()).catch(() => {})
    }
  }
}

/** 详情页：切换目标时用票号防乱序。 */
let detailTicket = 0
let detailSub: Promise<() => void> | null = null

export function subscribeConfigSet(id: string) {
  const mine = ++detailTicket
  $currentSet.set(null)
  $revisions.set([])

  void pb
    .collection(COLLECTION_CONFIG_SETS)
    .getOne<ConfigSetRecord>(id, { expand: 'head' })
    .then((rec) => {
      if (mine !== detailTicket) return
      $currentSet.set(rec)
    })
    .catch(() => {
      if (mine === detailTicket) $currentSet.set(null)
    })

  void pb
    .collection(COLLECTION_REVISIONS)
    .getFullList<RevisionRecord>({
      filter: pb.filter('config_set = {:s}', { s: id }),
      sort: '-seq',
    })
    .then((list) => {
      if (mine !== detailTicket) return
      $revisions.set(list)
    })

  // 退订上一次详情订阅
  if (detailSub) {
    const prev = detailSub
    detailSub = null
    void prev.then((fn) => fn()).catch(() => {})
  }

  detailSub = pb.collection(COLLECTION_CONFIG_SETS).subscribe<ConfigSetRecord>(id, (e) => {
    if (mine !== detailTicket) return
    if (e.action === 'delete') {
      $currentSet.set(null)
      return
    }
    $currentSet.set(e.record)
  })

  return () => {
    if (mine === detailTicket) {
      $currentSet.set(null)
      $revisions.set([])
    }
    if (detailSub) {
      const pending = detailSub
      detailSub = null
      void pending.then((fn) => fn()).catch(() => {})
    }
  }
}

export async function reloadConfigSet(id: string) {
  const rec = await pb.collection(COLLECTION_CONFIG_SETS).getOne<ConfigSetRecord>(id, {
    expand: 'head',
  })
  $currentSet.set(rec)
  const list = await pb.collection(COLLECTION_REVISIONS).getFullList<RevisionRecord>({
    filter: pb.filter('config_set = {:s}', { s: id }),
    sort: '-seq',
  })
  $revisions.set(list)
  // 同步列表里的那一项
  const cur = $configSets.get()
  const idx = cur.findIndex((s) => s.id === id)
  if (idx >= 0) {
    const next = [...cur]
    next[idx] = rec
    $configSets.set(next)
  }
}

export async function setConfigSetPaused(id: string, paused: boolean) {
  await pb.collection(COLLECTION_CONFIG_SETS).update(id, { paused })
}
