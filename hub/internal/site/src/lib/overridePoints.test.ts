import { describe, expect, it } from 'vitest'
import { diffHunkCount, escapeSeg, splitPoints } from '@/lib/overridePoints'

describe('escapeSeg', () => {
  it('转义 gjson 路径里的四个元字符', () => {
    // 与 Go 侧 overrides.EscapeSeg 逐字符一致，否则覆盖层会写到错误的位置
    expect(escapeSeg('a.b')).toBe('a\\.b')
    expect(escapeSeg('c*d')).toBe('c\\*d')
    expect(escapeSeg('e?f')).toBe('e\\?f')
    expect(escapeSeg('g\\h')).toBe('g\\\\h')
    expect(escapeSeg('普通键')).toBe('普通键')
  })
})

describe('splitPoints', () => {
  it('逐键下降进对象', () => {
    expect(splitPoints('{"env":{"A":"1","B":"2"}}', '{"env":{"A":"9","B":"2"}}')).toEqual([
      { selector: 'env.A', base: '"1"', mine: '"9"' },
    ])
  })

  it('数组整体当叶子', () => {
    const pts = splitPoints('{"allow":["a"]}', '{"allow":["a","b"]}')
    expect(pts).toHaveLength(1)
    expect(pts![0].selector).toBe('allow')
  })

  it('一侧缺键时该侧为空串', () => {
    expect(splitPoints('{"a":1}', '{"a":1,"b":2}')).toEqual([
      { selector: 'b', base: '', mine: '2' },
    ])
    expect(splitPoints('{"a":1,"b":2}', '{"a":1}')).toEqual([
      { selector: 'b', base: '2', mine: '' },
    ])
  })

  it('重新缩进不算差异点', () => {
    expect(splitPoints('{\n  "a": {\n    "b": 1\n  }\n}', '{"a":{"b":1}}')).toEqual([])
  })

  it('selector 按字典序稳定排列', () => {
    const pts = splitPoints('{}', '{"z":1,"a":1,"m":1}')
    expect(pts!.map((p) => p.selector)).toEqual(['a', 'm', 'z'])
  })

  it('转义带元字符的键', () => {
    const pts = splitPoints('{"hooks":{"a.b":1}}', '{"hooks":{"a.b":2}}')
    expect(pts![0].selector).toBe('hooks.a\\.b')
  })

  it('两侧不都是 JSON 对象时返回 null —— 走文本一路', () => {
    expect(splitPoints('# 标题', '# 新标题')).toBeNull()
    expect(splitPoints('[1]', '[1,2]')).toBeNull()
    expect(splitPoints('{"a":1}', '不是 JSON')).toBeNull()
  })
})

describe('diffHunkCount', () => {
  it('数 @@ 块 —— 下标与后端的 hunk 一一对应', () => {
    const diff = [
      '--- f（基线）',
      '+++ f（本机）',
      '@@ -1,4 +1,4 @@',
      '-a',
      '+A',
      '@@ -13,4 +13,4 @@',
      '-z',
      '+Z',
      '',
    ].join('\n')
    expect(diffHunkCount(diff)).toBe(2)
  })

  it('空 diff 是 0 块', () => {
    expect(diffHunkCount('')).toBe(0)
  })
})
