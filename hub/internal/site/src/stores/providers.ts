import { atom } from 'nanostores'
import { pb } from '@/lib/pb'
import { COLLECTION_PROVIDERS, type ProviderRecord } from '@/types/collections'

export const $providers = atom<ProviderRecord[]>([])
export const $providersLoading = atom(true)
export const $providersError = atom('')

let provSub: Promise<() => void> | null = null
let provRef = 0

function byName(a: ProviderRecord, b: ProviderRecord) {
  return a.name.localeCompare(b.name)
}

/**
 * 订阅事件里 expand 的 credential 可能带 cipher_value，
 * 照 stores/credentials.ts 的做法只留非敏感字段。
 */
function sanitize(raw: ProviderRecord): ProviderRecord {
  const cred = raw.expand?.credential
  return {
    id: raw.id,
    name: raw.name,
    preset: raw.preset,
    base_url: raw.base_url,
    auth_field: raw.auth_field,
    credential: raw.credential,
    models: raw.models,
    defaults: raw.defaults,
    note: raw.note,
    created: raw.created,
    updated: raw.updated,
    expand: cred
      ? {
          credential: {
            id: cred.id,
            name: cred.name,
            last4: cred.last4,
            note: cred.note,
            created: cred.created,
            updated: cred.updated,
          },
        }
      : undefined,
  }
}

async function loadProviders() {
  try {
    const list = await pb.collection(COLLECTION_PROVIDERS).getFullList<ProviderRecord>({
      sort: 'name',
      expand: 'credential',
    })
    $providers.set(list.map(sanitize).sort(byName))
    $providersError.set('')
  } catch (e) {
    $providersError.set(String(e))
  } finally {
    $providersLoading.set(false)
  }
}

export function subscribeProviders() {
  provRef += 1
  if (provRef === 1 && !provSub) {
    void loadProviders()
    provSub = pb.collection(COLLECTION_PROVIDERS).subscribe<ProviderRecord>('*', (e) => {
      const cur = $providers.get()
      if (e.action === 'delete') {
        $providers.set(cur.filter((p) => p.id !== e.record.id))
        return
      }
      const safe = sanitize(e.record)
      const idx = cur.findIndex((p) => p.id === safe.id)
      if (idx === -1) $providers.set([...cur, safe].sort(byName))
      else {
        const next = [...cur]
        next[idx] = safe
        $providers.set(next.sort(byName))
      }
    })
  }
  return () => {
    provRef -= 1
    if (provRef === 0 && provSub) {
      const pending = provSub
      provSub = null
      void pending.then((fn) => fn()).catch(() => {})
    }
  }
}

export async function reloadProviders() {
  await loadProviders()
}
