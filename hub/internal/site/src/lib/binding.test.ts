import { describe, expect, it } from 'vitest'
import {
  emptySlots,
  fillAllSlots,
  hasProviderRefs,
  insertEnvSnippet,
  isPassthrough,
} from '@/lib/binding'

describe('模型槽', () => {
  it('选一个模型即四槽同填', () => {
    expect(fillAllSlots('glm-5.1')).toEqual({
      main: 'glm-5.1',
      opus: 'glm-5.1',
      sonnet: 'glm-5.1',
      haiku: 'glm-5.1',
    })
  })

  it('透传模式 = 四槽全空', () => {
    expect(isPassthrough(emptySlots())).toBe(true)
    expect(isPassthrough(fillAllSlots('glm-5.1'))).toBe(false)
    // 半填不是透传——它是配置错误，UI 要拦住。
    expect(isPassthrough({ main: 'a', opus: '', sonnet: '', haiku: '' })).toBe(false)
  })
})

describe('env 片段', () => {
  it('往空 settings.json 里插入六个键的占位符块', () => {
    const out = insertEnvSnippet('{}', 'ANTHROPIC_AUTH_TOKEN')
    const parsed = JSON.parse(out)
    expect(parsed.env.ANTHROPIC_BASE_URL).toBe('{{provider.base_url}}')
    expect(parsed.env.ANTHROPIC_AUTH_TOKEN).toBe('{{provider.auth_token}}')
    expect(parsed.env.ANTHROPIC_MODEL).toBe('{{provider.model}}')
    expect(parsed.env.ANTHROPIC_DEFAULT_OPUS_MODEL).toBe('{{provider.model_opus}}')
    expect(parsed.env.ANTHROPIC_DEFAULT_SONNET_MODEL).toBe('{{provider.model_sonnet}}')
    expect(parsed.env.ANTHROPIC_DEFAULT_HAIKU_MODEL).toBe('{{provider.model_haiku}}')
  })

  it('用 API_KEY 鉴权的平台插的是 ANTHROPIC_API_KEY', () => {
    const parsed = JSON.parse(insertEnvSnippet('{}', 'ANTHROPIC_API_KEY'))
    expect(parsed.env.ANTHROPIC_API_KEY).toBe('{{provider.auth_token}}')
    expect(parsed.env.ANTHROPIC_AUTH_TOKEN).toBeUndefined()
  })

  it('保留 env 之外与 env 之内的既有键', () => {
    const parsed = JSON.parse(
      insertEnvSnippet(
        '{"permissions":{"allow":["Bash"]},"env":{"MY_VAR":"1"}}',
        'ANTHROPIC_AUTH_TOKEN',
      ),
    )
    expect(parsed.permissions.allow).toEqual(['Bash'])
    expect(parsed.env.MY_VAR).toBe('1')
    expect(parsed.env.ANTHROPIC_BASE_URL).toBe('{{provider.base_url}}')
  })

  it('内容不是合法 JSON 时原样返回，绝不写坏用户的文件', () => {
    expect(insertEnvSnippet('{ 这不是 JSON', 'ANTHROPIC_AUTH_TOKEN')).toBe('{ 这不是 JSON')
  })
})

describe('hasProviderRefs', () => {
  it('认得引用', () => {
    expect(hasProviderRefs('{"a":"{{provider.base_url}}"}')).toBe(true)
    expect(hasProviderRefs('{"a":"{{cred.k}}"}')).toBe(false)
    // 转义的不算
    expect(hasProviderRefs('写法是 {{{{provider.base_url}}')).toBe(false)
  })
})
