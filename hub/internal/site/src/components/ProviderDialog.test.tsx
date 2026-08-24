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
    claude: {
      base_url: 'https://open.bigmodel.cn/api/anthropic',
      auth_field: 'ANTHROPIC_AUTH_TOKEN',
      models: ['glm-5.2', 'glm-4.7'],
      defaults: {
        main: 'glm-5.2[1m]',
        opus: 'glm-5.2[1m]',
        sonnet: 'glm-5.2[1m]',
        haiku: 'glm-4.7',
      },
      default_model: '',
    },
    openai: {
      base_url: 'https://open.bigmodel.cn/api/paas/v4',
      auth_field: 'OPENAI_API_KEY',
      models: ['glm-5.2'],
      defaults: { main: '', opus: '', sonnet: '', haiku: '' },
      default_model: 'glm-5.2',
    },
  },
  {
    id: 'anthropic',
    name: 'Anthropic Official',
    claude: {
      base_url: 'https://api.anthropic.com',
      auth_field: 'ANTHROPIC_API_KEY',
      models: ['claude-opus-5'],
      defaults: { main: '', opus: '', sonnet: '', haiku: '' },
      default_model: '',
    },
    openai: {
      base_url: '',
      auth_field: 'OPENAI_API_KEY',
      models: [],
      defaults: { main: '', opus: '', sonnet: '', haiku: '' },
      default_model: '',
    },
  },
]

const editing: ProviderRecord = {
  id: 'p1',
  name: '智谱 GLM · 个人',
  preset: 'zhipu',
  note: '',
  key_last4: 'a1b2',
  claude: {
    base_url: 'https://open.bigmodel.cn/api/anthropic',
    auth_field: 'ANTHROPIC_AUTH_TOKEN',
    key_last4: 'a1b2',
    models: ['glm-5.2', 'glm-4.7'],
    defaults: {
      main: 'glm-5.2[1m]',
      opus: 'glm-5.2[1m]',
      sonnet: 'glm-5.2[1m]',
      haiku: 'glm-4.7',
    },
  },
  openai: null,
  created: '',
  updated: '',
}

describe('ProviderDialog', () => {
  it('选预设时两个端点一起带出', async () => {
    render(wrap(<ProviderDialog presets={presets} onClose={vi.fn()} onSaved={vi.fn()} />))
    await userEvent.click(screen.getByText('Zhipu GLM'))

    expect(screen.getByLabelText('claude base_url')).toHaveValue(
      'https://open.bigmodel.cn/api/anthropic',
    )
    expect(screen.getByLabelText('openai base_url')).toHaveValue(
      'https://open.bigmodel.cn/api/paas/v4',
    )
    expect(screen.getByLabelText('openai auth_field')).toHaveValue('OPENAI_API_KEY')
  })

  it('平台没有 openai 口时那一侧留空并说明', async () => {
    render(wrap(<ProviderDialog presets={presets} onClose={vi.fn()} onSaved={vi.fn()} />))
    await userEvent.click(screen.getByText('Anthropic Official'))

    // openai 分区没配 base_url，默认折叠——先展开再看。
    await userEvent.click(screen.getByRole('button', { name: /OpenAI 端点/ }))
    expect(screen.getByLabelText('openai base_url')).toHaveValue('')
    expect(screen.getByText(/该平台未提供 OpenAI 端点/)).toBeTruthy()
  })

  it('端点状态是显示不是开关：清空 base_url 即未配置', async () => {
    render(wrap(<ProviderDialog presets={presets} onClose={vi.fn()} onSaved={vi.fn()} />))
    await userEvent.click(screen.getByText('Zhipu GLM'))
    expect(screen.getByTestId('openai-status')).toHaveTextContent('已配置')

    await userEvent.clear(screen.getByLabelText('openai base_url'))
    expect(screen.getByTestId('openai-status')).toHaveTextContent('未配置')

    // 没有任何 checkbox / switch 可以单独开关端点
    expect(screen.queryByRole('switch')).toBeNull()
    expect(screen.queryByRole('checkbox', { name: /端点/ })).toBeNull()
  })

  it('OpenAI 分区带「本期不产生注入」的说明', async () => {
    render(wrap(<ProviderDialog presets={presets} onClose={vi.fn()} onSaved={vi.fn()} />))
    await userEvent.click(screen.getByRole('button', { name: /OpenAI 端点/ }))
    expect(screen.getByTestId('openai-inert-note')).toBeTruthy()
  })

  it('编辑态密码框为空，提示留空则不修改，右侧显示末四位', () => {
    render(
      wrap(
        <ProviderDialog
          presets={presets}
          editing={editing}
          onClose={vi.fn()}
          onSaved={vi.fn()}
        />,
      ),
    )
    const key = screen.getByLabelText('API key')
    expect(key).toHaveValue('')
    expect(key).toHaveAttribute('placeholder', expect.stringContaining('留空则不修改'))
    expect(screen.getAllByText('····a1b2').length).toBeGreaterThan(0)
  })

  it('编辑态保存时不带 key 字段——留空 = 不修改', async () => {
    const save = vi.fn().mockResolvedValue(undefined)
    render(
      wrap(
        <ProviderDialog
          presets={presets}
          editing={editing}
          onClose={vi.fn()}
          onSaved={vi.fn()}
          onSubmit={save}
        />,
      ),
    )
    await userEvent.click(screen.getByText('保存'))

    const body = save.mock.calls[0][0]
    expect('key' in body).toBe(false)
    expect('key' in body.claude).toBe(false)
  })

  it('填了端点级 key 就带上它', async () => {
    const save = vi.fn().mockResolvedValue(undefined)
    render(
      wrap(
        <ProviderDialog
          presets={presets}
          editing={editing}
          onClose={vi.fn()}
          onSaved={vi.fn()}
          onSubmit={save}
        />,
      ),
    )
    await userEvent.type(screen.getByLabelText('claude 单独的 key'), 'sk-claude-abcdef12')
    await userEvent.click(screen.getByText('保存'))

    expect(save.mock.calls[0][0].claude.key).toBe('sk-claude-abcdef12')
  })

  it('点「清除」把端点级 key 显式清空——留空是「不修改」，撤不掉已设的值', async () => {
    const save = vi.fn().mockResolvedValue(undefined)
    render(
      wrap(
        <ProviderDialog
          presets={presets}
          editing={editing}
          onClose={vi.fn()}
          onSaved={vi.fn()}
          onSubmit={save}
        />,
      ),
    )
    await userEvent.click(screen.getAllByText('清除')[1])
    await userEvent.click(screen.getByText('保存'))

    expect(save.mock.calls[0][0].claude.key).toBe('')
  })

  it('新建时配了 base_url 却没有任何 key 则禁止保存', async () => {
    render(wrap(<ProviderDialog presets={presets} onClose={vi.fn()} onSaved={vi.fn()} />))
    await userEvent.click(screen.getByText('Zhipu GLM'))
    await userEvent.type(screen.getByLabelText('名称'), '智谱个人')

    expect(screen.getByText('保存')).toBeDisabled()

    await userEvent.type(screen.getByLabelText('API key'), 'sk-zhipu-abcdef12')
    expect(screen.getByText('保存')).not.toBeDisabled()
  })

  it('编辑态显示重注入提示', () => {
    render(
      wrap(
        <ProviderDialog
          presets={presets}
          editing={editing}
          boundSetCount={2}
          onClose={vi.fn()}
          onSaved={vi.fn()}
        />,
      ),
    )
    expect(screen.getByTestId('reinject-hint')).toBeTruthy()
  })
})

