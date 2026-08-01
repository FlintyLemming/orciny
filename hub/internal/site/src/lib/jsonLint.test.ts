import { describe, expect, it } from 'vitest'
import { lintJson } from '@/lib/jsonLint'

describe('lintJson', () => {
  it('合法 JSON 无问题', () => {
    expect(lintJson('{"a": 1}')).toEqual([])
  })

  it('空内容不算错', () => {
    expect(lintJson('')).toEqual([])
    expect(lintJson('   \n ')).toEqual([])
  })

  // 定位是这个函数存在的理由：只说「JSON 错误」等于没说
  it('给出行列', () => {
    const problems = lintJson('{\n  "a": 1,\n  "b":\n}')
    expect(problems).toHaveLength(1)
    expect(problems[0].line).toBeGreaterThanOrEqual(3)
    expect(problems[0].message).not.toEqual('')
  })

  // 含占位符的 JSON 在编辑器里是常态，不能因此报错
  it('占位符先替成字符串再校验', () => {
    expect(lintJson('{"key": "{{cred.k}}"}')).toEqual([])
    expect(lintJson('{"n": {{var.count}}}')).toEqual([])
  })
})
