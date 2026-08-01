/**
 * 占位符的前端词法。
 *
 * 与 Go 侧 `protocol/placeholder.go` 是同一套规则的两份实现——这是有意为之
 * 的重复：编辑器要在**内容还没提交到 hub** 的时候就给出告警与补全，
 * 而那时没有任何后端可以问。两份实现的一致性靠双方各自的测试用同一组
 * 例子来保证（转义、非法名、machine 白名单）。
 */

export type RefKind = 'cred' | 'var' | 'machine'

export interface Ref {
  kind: RefKind
  name: string
}

export interface ParseResult {
  refs: Ref[]
  errors: string[]
}

/** {{machine.*}} 允许的全部名字，与 Go 侧 protocol.MachineKeys 一致。 */
export const MACHINE_KEYS = ['name', 'hostname', 'os', 'arch'] as const

const NAME_RE = /^[A-Za-z0-9_-]+$/

export function parsePlaceholders(text: string): ParseResult {
  const refs: Ref[] = []
  const errors: string[] = []
  const seen = new Set<string>()

  let i = 0
  while (i < text.length) {
    if (!text.startsWith('{{', i)) {
      i++
      continue
    }
    if (text.startsWith('{{{{', i)) {
      i += 4 // 字面的 "{{"
      continue
    }
    const end = text.indexOf('}}', i + 2)
    if (end < 0) {
      errors.push(`偏移 ${i} 处有未闭合的 {{`)
      break
    }
    const body = text.slice(i + 2, end)
    const dot = body.indexOf('.')
    const prefix = dot < 0 ? '' : body.slice(0, dot)
    const name = dot < 0 ? '' : body.slice(dot + 1)

    if (dot < 0) {
      errors.push(`{{${body}}} 缺少 . 分隔`)
    } else if (!NAME_RE.test(name)) {
      errors.push(`{{${body}}} 的名字非法（只允许 [A-Za-z0-9_-]）`)
    } else if (prefix === 'cred' || prefix === 'var') {
      push(refs, seen, { kind: prefix, name })
    } else if (prefix === 'machine') {
      if ((MACHINE_KEYS as readonly string[]).includes(name)) {
        push(refs, seen, { kind: 'machine', name })
      } else {
        errors.push(`machine.${name} 不是内置名（只有 ${MACHINE_KEYS.join(' / ')}）`)
      }
    } else {
      errors.push(`未知前缀 ${prefix}`)
    }
    i = end + 2
  }
  return { refs, errors }
}

function push(refs: Ref[], seen: Set<string>, r: Ref) {
  const key = `${r.kind}.${r.name}`
  if (seen.has(key)) return
  seen.add(key)
  refs.push(r)
}

/** known 的元素形如 "cred.foo" / "var.bar"。machine.* 永远算已定义。 */
export function undefinedRefs(text: string, known: Set<string>): Ref[] {
  return parsePlaceholders(text).refs.filter(
    (r) => r.kind !== 'machine' && !known.has(`${r.kind}.${r.name}`),
  )
}

/** 补全候选：已有的凭据与变量，加上内置的 machine.*。 */
export function completions(prefix: string, known: string[]): string[] {
  const all = [...known, ...MACHINE_KEYS.map((k) => `machine.${k}`)]
  return all.filter((c) => c.startsWith(prefix)).sort()
}
