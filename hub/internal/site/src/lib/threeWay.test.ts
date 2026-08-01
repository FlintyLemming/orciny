import { describe, expect, it } from 'vitest'
import { threeWayRows } from '@/lib/threeWay'

describe('threeWayRows', () => {
  it('三边一致的行标记为 same', () => {
    const rows = threeWayRows('一样\n', '一样\n', '一样\n')
    expect(rows).toHaveLength(1)
    expect(rows[0].same).toBe(true)
    expect(rows[0].conflicting).toBe(false)
  })

  it('只有一边改了不算冲突', () => {
    const rows = threeWayRows('原文\n', '左边改了\n', '原文\n')
    expect(rows[0].same).toBe(false)
    expect(rows[0].conflicting).toBe(false)
  })

  it('两边都改且改法不同才算冲突', () => {
    const rows = threeWayRows('原文\n', 'A 的改法\n', 'B 的改法\n')
    expect(rows[0].conflicting).toBe(true)
  })

  it('两边改成一样的不算冲突', () => {
    const rows = threeWayRows('原文\n', '同样的改法\n', '同样的改法\n')
    expect(rows[0].conflicting).toBe(false)
    expect(rows[0].same).toBe(false)
  })

  it('新增行：base 为 null', () => {
    const rows = threeWayRows('', '新增\n', '')
    expect(rows[0].base).toBeNull()
    expect(rows[0].left).toBe('新增')
  })

  it('删除行：对应侧为 null', () => {
    const rows = threeWayRows('要删的\n', '', '要删的\n')
    expect(rows[0].left).toBeNull()
    expect(rows[0].right).toBe('要删的')
  })
})
