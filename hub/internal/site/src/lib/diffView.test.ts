import { describe, expect, it } from 'vitest'
import { groupChanges, inlineDiff } from '@/lib/diffView'

describe('groupChanges', () => {
  it('按类型分组，组内按路径排序', () => {
    const groups = groupChanges([
      { path: 'b', kind: 'modified', from_hash: '1', to_hash: '2' },
      { path: 'a', kind: 'added', from_hash: '', to_hash: '3' },
      { path: 'c', kind: 'modified', from_hash: '4', to_hash: '5' },
      { path: 'd', kind: 'removed', from_hash: '6', to_hash: '' },
    ])
    expect(groups.map((g) => g.kind)).toEqual(['added', 'modified', 'removed'])
    expect(groups[1].files.map((f) => f.path)).toEqual(['b', 'c'])
  })

  it('空组不出现', () => {
    expect(groupChanges([{ path: 'a', kind: 'added', from_hash: '', to_hash: '1' }]))
      .toHaveLength(1)
  })
})

describe('inlineDiff', () => {
  it('给出增删与上下文行', () => {
    const rows = inlineDiff('第一行\n第二行\n', '第一行\n改过的第二行\n')
    expect(rows.some((r) => r.type === 'del' && r.text.includes('第二行'))).toBe(true)
    expect(rows.some((r) => r.type === 'add' && r.text.includes('改过的'))).toBe(true)
    expect(rows.some((r) => r.type === 'ctx' && r.text.includes('第一行'))).toBe(true)
  })

  it('相同内容全是上下文行', () => {
    expect(inlineDiff('一样\n', '一样\n').every((r) => r.type === 'ctx')).toBe(true)
  })
})
