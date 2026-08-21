import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { BindingDriftActions } from '@/components/BindingDriftActions'

i18n.load('zh', {})
i18n.activate('zh')

function wrap(ui: React.ReactNode) {
  return <I18nProvider i18n={i18n}>{ui}</I18nProvider>
}

const base = { url: 'https://api.moonshot.cn/anthropic' }

describe('BindingDriftActions', () => {
  it('第一档：命中已有 Provider，给「改成它」', async () => {
    const onRebind = vi.fn()
    render(
      wrap(
        <BindingDriftActions
          match={{
            ...base,
            match: {
              kind: 'provider',
              exact: true,
              provider_id: 'p1',
              provider_name: 'Kimi 官方',
            },
          }}
          onRebind={onRebind}
          onCreateProvider={vi.fn()}
        />,
      ),
    )
    expect(screen.getByText(/Kimi 官方/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /改成它|改绑/ }))
    expect(onRebind).toHaveBeenCalledWith('p1')
  })

  it('host 匹配时措辞降级为「可能是」', () => {
    render(
      wrap(
        <BindingDriftActions
          match={{
            ...base,
            match: {
              kind: 'provider',
              exact: false,
              provider_id: 'p1',
              provider_name: 'Kimi 官方',
            },
          }}
          onRebind={vi.fn()}
          onCreateProvider={vi.fn()}
        />,
      ),
    )
    expect(screen.getByText(/可能是/)).toBeInTheDocument()
  })

  it('第二档：命中内置预设，给「新建服务配置」并带上 key 位置', async () => {
    const onCreate = vi.fn()
    render(
      wrap(
        <BindingDriftActions
          match={{
            ...base,
            match: {
              kind: 'preset',
              exact: true,
              preset_id: 'kimi',
              preset_name: 'Kimi (Moonshot)',
            },
            key_location: 'env.ANTHROPIC_AUTH_TOKEN',
            key_masked: 'sk-k…1234',
          }}
          onRebind={vi.fn()}
          onCreateProvider={onCreate}
        />,
      ),
    )
    expect(screen.getByText(/Kimi \(Moonshot\)/)).toBeInTheDocument()
    expect(screen.getByText(/sk-k…1234/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /新建服务配置/ }))
    expect(onCreate).toHaveBeenCalledWith('kimi', 'env.ANTHROPIC_AUTH_TOKEN')
  })

  it('第三档：都不命中，只写明无法识别，不给动作按钮', () => {
    render(
      wrap(
        <BindingDriftActions
          match={{ url: 'https://某中转.test/v1', match: { kind: 'none', exact: false } }}
          onRebind={vi.fn()}
          onCreateProvider={vi.fn()}
        />,
      ),
    )
    expect(screen.getByText(/无法识别/)).toBeInTheDocument()
    expect(screen.queryByRole('button')).toBeNull()
  })

  it('反查还没回来时不显示任何动作', () => {
    render(
      wrap(
        <BindingDriftActions match={undefined} onRebind={vi.fn()} onCreateProvider={vi.fn()} />,
      ),
    )
    expect(screen.queryByRole('button')).toBeNull()
  })
})
