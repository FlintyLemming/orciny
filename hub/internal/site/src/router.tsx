import { atom } from 'nanostores'

/**
 * M0 不引入路由库：一共六个屏，一个 hash 就够，少一个依赖少一份升级负担。
 * M1 页面变多时再换 react-router。
 */
export type RouteKey =
  | 'overview' | 'machines' | 'configsets' | 'inbox'
  | 'usage' | 'subscriptions' | 'providers' | 'settings'
  | 'import'

const known: RouteKey[] = [
  'overview', 'machines', 'configsets', 'inbox',
  'usage', 'subscriptions', 'providers', 'settings', 'import',
]

function parseHash(): { key: RouteKey; param?: string } {
  const raw = window.location.hash.replace(/^#\/?/, '')
  const [key, param] = raw.split('/')
  if (known.includes(key as RouteKey)) return { key: key as RouteKey, param }
  return { key: 'machines' }
}

export const $route = atom<{ key: RouteKey; param?: string }>(parseHash())

window.addEventListener('hashchange', () => $route.set(parseHash()))

export function navigate(key: RouteKey, param?: string) {
  window.location.hash = param ? `#/${key}/${param}` : `#/${key}`
}
