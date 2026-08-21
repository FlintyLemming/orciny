import { describe, expect, it } from 'vitest'
import {
  completions,
  parsePlaceholders,
  PROVIDER_KEYS,
  undefinedRefs,
} from '@/lib/placeholder'

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

describe('provider 占位符', () => {
  it('认得六个内置名', () => {
    const { refs, errors } = parsePlaceholders(
      '{{provider.base_url}} {{provider.auth_token}} {{provider.model}} ' +
        '{{provider.model_opus}} {{provider.model_sonnet}} {{provider.model_haiku}}',
    )
    expect(errors).toEqual([])
    expect(refs.map((r) => `${r.kind}.${r.name}`)).toEqual([
      'provider.base_url',
      'provider.auth_token',
      'provider.model',
      'provider.model_opus',
      'provider.model_sonnet',
      'provider.model_haiku',
    ])
  })

  it('白名单外的名字报错，与 Go 侧对称', () => {
    const { refs, errors } = parsePlaceholders('{{provider.temperature}}')
    expect(refs).toEqual([])
    expect(errors[0]).toContain('provider.temperature')
  })

  it('PROVIDER_KEYS 与 Go 侧 protocol.ProviderKeys 逐字符一致', () => {
    expect(PROVIDER_KEYS).toEqual([
      'base_url',
      'auth_token',
      'model',
      'model_opus',
      'model_sonnet',
      'model_haiku',
    ])
  })

  it('provider.* 永远算已定义——它由三条绑定校验判定', () => {
    expect(undefinedRefs('{{provider.base_url}}{{cred.gone}}', new Set())).toEqual([
      { kind: 'cred', name: 'gone' },
    ])
  })

  it('补全列表带上六个 provider 内置名', () => {
    expect(completions('provider.', [])).toEqual([
      'provider.auth_token',
      'provider.base_url',
      'provider.model',
      'provider.model_haiku',
      'provider.model_opus',
      'provider.model_sonnet',
    ])
  })

  it('转义的 {{{{provider.x}} 不算引用', () => {
    expect(parsePlaceholders('写法是 {{{{provider.base_url}}').refs).toEqual([])
  })
})
