import { atom } from 'nanostores'
import { pb } from '@/lib/pb'

/**
 * M0 用 PocketBase superuser 作为唯一身份，不建 users collection（spec §5.3）。
 * 已知取舍：前端持有的 token 等价于完整数据库权限——单管理员场景可接受，
 * 省掉一整套用户与角色代码。v2 做微团队时再引入 users collection。
 */
export const $authed = atom(pb.authStore.isValid)
export const $authEmail = atom((pb.authStore.record?.email as string) ?? '')

pb.authStore.onChange(() => {
  $authed.set(pb.authStore.isValid)
  $authEmail.set((pb.authStore.record?.email as string) ?? '')
})

export async function login(email: string, password: string) {
  await pb.collection('_superusers').authWithPassword(email, password)
}

export function logout() {
  pb.authStore.clear()
}
