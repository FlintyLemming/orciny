import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { ExtractDialog } from '@/components/ExtractDialog'
import type { ProviderRecord } from '@/types/collections'

i18n.load('en', {})
i18n.activate('en')

const zhipu: ProviderRecord = {
  id: 'p1',
  name: '智谱 GLM',
  preset: 'zhipu',
  note: '',
  key_last4: '',
  claude: {
    base_url: 'https://open.bigmodel.cn/api/anthropic',
    auth_field: 'ANTHROPIC_AUTH_TOKEN',
    models: [],
    defaults: null,
  },
  openai: null,
  created: '',
  updated: '',
}
const other: ProviderRecord = { ...zhipu, id: 'p2', name: '另一家' }

function wrap(ui: React.ReactNode) {
  return <I18nProvider i18n={i18n}>{ui}</I18nProvider>
}

describe('ExtractDialog', () => {
  it('反查命中时预选那条 provider', () => {
    render(
      wrap(
        <ExtractDialog
          providers={[other, zhipu]}
          suggestedProviderId="p1"
          masked="sk-z…3456"
          onSubmit={vi.fn()}
          onClose={vi.fn()}
        />,
      ),
    )
    expect(screen.getByLabelText('服务配置')).toHaveValue('p1')
  })

  it('反查不中时退回列表里的第一条，让用户自己选', () => {
    render(
      wrap(
        <ExtractDialog
          providers={[other, zhipu]}
          masked="sk-z…3456"
          onSubmit={vi.fn()}
          onClose={vi.fn()}
        />,
      ),
    )
    expect(screen.getByLabelText('服务配置')).toHaveValue('p2')
  })

  it('默认抽到 claude 端点，可以改成 openai', async () => {
    const onSubmit = vi.fn()
    render(
      wrap(
        <ExtractDialog
          providers={[zhipu]}
          masked="sk-z…3456"
          onSubmit={onSubmit}
          onClose={vi.fn()}
        />,
      ),
    )
    expect(screen.getByLabelText('端点')).toHaveValue('claude')

    await userEvent.selectOptions(screen.getByLabelText('端点'), 'openai')
    await userEvent.click(screen.getByText('抽取'))
    expect(onSubmit).toHaveBeenCalledWith('p1', 'openai')
  })

  it('一条 provider 都没有时禁止提交并给出去处', () => {
    render(
      wrap(
        <ExtractDialog providers={[]} masked="sk-z…3456" onSubmit={vi.fn()} onClose={vi.fn()} />,
      ),
    )
    expect(screen.getByText('抽取')).toBeDisabled()
    expect(screen.getByText(/先到「AI 服务」页建一条/)).toBeTruthy()
  })

  it('显示被抽取值的掩码，不显示明文', () => {
    render(
      wrap(
        <ExtractDialog
          providers={[zhipu]}
          masked="sk-z…3456"
          onSubmit={vi.fn()}
          onClose={vi.fn()}
        />,
      ),
    )
    expect(screen.getByText('sk-z…3456')).toBeTruthy()
  })
})
