import { describe, expect, it } from 'vitest'
import {
  completions,
  endpointOf,
  parsePlaceholders,
  PROVIDER_KEYS,
  undefinedRefs,
} from '@/lib/placeholder'

describe('parsePlaceholders', () => {
  it('提取两种引用', () => {
    const { refs, errors } = parsePlaceholders('a{{var.my-key_1}}b{{var.ws}}c{{machine.hostname}}')
    expect(errors).toEqual([])
    expect(refs.map((r) => `${r.kind}.${r.name}`)).toEqual([
      'var.my-key_1',
      'var.ws',
      'machine.hostname',
    ])
  })

  it('去重', () => {
    const { refs } = parsePlaceholders('{{var.a}}{{var.a}}')
    expect(refs).toHaveLength(1)
  })

  // 转义规则必须与 Go 侧逐位一致，否则编辑器的告警与 hub 的发布校验会打架
  it('{{{{ 是字面量，不产生引用', () => {
    const { refs, errors } = parsePlaceholders('写作 {{{{var.x}} 表示字面量')
    expect(refs).toEqual([])
    expect(errors).toEqual([])
  })

  it('报出语法错误', () => {
    expect(parsePlaceholders('{{var.x').errors).toHaveLength(1)
    expect(parsePlaceholders('{{var.}}').errors).toHaveLength(1)
    expect(parsePlaceholders('{{unknown.x}}').errors).toHaveLength(1)
    expect(parsePlaceholders('{{machine.secret}}').errors).toHaveLength(1)
  })
})

describe('undefinedRefs', () => {
  it('只报未定义的，machine.* 永远算已定义', () => {
    const got = undefinedRefs('{{var.known}}{{var.gone}}{{machine.os}}', new Set(['var.known']))
    expect(got.map((r) => `${r.kind}.${r.name}`)).toEqual(['var.gone'])
  })
})

describe('completions', () => {
  it('按前缀过滤', () => {
    expect(completions('var.a', ['var.anthropic_key', 'var.other', 'var.ws'])).toEqual([
      'var.anthropic_key',
    ])
  })

  it('空前缀给出全部候选加内置的 machine.*', () => {
    const got = completions('', ['var.k'])
    expect(got).toContain('var.k')
    expect(got).toContain('machine.hostname')
  })
})

describe('端点限定的 provider 词法', () => {
  it('九个内置名与 Go 侧逐字符一致、顺序相同', () => {
    expect([...PROVIDER_KEYS]).toEqual([
      'claude.base_url',
      'claude.auth_token',
      'claude.model',
      'claude.model_opus',
      'claude.model_sonnet',
      'claude.model_haiku',
      'openai.base_url',
      'openai.api_key',
      'openai.model',
    ])
  })

  it('九个名字全部 parse 通过', () => {
    for (const k of PROVIDER_KEYS) {
      const { refs, errors } = parsePlaceholders(`{{provider.${k}}}`)
      expect(errors).toEqual([])
      expect(refs).toEqual([{ kind: 'provider', name: k }])
    }
  })

  it('旧写法报「不是内置名」', () => {
    const { errors } = parsePlaceholders('{{provider.base_url}}')
    expect(errors.join()).toContain('不是内置名')
  })

  it('cred 前缀报「未知前缀」', () => {
    const { refs, errors } = parsePlaceholders('{{cred.zhipu}}')
    expect(refs).toEqual([])
    expect(errors.join()).toContain('未知前缀')
  })

  it('端点对但字段不对也报错', () => {
    expect(parsePlaceholders('{{provider.claude.temperature}}').errors).toHaveLength(1)
  })

  it('var 的名字仍然做字符集校验', () => {
    expect(parsePlaceholders('{{var.a.b}}').errors.join()).toContain('名字非法')
  })

  it('endpointOf 只认白名单里的名字', () => {
    expect(endpointOf('claude.base_url')).toBe('claude')
    expect(endpointOf('openai.api_key')).toBe('openai')
    expect(endpointOf('base_url')).toBe('')
    expect(endpointOf('gemini.base_url')).toBe('')
  })

  it('provider.* 不参与未定义引用判定', () => {
    const text = '{{provider.openai.api_key}} {{var.workspace}}'
    expect(undefinedRefs(text, new Set(['var.workspace']))).toEqual([])
    expect(undefinedRefs(text, new Set())).toEqual([{ kind: 'var', name: 'workspace' }])
  })

  it('补全候选里是端点限定名', () => {
    expect(completions('provider.openai.', [])).toEqual([
      'provider.openai.api_key',
      'provider.openai.base_url',
      'provider.openai.model',
    ])
  })

  it('转义的 {{{{provider.x}} 不算引用', () => {
    expect(parsePlaceholders('写法是 {{{{provider.claude.base_url}}').refs).toEqual([])
  })
})
