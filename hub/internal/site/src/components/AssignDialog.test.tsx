import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { AssignDialog } from '@/components/AssignDialog'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

// 测试里 Trans 需要 i18n 上下文；用空消息表即可（msgid 原样显示）
i18n.load('en', {})
i18n.activate('en')

function wrap(ui: React.ReactNode) {
  return <I18nProvider i18n={i18n}>{ui}</I18nProvider>
}

describe('AssignDialog', () => {
  it('未选模式时提交按钮禁用', () => {
    render(wrap(
      <AssignDialog machineId="m" configSets={[{ id: 's', name: 'x' }]} onSubmit={() => {}} onClose={() => {}} />,
    ))
    expect(screen.getByRole('button', { name: /确定|Confirm/ })).toBeDisabled()
  })

  it('选 apply 后提交带上 mode', async () => {
    const onSubmit = vi.fn()
    render(wrap(
      <AssignDialog machineId="m" configSets={[{ id: 's', name: 'x' }]} onSubmit={onSubmit} onClose={() => {}} />,
    ))
    await userEvent.click(screen.getByLabelText(/应用配置集|Apply/))
    await userEvent.click(screen.getByRole('button', { name: /确定|Confirm/ }))
    expect(onSubmit).toHaveBeenCalledWith({ configSet: 's', mode: 'apply' })
  })
})
