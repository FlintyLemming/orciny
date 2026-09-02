/**
 * 差异点的前端切分。
 *
 * 与 Go 侧 hub/internal/overrides/split.go 是**同一套规则的两份实现**：
 * 数组整体当叶子、键按字典序、空串 = 该键在这一侧不存在、
 * selector 逐段转义。OverrideDialog 要在建覆盖层之前把差异点列出来给
 * 用户勾，而 API 里没有「预览差异点」这条（M1.8 spec §7），只能自己算。
 *
 * 两份实现给出的 selector 必须逐字符一致——不一致就会写到错误的位置。
 */

export interface OverridePoint {
  selector: string
  /** 原始 JSON 片段；空串 = 该键在基线侧不存在 */
  base: string
  /** 原始 JSON 片段；空串 = 该键在本机侧不存在 */
  mine: string
}

/**
 * 转义一个键段，使它能安全地拼进 gjson / sjson 的路径。
 * 路径语法里 . 是分隔符、* ? 是通配符、\ 是转义符。
 */
export function escapeSeg(s: string): string {
  let out = ''
  for (const ch of s) {
    if (ch === '\\' || ch === '.' || ch === '*' || ch === '?') out += '\\'
    out += ch
  }
  return out
}

type JSONObject = Record<string, unknown>

function asObject(text: string): JSONObject | null {
  try {
    const v: unknown = JSON.parse(text)
    if (v === null || typeof v !== 'object' || Array.isArray(v)) return null
    return v as JSONObject
  } catch {
    return null
  }
}

function isPlainObject(v: unknown): v is JSONObject {
  return v !== null && typeof v === 'object' && !Array.isArray(v)
}

/** 规范化形式，只用于比较：重新缩进 / 键序不同不该算差异。 */
function canon(v: unknown): string {
  if (isPlainObject(v)) {
    const keys = Object.keys(v).sort()
    return `{${keys.map((k) => `${JSON.stringify(k)}:${canon(v[k])}`).join(',')}}`
  }
  if (Array.isArray(v)) return `[${v.map(canon).join(',')}]`
  return JSON.stringify(v) ?? 'null'
}

/**
 * 切出差异点。两侧不都是 JSON 对象时返回 null —— 那种文件走文本一路，
 * 勾选的是 unified diff 的 @@ 块而不是 selector。
 */
export function splitPoints(base: string, mine: string): OverridePoint[] | null {
  const b = asObject(base)
  const m = asObject(mine)
  if (!b || !m) return null
  const out: OverridePoint[] = []
  walk('', b, m, out)
  return out
}

function walk(prefix: string, base: JSONObject, mine: JSONObject, out: OverridePoint[]) {
  const keys = [...new Set([...Object.keys(base), ...Object.keys(mine)])].sort()
  for (const k of keys) {
    const sel = prefix ? `${prefix}.${escapeSeg(k)}` : escapeSeg(k)
    const hasB = Object.hasOwn(base, k)
    const hasM = Object.hasOwn(mine, k)
    const bv = base[k]
    const mv = mine[k]

    if (hasB && hasM) {
      if (isPlainObject(bv) && isPlainObject(mv)) {
        walk(sel, bv, mv, out)
        continue
      }
      if (canon(bv) === canon(mv)) continue
    }
    out.push({
      selector: sel,
      base: hasB ? JSON.stringify(bv) : '',
      mine: hasM ? JSON.stringify(mv) : '',
    })
  }
}

/**
 * 数 unified diff 里的 @@ 块。块的序号就是后端 overrides.Hunks 的下标
 * ——两侧都用 go-difflib 的 3 行上下文分组（spec §4.2）。
 */
export function diffHunkCount(diff: string): number {
  if (!diff) return 0
  return diff.split('\n').filter((l) => l.startsWith('@@')).length
}
