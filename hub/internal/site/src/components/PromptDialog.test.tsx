import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { PromptDialog } from '@/components/PromptDialog'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

i18n.load('en', {})
i18n.activate('en')

function wrap(ui: React.ReactNode) {
  return <I18nProvider i18n={i18n}>{ui}</I18nProvider>
}

describe('PromptDialog', () => {
  it('空值时确认按钮禁用', () => {
    render(
      wrap(
        <PromptDialog title="名称" defaultValue="" onSubmit={() => {}} onClose={() => {}} />,
      ),
    )
    expect(screen.getByRole('button', { name: /确定|Confirm|OK/ })).toBeDisabled()
  })

  it('提交修剪后的值', async () => {
    const onSubmit = vi.fn()
    render(
      wrap(
        <PromptDialog
          title="名称"
          defaultValue="  foo  "
          onSubmit={onSubmit}
          onClose={() => {}}
        />,
      ),
    )
    await userEvent.click(screen.getByRole('button', { name: /确定|Confirm|OK/ }))
    expect(onSubmit).toHaveBeenCalledWith('foo')
  })

  it('取消调用 onClose', async () => {
    const onClose = vi.fn()
    render(
      wrap(
        <PromptDialog title="名称" defaultValue="x" onSubmit={() => {}} onClose={onClose} />,
      ),
    )
    await userEvent.click(screen.getByRole('button', { name: /取消|Cancel/ }))
    expect(onClose).toHaveBeenCalled()
  })
})
