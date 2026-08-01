import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { FindingList } from '@/components/FindingList'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

i18n.load('en', {})
i18n.activate('en')

function wrap(ui: React.ReactNode) {
  return <I18nProvider i18n={i18n}>{ui}</I18nProvider>
}

describe('FindingList', () => {
  // 这条是安全断言：UI 会把 masked 直接渲染出来
  it('只渲染掩码，绝不渲染完整值', () => {
    render(wrap(
      <FindingList findings={[{
        path: '.claude/settings.json', location: 'env.K', key: 'K',
        masked: 'sk-a…mnop', suggested: 'k', rule: 'structured',
      }]} onAction={() => {}} />,
    ))
    expect(screen.getByText('sk-a…mnop')).toBeInTheDocument()
    expect(screen.queryByText(/sk-ant-abcdefghijklmnop/)).toBeNull()
  })

  it('结构化位置排在最前', () => {
    render(wrap(
      <FindingList findings={[
        { path: 'a', location: 'x', key: 'x', masked: '…', suggested: 'x', rule: 'value_entropy' },
        { path: 'b', location: 'env.Y', key: 'Y', masked: '…', suggested: 'y', rule: 'structured' },
      ]} onAction={() => {}} />,
    ))
    const rows = screen.getAllByRole('listitem')
    expect(rows[0]).toHaveTextContent('env.Y')
  })

  it('保留明文需要二次确认', async () => {
    const onAction = vi.fn()
    render(wrap(
      <FindingList findings={[{
        path: 'a', location: 'env.K', key: 'K', masked: '…', suggested: 'k', rule: 'structured',
      }]} onAction={onAction} />,
    ))
    await userEvent.click(screen.getByRole('button', { name: /保留明文|Keep plaintext/ }))
    expect(onAction).not.toHaveBeenCalled()
    expect(screen.getByText(/不可变的版本历史|immutable/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /确认|Confirm/ }))
    expect(onAction).toHaveBeenCalledWith('keep', expect.anything())
  })
})
