import { atom } from 'nanostores'
import { pb } from '@/lib/pb'
import { COLLECTION_EVENTS, type EventRecord } from '@/types/collections'

export const $events = atom<EventRecord[]>([])
export const $eventsLoading = atom(true)

const PAGE_SIZE = 50

/**
 * 每次订阅发一张票，用来作废迟到的响应。
 *
 * 快速切换机器时，先发出的那次 getList 可能后返回，把后一台机器的事件流
 * 覆盖掉。回调里核对票号，过期的直接丢。
 */
let ticket = 0

/** 订阅某台机器的事件流。返回退订函数。 */
export function subscribeEvents(machineId: string) {
  const mine = ++ticket
  $eventsLoading.set(true)
  $events.set([])

  void pb
    .collection(COLLECTION_EVENTS)
    .getList<EventRecord>(1, PAGE_SIZE, {
      filter: pb.filter('machine = {:m}', { m: machineId }),
      sort: '-created',
    })
    .then((res) => {
      if (mine !== ticket) return
      $events.set(res.items)
    })
    .finally(() => {
      if (mine === ticket) $eventsLoading.set(false)
    })

  // 同 stores/machines.ts：存 Promise 而非解析后的退订函数，否则 StrictMode
  // 的「挂载→卸载→再挂载」会在退订函数还没到手时跳过退订，订阅泄漏。
  const subscription = pb
    .collection(COLLECTION_EVENTS)
    .subscribe<EventRecord>('*', (e) => {
      if (mine !== ticket) return
      if (e.action !== 'create' || e.record.machine !== machineId) return
      $events.set([e.record, ...$events.get()].slice(0, PAGE_SIZE))
    })

  return () => {
    void subscription.then((fn) => fn()).catch(() => {})
    if (mine === ticket) $events.set([])
  }
}