describe('ProviderDialog 的 1M 上下文声明', () => {
  it('下拉列基名，1M 用独立复选框表示——同一个模型不该在下拉里出现两次', async () => {
    render(wrap(<ProviderDialog presets={presets} onClose={vi.fn()} onSaved={vi.fn()} />))
    await userEvent.click(screen.getByText('Zhipu GLM'))

    expect(screen.getByLabelText('claude 默认模型')).toHaveValue('glm-5.2')
    expect(screen.getByRole('checkbox', { name: 'claude 声明 1M 上下文' })).toBeChecked()
    expect(screen.getByLabelText('claude 默认模型')).not.toHaveDisplayValue('glm-5.2[1m]')
  })

  it('取消 1M 只影响用了该模型的槽，haiku 的独立映射保留', async () => {
    const save = vi.fn().mockResolvedValue(undefined)
    render(
      wrap(
        <ProviderDialog
          presets={presets}
          editing={editing}
          onClose={vi.fn()}
          onSaved={vi.fn()}
          onSubmit={save}
        />,
      ),
    )
    await userEvent.click(screen.getByRole('checkbox', { name: 'claude 声明 1M 上下文' }))
    await userEvent.click(screen.getByText('保存'))

    expect(save.mock.calls[0][0].claude.defaults).toEqual({
      main: 'glm-5.2',
      opus: 'glm-5.2',
      sonnet: 'glm-5.2',
      haiku: 'glm-4.7',
    })
  })

  it('换默认模型时带着当前的 1M 声明', async () => {
    const save = vi.fn().mockResolvedValue(undefined)
    render(
      wrap(
        <ProviderDialog
          presets={presets}
          editing={editing}
          onClose={vi.fn()}
          onSaved={vi.fn()}
          onSubmit={save}
        />,
      ),
    )
    await userEvent.selectOptions(screen.getByLabelText('claude 默认模型'), 'glm-4.7')
    await userEvent.click(screen.getByText('保存'))

    expect(save.mock.calls[0][0].claude.defaults.main).toBe('glm-4.7[1m]')
  })

  it('透传（不设模型）时 1M 复选框禁用', async () => {
    render(wrap(<ProviderDialog presets={presets} onClose={vi.fn()} onSaved={vi.fn()} />))
    await userEvent.click(screen.getByText('Anthropic Official'))

    expect(screen.getByRole('checkbox', { name: 'claude 声明 1M 上下文' })).toBeDisabled()
  })
})
