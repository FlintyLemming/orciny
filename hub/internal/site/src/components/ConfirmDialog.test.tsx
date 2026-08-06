import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

i18n.load('en', {})
i18n.activate('en')

function wrap(ui: React.ReactNode) {
  return <I18nProvider i18n={i18n}>{ui}</I18nProvider>
}

describe('ConfirmDialog', () => {
  it('确认调用 onConfirm', async () => {
    const onConfirm = vi.fn()
    render(
      wrap(
        <ConfirmDialog message="确定删除？" onConfirm={onConfirm} onClose={() => {}} />,
      ),
    )
    await userEvent.click(screen.getByRole('button', { name: /确认|Confirm/ }))
    expect(onConfirm).toHaveBeenCalled()
  })

  it('取消调用 onClose', async () => {
    const onClose = vi.fn()
    render(wrap(<ConfirmDialog message="确定删除？" onConfirm={() => {}} onClose={onClose} />))
    await userEvent.click(screen.getByRole('button', { name: /取消|Cancel/ }))
    expect(onClose).toHaveBeenCalled()
  })

  it('Escape 关闭', async () => {
    const onClose = vi.fn()
    render(wrap(<ConfirmDialog message="确定删除？" onConfirm={() => {}} onClose={onClose} />))
    await userEvent.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalled()
  })
})
