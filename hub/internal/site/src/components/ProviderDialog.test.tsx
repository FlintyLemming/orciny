import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { ProviderDialog } from '@/components/ProviderDialog'
import type { ProviderPreset, ProviderRecord } from '@/types/collections'

i18n.load('en', {})
i18n.activate('en')

function wrap(ui: React.ReactNode) {
  return <I18nProvider i18n={i18n}>{ui}</I18nProvider>
}

const presets: ProviderPreset[] = [
  {
    id: 'zhipu',
    name: 'Zhipu GLM',
    base_url: 'https://open.bigmodel.cn/api/anthropic',
    auth_field: 'ANTHROPIC_AUTH_TOKEN',
    models: ['glm-5.1', 'glm-4.7'],
    defaults: { main: 'glm-5.1', opus: 'glm-5.1', sonnet: 'glm-5.1', haiku: 'glm-5.1' },
  },
  {
    id: 'anthropic',
    name: 'Anthropic Official',
    base_url: 'https://api.anthropic.com',
    auth_field: 'ANTHROPIC_API_KEY',
    models: ['claude-opus-5'],
    defaults: { main: '', opus: '', sonnet: '', haiku: '' },
  },
]

const editing: ProviderRecord = {
  id: 'p1',
  name: '智谱 GLM · 个人',
  preset: 'zhipu',
  base_url: 'https://open.bigmodel.cn/api/anthropic',
  auth_field: 'ANTHROPIC_AUTH_TOKEN',
  credential: 'c1',
  models: ['glm-5.1'],
  defaults: presets[0].defaults,
  note: '',
  created: '',
  updated: '',
}

describe('ProviderDialog', () => {
  it('选平台自动带出 base_url、auth_field 与模型清单', async () => {
    render(
      wrap(
        <ProviderDialog
          presets={presets}
          credentials={[]}
          onClose={vi.fn()}
          onSaved={vi.fn()}
        />,
      ),
    )
    await userEvent.click(screen.getByRole('button', { name: /Zhipu GLM/ }))

    expect(screen.getByLabelText('base_url')).toHaveValue(
      'https://open.bigmodel.cn/api/anthropic',
    )
    expect(screen.getByLabelText('auth_field')).toHaveValue('ANTHROPIC_AUTH_TOKEN')
    expect(screen.getByText('glm-5.1')).toBeInTheDocument()
  })

  it('选「自定义」时 base_url 留空、可自由填写', async () => {
    render(
      wrap(
        <ProviderDialog
          presets={presets}
          credentials={[]}
          onClose={vi.fn()}
          onSaved={vi.fn()}
        />,
      ),
    )
    await userEvent.click(screen.getByRole('button', { name: /Zhipu GLM/ }))
    await userEvent.click(screen.getByRole('button', { name: /自定义|Custom/ }))
    expect(screen.getByLabelText('base_url')).toHaveValue('')
  })

  it('编辑既有服务配置时明确提示会立即重注入且不产生新版本', () => {
    render(
      wrap(
        <ProviderDialog
          presets={presets}
          credentials={[]}
          editing={editing}
          boundSetCount={3}
          onClose={vi.fn()}
          onSaved={vi.fn()}
        />,
      ),
    )
    expect(screen.getByTestId('reinject-hint')).toHaveTextContent(/不产生新版本|new version/)
    expect(screen.getByTestId('reinject-hint')).toHaveTextContent('3')
  })

  it('缺 base_url 或 凭据 时保存按钮禁用', async () => {
    render(
      wrap(
        <ProviderDialog
          presets={presets}
          credentials={[]}
          onClose={vi.fn()}
          onSaved={vi.fn()}
        />,
      ),
    )
    await userEvent.click(screen.getByRole('button', { name: /Zhipu GLM/ }))
    expect(screen.getByRole('button', { name: /^保存$|^Save$/ })).toBeDisabled()
  })
})
