import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { AddFileDialog } from '@/components/AddFileDialog'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

i18n.load('en', {})
i18n.activate('en')

function wrap(ui: React.ReactNode) {
  return <I18nProvider i18n={i18n}>{ui}</I18nProvider>
}

describe('AddFileDialog', () => {
  it('空路径时提交按钮禁用', () => {
    render(wrap(<AddFileDialog onSubmit={() => {}} onClose={() => {}} />))
    expect(screen.getByRole('button', { name: /^添加$|^Add$/ })).toBeDisabled()
  })

  it('提交修剪后的路径', async () => {
    const onSubmit = vi.fn()
    render(wrap(<AddFileDialog onSubmit={onSubmit} onClose={() => {}} />))
    await userEvent.type(screen.getByLabelText(/文件路径|Path/), '  .claude/settings.json  ')
    await userEvent.click(screen.getByRole('button', { name: /^添加$|^Add$/ }))
    expect(onSubmit).toHaveBeenCalledWith('.claude/settings.json')
  })

  it('拒绝草稿中已有的路径', async () => {
    const onSubmit = vi.fn()
    render(wrap(
      <AddFileDialog
        existingPaths={['.claude/settings.json']}
        onSubmit={onSubmit}
        onClose={() => {}}
      />,
    ))
    await userEvent.type(screen.getByLabelText(/文件路径|Path/), '.claude/settings.json')
    await userEvent.click(screen.getByRole('button', { name: /^添加$|^Add$/ }))
    expect(onSubmit).not.toHaveBeenCalled()
    expect(screen.getByText(/已有该路径|already/i)).toBeInTheDocument()
  })

  it('取消调用 onClose', async () => {
    const onClose = vi.fn()
    render(wrap(<AddFileDialog onSubmit={() => {}} onClose={onClose} />))
    await userEvent.click(screen.getByRole('button', { name: /取消|Cancel/ }))
    expect(onClose).toHaveBeenCalled()
  })
})
