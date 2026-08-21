import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { PublishDialog } from '@/components/PublishDialog'
import { fixAuthField, validateConfigSet } from '@/lib/api'

vi.mock('@/lib/api', () => ({
  validateConfigSet: vi.fn(),
  publishConfigSet: vi.fn(),
  fixAuthField: vi.fn(),
}))

i18n.load('en', {})
i18n.activate('en')

function wrap(ui: React.ReactNode) {
  return <I18nProvider i18n={i18n}>{ui}</I18nProvider>
}

function renderDialog() {
  return render(
    wrap(
      <PublishDialog
        setId="s1"
        draftState="dirty"
        affectedMachines={2}
        onClose={vi.fn()}
        onPublished={vi.fn()}
      />,
    ),
  )
}

describe('PublishDialog', () => {
  it('只有警告时仍然可以发布', async () => {
    vi.mocked(validateConfigSet).mockResolvedValue([
      { path: '', kind: 'binding_unused', detail: '绑了但没用', warning: true },
    ])
    renderDialog()

    expect(await screen.findByText(/绑了但没用/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^发布$|^Publish$/ })).toBeEnabled()
  })

  it('有错误时禁止发布', async () => {
    vi.mocked(validateConfigSet).mockResolvedValue([
      { path: '.claude/settings.json', kind: 'binding_missing', detail: '没有服务绑定' },
    ])
    renderDialog()

    expect(await screen.findByText(/没有服务绑定/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^发布$|^Publish$/ })).toBeDisabled()
  })

  it('带 fix 的问题给「一键修复」，点了之后重新校验', async () => {
    vi.mocked(validateConfigSet)
      .mockResolvedValueOnce([
        {
          path: '.claude/settings.json',
          kind: 'auth_field_mismatch',
          detail: '键名对不上',
          fix: {
            kind: 'replace_env_key',
            from: 'ANTHROPIC_API_KEY',
            to: 'ANTHROPIC_AUTH_TOKEN',
          },
        },
      ])
      .mockResolvedValueOnce([])
    vi.mocked(fixAuthField).mockResolvedValue(undefined)

    renderDialog()

    await userEvent.click(await screen.findByRole('button', { name: /一键修复|Fix it/ }))
    expect(fixAuthField).toHaveBeenCalledWith('s1')
    expect(screen.queryByText(/键名对不上/)).toBeNull()
  })
})
