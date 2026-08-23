import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { ProviderRow } from '@/pages/Providers'
import type { ProviderRecord } from '@/types/collections'

i18n.load('en', {})
i18n.activate('en')

const base: ProviderRecord = {
  id: 'p1',
  name: '智谱 GLM',
  preset: 'zhipu',
  note: '',
  key_last4: 'a1b2',
  claude: {
    base_url: 'https://open.bigmodel.cn/api/anthropic',
    auth_field: 'ANTHROPIC_AUTH_TOKEN',
    key_last4: 'a1b2',
    models: ['glm-5.2', 'glm-4.7', 'glm-5-turbo', 'glm-5.2[1m]'],
    defaults: null,
  },
  openai: null,
  created: '',
  updated: '',
}

function row(p: ProviderRecord, n = 2) {
  return render(
    <I18nProvider i18n={i18n}>
      <ProviderRow provider={p} boundCount={n} onEdit={vi.fn()} onDelete={vi.fn()} />
    </I18nProvider>,
  )
}

describe('ProviderRow', () => {
  it('两个端点各一行，claude 行显示鉴权字段、末四位与模型数', () => {
    row(base)
    expect(screen.getByText('https://open.bigmodel.cn/api/anthropic')).toBeTruthy()
    expect(screen.getByText(/ANTHROPIC_AUTH_TOKEN/)).toBeTruthy()
    expect(screen.getByText(/····a1b2/)).toBeTruthy()
    expect(screen.getByText(/4 个模型/)).toBeTruthy()
  })

  /**
   * 未配置的端点显示为置灰的「未配置」行，而不是整行不显示——
   * 用户要能一眼看出「这条还有另一个口没填」（M1.6 spec §5.1）。
   */
  it('未配置的端点显示成置灰的「未配置」行', () => {
    row(base)
    const openai = screen.getByTestId('endpoint-openai')
    expect(openai).toHaveTextContent('OpenAI')
    expect(openai).toHaveTextContent('未配置')
  })

  it('两个端点都配了就各显示各的', () => {
    row({
      ...base,
      openai: {
        base_url: 'https://open.bigmodel.cn/api/paas/v4',
        auth_field: 'OPENAI_API_KEY',
        key_last4: 'a1b2',
        models: ['glm-5.2'],
        default_model: 'glm-5.2',
      },
    })
    const openai = screen.getByTestId('endpoint-openai')
    expect(openai).toHaveTextContent('https://open.bigmodel.cn/api/paas/v4')
    expect(openai).toHaveTextContent('OPENAI_API_KEY')
    expect(openai).not.toHaveTextContent('未配置')
  })

  it('显示引用数', () => {
    row(base, 3)
    expect(screen.getByText(/被 3 个配置集引用/)).toBeTruthy()
  })
})
