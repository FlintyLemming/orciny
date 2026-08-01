import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { DegradedBanner } from '@/components/DegradedBanner'

i18n.load('zh', {})
i18n.activate('zh')

function wrap(ui: React.ReactNode) {
  return <I18nProvider i18n={i18n}>{ui}</I18nProvider>
}

describe('DegradedBanner', () => {
  it('非 degraded 时不渲染', () => {
    const { container } = render(wrap(
      <DegradedBanner state="aligned" lastError="" onClear={() => {}} />,
    ))
    expect(container).toBeEmptyDOMElement()
  })

  it('degraded 时显示原因与后果', () => {
    render(wrap(
      <DegradedBanner state="degraded" lastError="permission denied" onClear={() => {}} />,
    ))
    expect(screen.getByText(/permission denied/)).toBeInTheDocument()
    expect(screen.getByText(/停止向这台机器应用|stopped applying/)).toBeInTheDocument()
    expect(screen.getByText(/先看看|survey/)).toBeInTheDocument()
  })

  // 解除是有后果的操作，不能一键就走
  it('解除需要二次确认', async () => {
    const onClear = vi.fn()
    render(wrap(
      <DegradedBanner state="degraded" lastError="x" onClear={onClear} />,
    ))
    await userEvent.click(screen.getByRole('button', { name: /解除|Clear/ }))
    expect(onClear).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: /确认|Confirm/ }))
    expect(onClear).toHaveBeenCalled()
  })
})
