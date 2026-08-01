import { atom } from 'nanostores'
import { pb } from '@/lib/pb'
import {
  COLLECTION_CREDENTIALS,
  COLLECTION_VARIABLES,
  type CredentialRecord,
  type VariableRecord,
} from '@/types/collections'

export const $credentials = atom<CredentialRecord[]>([])
export const $credentialsLoading = atom(true)
export const $credentialsError = atom('')

let credSub: Promise<() => void> | null = null
let credRef = 0

function byName(a: CredentialRecord, b: CredentialRecord) {
  return a.name.localeCompare(b.name)
}

async function loadCreds() {
  try {
    const list = await pb.collection(COLLECTION_CREDENTIALS).getFullList<CredentialRecord>({
      sort: 'name',
      fields: 'id,name,last4,note,created,updated',
    })
    $credentials.set(list.sort(byName))
    $credentialsError.set('')
  } catch (e) {
    $credentialsError.set(String(e))
  } finally {
    $credentialsLoading.set(false)
  }
}

export function subscribeCredentials() {
  credRef += 1
  if (credRef === 1 && !credSub) {
    void loadCreds()
    credSub = pb.collection(COLLECTION_CREDENTIALS).subscribe<CredentialRecord>('*', (e) => {
      const cur = $credentials.get()
      if (e.action === 'delete') {
        $credentials.set(cur.filter((c) => c.id !== e.record.id))
        return
      }
      // 订阅事件可能带 cipher_value，丢掉敏感字段
      const safe: CredentialRecord = {
        id: e.record.id,
        name: e.record.name,
        last4: e.record.last4,
        note: e.record.note,
        created: e.record.created,
        updated: e.record.updated,
      }
      const idx = cur.findIndex((c) => c.id === safe.id)
      if (idx === -1) $credentials.set([...cur, safe].sort(byName))
      else {
        const next = [...cur]
        next[idx] = safe
        $credentials.set(next.sort(byName))
      }
    })
  }
  return () => {
    credRef -= 1
    if (credRef === 0 && credSub) {
      const pending = credSub
      credSub = null
      void pending.then((fn) => fn()).catch(() => {})
    }
  }
}

export async function reloadCredentials() {
  await loadCreds()
}

// ---------- 机器变量 ----------

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
