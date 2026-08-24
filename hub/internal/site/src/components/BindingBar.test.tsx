import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { BindingBar } from '@/components/BindingBar'
import { emptySlots, fillAllSlots } from '@/lib/binding'
import type { ProviderRecord } from '@/types/collections'

i18n.load('en', {})
i18n.activate('en')

function wrap(ui: React.ReactNode) {
  return <I18nProvider i18n={i18n}>{ui}</I18nProvider>
}

const provider: ProviderRecord = {
  id: 'p1',
  name: '智谱 GLM · 个人',
  preset: 'zhipu',
  note: '',
  key_last4: 'a1b2',
  claude: {
    base_url: 'https://open.bigmodel.cn/api/anthropic',
    auth_field: 'ANTHROPIC_AUTH_TOKEN',
    key_last4: 'a1b2',
    models: ['glm-5.1', 'glm-4.7'],
    defaults: null,
  },
  openai: null,
  created: '',
  updated: '',
}

/** 只配了 openai 端点：绑不上，下拉里要置灰（M1.6 spec §5.3）。 */
const noClaude: ProviderRecord = {
  id: 'p2',
  name: '只有 OpenAI 的中转',
  preset: '',
  note: '',
  key_last4: 'ffff',
  claude: null,
  openai: {
    base_url: 'https://relay.example/v1',
    auth_field: 'OPENAI_API_KEY',
    key_last4: 'ffff',
    models: ['gpt-5.2'],
    default_model: 'gpt-5.2',
  },
  created: '',
  updated: '',
}

describe('BindingBar', () => {
  it('选主模型时四槽同填', async () => {
    const onChange = vi.fn()
    render(
      wrap(
        <BindingBar
          providers={[provider]}
          binding={{ provider: 'p1', models: emptySlots() }}
          settingsText="{}"
          onChange={onChange}
          onInsertSnippet={vi.fn()}
        />,
      ),
    )

    await userEvent.selectOptions(screen.getByLabelText(/主模型|Main model/), 'glm-5.1')
    expect(onChange).toHaveBeenCalledWith({
      provider: 'p1',
      models: { main: 'glm-5.1', opus: 'glm-5.1', sonnet: 'glm-5.1', haiku: 'glm-5.1' },
    })
  })

  it('高级里能分开设三个槽', async () => {
    const onChange = vi.fn()
    render(
      wrap(
        <BindingBar
          providers={[provider]}
          binding={{ provider: 'p1', models: fillAllSlots('glm-5.1') }}
          settingsText="{}"
          onChange={onChange}
          onInsertSnippet={vi.fn()}
        />,
      ),
    )

    await userEvent.click(screen.getByRole('button', { name: /高级|Advanced/ }))
    await userEvent.selectOptions(screen.getByLabelText(/haiku/i), 'glm-4.7')
    expect(onChange).toHaveBeenCalledWith({
      provider: 'p1',
      models: { main: 'glm-5.1', opus: 'glm-5.1', sonnet: 'glm-5.1', haiku: 'glm-4.7' },
    })
  })

  it('「插入 env 片段」只在文件里还没有 provider 引用时出现', () => {
    const { rerender } = render(
      wrap(
        <BindingBar
          providers={[provider]}
          binding={{ provider: 'p1', models: fillAllSlots('glm-5.1') }}
          settingsText="{}"
          onChange={vi.fn()}
          onInsertSnippet={vi.fn()}
        />,
      ),
    )
    expect(
      screen.getByRole('button', { name: /插入 env 片段|Insert env/ }),
    ).toBeInTheDocument()

    rerender(
      wrap(
        <BindingBar
          providers={[provider]}
          binding={{ provider: 'p1', models: fillAllSlots('glm-5.1') }}
          settingsText='{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}"}}'
          onChange={vi.fn()}
          onInsertSnippet={vi.fn()}
        />,
      ),
    )
    expect(screen.queryByRole('button', { name: /插入 env 片段|Insert env/ })).toBeNull()
  })

  it('未绑定时不显示模型下拉与插入按钮', () => {
    render(
      wrap(
        <BindingBar
          providers={[provider]}
          binding={null}
          settingsText="{}"
          onChange={vi.fn()}
          onInsertSnippet={vi.fn()}
        />,
      ),
    )
    expect(screen.queryByLabelText(/主模型|Main model/)).toBeNull()
    expect(screen.queryByRole('button', { name: /插入 env 片段|Insert env/ })).toBeNull()
  })

  it('透传模式勾选后四槽清空', async () => {
    const onChange = vi.fn()
    render(
      wrap(
        <BindingBar
          providers={[provider]}
          binding={{ provider: 'p1', models: fillAllSlots('glm-5.1') }}
          settingsText="{}"
          onChange={onChange}
          onInsertSnippet={vi.fn()}
        />,
      ),
    )
    await userEvent.click(screen.getByRole('button', { name: /高级|Advanced/ }))
    await userEvent.click(screen.getByLabelText(/透传模式|Passthrough/))
    expect(onChange).toHaveBeenCalledWith({ provider: 'p1', models: emptySlots() })
  })
})

