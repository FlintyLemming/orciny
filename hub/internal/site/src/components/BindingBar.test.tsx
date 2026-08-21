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
  base_url: 'https://open.bigmodel.cn/api/anthropic',
  auth_field: 'ANTHROPIC_AUTH_TOKEN',
  credential: 'c1',
  models: ['glm-5.1', 'glm-4.7'],
  defaults: null,
  note: '',
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
          settingsText='{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}'
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
