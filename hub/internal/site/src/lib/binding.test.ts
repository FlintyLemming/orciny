import { describe, expect, it } from 'vitest'
import {
  emptySlots,
  fillAllSlots,
  hasOneM,
  hasProviderRefs,
  insertEnvSnippet,
  isPassthrough,
  isSlotsSplit,
  setOneM,
  setSlotsOneM,
  stripOneM,
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
    expect(parsed.env.ANTHROPIC_BASE_URL).toBe('{{provider.claude.base_url}}')
    expect(parsed.env.ANTHROPIC_AUTH_TOKEN).toBe('{{provider.claude.auth_token}}')
    expect(parsed.env.ANTHROPIC_MODEL).toBe('{{provider.claude.model}}')
    expect(parsed.env.ANTHROPIC_DEFAULT_OPUS_MODEL).toBe('{{provider.claude.model_opus}}')
    expect(parsed.env.ANTHROPIC_DEFAULT_SONNET_MODEL).toBe('{{provider.claude.model_sonnet}}')
    expect(parsed.env.ANTHROPIC_DEFAULT_HAIKU_MODEL).toBe('{{provider.claude.model_haiku}}')
  })

  it('用 API_KEY 鉴权的平台插的是 ANTHROPIC_API_KEY', () => {
    const parsed = JSON.parse(insertEnvSnippet('{}', 'ANTHROPIC_API_KEY'))
    expect(parsed.env.ANTHROPIC_API_KEY).toBe('{{provider.claude.auth_token}}')
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
    expect(parsed.env.ANTHROPIC_BASE_URL).toBe('{{provider.claude.base_url}}')
  })

  it('内容不是合法 JSON 时原样返回，绝不写坏用户的文件', () => {
    expect(insertEnvSnippet('{ 这不是 JSON', 'ANTHROPIC_AUTH_TOKEN')).toBe('{ 这不是 JSON')
  })
})

describe('hasProviderRefs', () => {
  it('认得引用', () => {
    expect(hasProviderRefs('{"a":"{{provider.claude.base_url}}"}')).toBe(true)
    expect(hasProviderRefs('{"a":"{{provider.openai.api_key}}"}')).toBe(true)
    expect(hasProviderRefs('{"a":"{{provider.base_url}}"}')).toBe(false)
    expect(hasProviderRefs('{"a":"{{cred.k}}"}')).toBe(false)
    // 转义的不算
    expect(hasProviderRefs('写法是 {{{{provider.claude.base_url}}')).toBe(false)
  })
})

describe('1M 上下文声明', () => {
  it('识别规则与 Claude Code 的 /[1m]/i 对齐', () => {
    expect(hasOneM('glm-5.2[1m]')).toBe(true)
    expect(hasOneM('glm-5.2[1M]')).toBe(true)
    expect(hasOneM('glm-5.2 [1m]  ')).toBe(true)
    expect(hasOneM('glm-5.2')).toBe(false)
    expect(hasOneM('glm-5.2[1m]-turbo')).toBe(false)
  })

  it('strip 拿到基名，set 写回小写形式', () => {
    expect(stripOneM('glm-5.2 [1M]  ')).toBe('glm-5.2')
    expect(stripOneM('glm-5.2')).toBe('glm-5.2')
    expect(setOneM('glm-5.2', true)).toBe('glm-5.2[1m]')
    expect(setOneM('glm-5.2[1M]', true)).toBe('glm-5.2[1m]')
    expect(setOneM('glm-5.2[1m]', false)).toBe('glm-5.2')
    expect(setOneM('', true)).toBe('')
  })

  it('只给基名相同的槽换声明，其余槽原样保留', () => {
    // 智谱预设：haiku 故意指向便宜的 glm-4.7，不该被 1M 开关波及。
    const slots = {
      main: 'glm-5.2[1m]',
      opus: 'glm-5.2[1m]',
      sonnet: 'glm-5.2[1m]',
      haiku: 'glm-4.7',
    }
    expect(setSlotsOneM(slots, 'glm-5.2', false)).toEqual({
      main: 'glm-5.2',
      opus: 'glm-5.2',
      sonnet: 'glm-5.2',
      haiku: 'glm-4.7',
    })
    expect(setSlotsOneM(setSlotsOneM(slots, 'glm-5.2', false), 'glm-5.2', true)).toEqual(slots)
  })

  it('空槽不会被塞进声明', () => {
    expect(setSlotsOneM(emptySlots(), '', true)).toEqual(emptySlots())
  })
})

describe('isSlotsSplit', () => {
  it('四槽同填或全空不算分设', () => {
    expect(isSlotsSplit(emptySlots())).toBe(false)
    expect(isSlotsSplit(fillAllSlots('glm-5.2'))).toBe(false)
    expect(isSlotsSplit(fillAllSlots('glm-5.2[1m]'))).toBe(false)
  })

  it('四槽基名不同算分设', () => {
    expect(
      isSlotsSplit({
        main: 'glm-5.2[1m]',
        opus: 'glm-5.2[1m]',
        sonnet: 'glm-5.2[1m]',
        haiku: 'glm-4.7',
      }),
    ).toBe(true)
  })
})