describe('BindingBar 的端点感知', () => {
  it('没配 claude 端点的 provider 在下拉里置灰', () => {
    render(
      wrap(
        <BindingBar
          providers={[provider, noClaude]}
          binding={null}
          settingsText="{}"
          onChange={vi.fn()}
          onInsertSnippet={vi.fn()}
        />,
      ),
    )
    const opt = screen.getByRole('option', { name: /只有 OpenAI 的中转/ })
    expect(opt).toBeDisabled()
    expect(opt).toHaveAttribute('title', expect.stringContaining('还没有 Claude 端点'))
  })

  it('模型下拉取 claude 端点的模型清单', () => {
    render(
      wrap(
        <BindingBar
          providers={[provider]}
          binding={{ provider: 'p1', models: emptySlots() }}
          settingsText="{}"
          onChange={vi.fn()}
          onInsertSnippet={vi.fn()}
        />,
      ),
    )
    expect(screen.getByRole('option', { name: 'glm-5.1' })).toBeTruthy()
  })
})

describe('BindingBar 的 1M 上下文声明', () => {
  const binding = { provider: 'p1', models: fillAllSlots('glm-5.1[1m]') }

  it('已存的 1M 绑定：下拉显示基名而不是空白', () => {
    render(
      wrap(
        <BindingBar
          providers={[provider]}
          binding={binding}
          settingsText=""
          onChange={vi.fn()}
          onInsertSnippet={vi.fn()}
        />,
      ),
    )
    expect(screen.getByLabelText('主模型')).toHaveValue('glm-5.1')
    expect(screen.getByRole('checkbox', { name: '声明 1M 上下文' })).toBeChecked()
  })

  it('取消 1M 只影响基名相同的槽', async () => {
    const onChange = vi.fn()
    const mixed = {
      provider: 'p1',
      models: { main: 'glm-5.1[1m]', opus: 'glm-5.1[1m]', sonnet: 'glm-5.1[1m]', haiku: 'glm-4.7' },
    }
    render(
      wrap(
        <BindingBar
          providers={[provider]}
          binding={mixed}
          settingsText=""
          onChange={onChange}
          onInsertSnippet={vi.fn()}
        />,
      ),
    )
    await userEvent.click(screen.getByRole('checkbox', { name: '声明 1M 上下文' }))

    expect(onChange.mock.calls[0][0].models).toEqual({
      main: 'glm-5.1',
      opus: 'glm-5.1',
      sonnet: 'glm-5.1',
      haiku: 'glm-4.7',
    })
  })

  it('高级里的分槽下拉保留各槽自己的声明', async () => {
    const onChange = vi.fn()
    render(
      wrap(
        <BindingBar
          providers={[provider]}
          binding={binding}
          settingsText=""
          onChange={onChange}
          onInsertSnippet={vi.fn()}
        />,
      ),
    )
    await userEvent.click(screen.getByRole('button', { name: /高级/ }))
    expect(screen.getByLabelText('haiku')).toHaveValue('glm-5.1')

    await userEvent.selectOptions(screen.getByLabelText('haiku'), 'glm-4.7')
    expect(onChange.mock.calls[0][0].models.haiku).toBe('glm-4.7[1m]')
  })
})
