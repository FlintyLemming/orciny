/**
 * 三方对比：基线 / 机器 A / 机器 B。
 *
 * 两台机器改了同一文件时 UI 硬阻止收编，先让用户看清三边再选一侧。
 * 用 jsdiff 分别算 base→left 与 base→right，再按 base 行对齐成三列表。
 */

import { diffArrays } from 'diff'

export interface Side {
  label: string
  content: string
}

export interface ThreeWayRow {
  base: string | null
  left: string | null
  right: string | null
  /** 三边是否一致 */
  same: boolean
  /** left 与 right 是否互相冲突（都改了同一行且改法不同） */
  conflicting: boolean
}

interface Pair {
  base: string | null
  side: string | null
}

function splitLines(s: string): string[] {
  if (s === '') return []
  return s.replace(/\n$/, '').split('\n')
}

/**
 * base→side 的 diff 收成 (base, side) 对。
 * 相邻的 remove+add 配对成「替换」，便于三方同行展示。
 */
function sidePairs(base: string[], side: string[]): Pair[] {
  const parts = diffArrays(base, side)
  const pairs: Pair[] = []
  let i = 0
  while (i < parts.length) {
    const p = parts[i]
    const next = parts[i + 1]
    if (p.removed && next?.added) {
      const dels = p.value
      const adds = next.value
      const n = Math.max(dels.length, adds.length)
      for (let k = 0; k < n; k++) {
        pairs.push({
          base: k < dels.length ? dels[k] : null,
          side: k < adds.length ? adds[k] : null,
        })
      }
      i += 2
      continue
    }
    if (p.added) {
      for (const v of p.value) pairs.push({ base: null, side: v })
      i++
      continue
    }
    if (p.removed) {
      for (const v of p.value) pairs.push({ base: v, side: null })
      i++
      continue
    }
    for (const v of p.value) pairs.push({ base: v, side: v })
    i++
  }
  return pairs
}

function makeRow(base: string | null, left: string | null, right: string | null): ThreeWayRow {
  const same = base === left && left === right
  // 两边都相对 base 做了改动，且改法不同 → 冲突
  const leftChanged = left !== base
  const rightChanged = right !== base
  const conflicting = leftChanged && rightChanged && left !== right
  return { base, left, right, same, conflicting }
}

/** 把左右两侧相对 base 的 pair 流对齐成三列行。 */
function mergePairs(left: Pair[], right: Pair[]): ThreeWayRow[] {
  const rows: ThreeWayRow[] = []
  let i = 0
  let j = 0
  while (i < left.length || j < right.length) {
    const L = i < left.length ? left[i] : null
    const R = j < right.length ? right[j] : null

    // 同 base 行（含两侧都删：side 为 null）
    if (L && R && L.base !== null && R.base !== null && L.base === R.base) {
      rows.push(makeRow(L.base, L.side, R.side))
      i++
      j++
      continue
    }

    // 两侧同时在插入：并到一行以便标冲突
    if (L && L.base === null && R && R.base === null) {
      rows.push(makeRow(null, L.side, R.side))
      i++
      j++
      continue
    }

    if (L && L.base === null) {
      rows.push(makeRow(null, L.side, null))
      i++
      continue
    }

    if (R && R.base === null) {
      rows.push(makeRow(null, null, R.side))
      j++
      continue
    }

    // 一侧还有 base 行、另一侧已耗尽（不应常见）
    if (L) {
      rows.push(makeRow(L.base, L.side, L.base))
      i++
      continue
    }
    if (R) {
      rows.push(makeRow(R.base, R.base, R.side))
      j++
    }
  }
  return rows
}

export function threeWayRows(base: string, left: string, right: string): ThreeWayRow[] {
  const b = splitLines(base)
  const l = splitLines(left)
  const r = splitLines(right)
  return mergePairs(sidePairs(b, l), sidePairs(b, r))
}
