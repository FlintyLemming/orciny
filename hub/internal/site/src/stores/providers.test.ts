import { describe, expect, it } from 'vitest'
import { sanitizeProvider } from '@/stores/providers'
import type { ProviderRecord } from '@/types/collections'

describe('sanitizeProvider', () => {
  it('保留两个端点与末四位', () => {
    const raw = {
      id: 'p1',
      name: '智谱 GLM',
      preset: 'zhipu',
      note: '',
      key_last4: '3456',
      claude: {
        base_url: 'https://open.bigmodel.cn/api/anthropic',
        auth_field: 'ANTHROPIC_AUTH_TOKEN',
        key_last4: '3456',
        models: ['glm-5.2'],
        defaults: { main: 'glm-5.2', opus: 'glm-5.2', sonnet: 'glm-5.2', haiku: 'glm-4.7' },
      },
      claude_key_cipher: '不该出现在这里',
      created: '',
      updated: '',
    } as unknown as ProviderRecord

    const got = sanitizeProvider(raw)
    expect(got.claude?.base_url).toBe('https://open.bigmodel.cn/api/anthropic')
    expect(got.claude?.key_last4).toBe('3456')
    expect(got.key_last4).toBe('3456')
  })

  /**
   * 密文是顶层 Hidden 字段，PocketBase 不会下发它。
   * 这条测试是双保险：万一哪天有人把 Hidden 摘了，白名单式的 sanitize
   * 仍然不会把它放进 store。
   */
  it('任何 cipher 字段都不进 store', () => {
    const raw = {
      id: 'p1',
      name: 'x',
      preset: '',
      note: '',
      key_last4: '',
      key_cipher: 'leak',
      claude_key_cipher: 'leak',
      openai_key_cipher: 'leak',
      claude: null,
      openai: null,
      created: '',
      updated: '',
    } as unknown as ProviderRecord

    expect(JSON.stringify(sanitizeProvider(raw))).not.toContain('leak')
  })

  it('端点为 null 时不炸', () => {
    const raw = {
      id: 'p1',
      name: 'x',
      preset: '',
      note: '',
      key_last4: '',
      claude: null,
      openai: null,
      created: '',
      updated: '',
    } as ProviderRecord
    const got = sanitizeProvider(raw)
    expect(got.claude).toBeNull()
    expect(got.openai).toBeNull()
  })
})
