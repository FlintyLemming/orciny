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
 * 白名单式过滤：只把已知的非敏感字段放进 store。
 *
 * 密文本来就是后端的 Hidden 字段、PocketBase 不会下发；这里再挡一道，
 * 是因为「有一天有人把 Hidden 摘了」的代价是把 key 的密文塞进浏览器内存。
 */
export function sanitizeProvider(raw: ProviderRecord): ProviderRecord {
  return {
    id: raw.id,
    name: raw.name,
    preset: raw.preset,
    note: raw.note,
    key_last4: raw.key_last4 ?? '',
    claude: raw.claude
      ? {
          base_url: raw.claude.base_url,
          auth_field: raw.claude.auth_field,
          key_last4: raw.claude.key_last4,
          models: raw.claude.models,
          defaults: raw.claude.defaults,
        }
      : null,
    openai: raw.openai
      ? {
          base_url: raw.openai.base_url,
          auth_field: raw.openai.auth_field,
          key_last4: raw.openai.key_last4,
          models: raw.openai.models,
          default_model: raw.openai.default_model,
        }
      : null,
    created: raw.created,
    updated: raw.updated,
  }
}

async function loadProviders() {
  try {
    const list = await pb.collection(COLLECTION_PROVIDERS).getFullList<ProviderRecord>({
      sort: 'name',
    })
    $providers.set(list.map(sanitizeProvider).sort(byName))
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
      const safe = sanitizeProvider(e.record)
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
