import { describe, expect, it } from 'vitest'
import { completions, parsePlaceholders, undefinedRefs } from '@/lib/placeholder'

describe('parsePlaceholders', () => {
  it('提取三种引用', () => {
    const { refs, errors } = parsePlaceholders(
      'a{{cred.my-key_1}}b{{var.ws}}c{{machine.hostname}}',
    )
    expect(errors).toEqual([])
    expect(refs.map((r) => `${r.kind}.${r.name}`)).toEqual([
      'cred.my-key_1',
      'var.ws',
      'machine.hostname',
    ])
  })

  it('去重', () => {
    const { refs } = parsePlaceholders('{{cred.a}}{{cred.a}}')
    expect(refs).toHaveLength(1)
  })

  // 转义规则必须与 Go 侧逐位一致，否则编辑器的告警与 hub 的发布校验会打架
  it('{{{{ 是字面量，不产生引用', () => {
    const { refs, errors } = parsePlaceholders('写作 {{{{cred.x}} 表示字面量')
    expect(refs).toEqual([])
    expect(errors).toEqual([])
  })

  it('报出语法错误', () => {
    expect(parsePlaceholders('{{cred.x').errors).toHaveLength(1)
    expect(parsePlaceholders('{{cred.}}').errors).toHaveLength(1)
    expect(parsePlaceholders('{{unknown.x}}').errors).toHaveLength(1)
    expect(parsePlaceholders('{{machine.secret}}').errors).toHaveLength(1)
  })
})

describe('undefinedRefs', () => {
  it('只报未定义的，machine.* 永远算已定义', () => {
    const got = undefinedRefs(
      '{{cred.known}}{{cred.gone}}{{var.ws}}{{machine.os}}',
      new Set(['cred.known', 'var.ws']),
    )
    expect(got.map((r) => `${r.kind}.${r.name}`)).toEqual(['cred.gone'])
  })
})

describe('completions', () => {
  it('按前缀过滤', () => {
    expect(completions('cred.an', ['cred.anthropic_key', 'cred.other', 'var.ws']))
      .toEqual(['cred.anthropic_key'])
  })

  it('空前缀给出全部候选加内置的 machine.*', () => {
    const got = completions('', ['cred.k'])
    expect(got).toContain('cred.k')
    expect(got).toContain('machine.hostname')
  })
})
