import { render, screen, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { Toast } from '@/components/Toast'
import { hideToast, showToast } from '@/stores/toast'

i18n.load('en', {})
i18n.activate('en')

function wrap(ui: React.ReactNode) {
  return <I18nProvider i18n={i18n}>{ui}</I18nProvider>
}

describe('Toast', () => {
  beforeEach(() => {
    hideToast()
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('显示消息与撤销按钮，超时自动消失', () => {
    render(wrap(<Toast />))
    expect(screen.queryByRole('status')).toBeNull()

    act(() => {
      showToast('已切到 glm-5.2 · 影响 3 台', vi.fn(), 8000)
    })

    expect(screen.getByRole('status')).toHaveTextContent('已切到 glm-5.2 · 影响 3 台')
    expect(screen.getByRole('button', { name: '撤销' })).toBeTruthy()

    act(() => {
      vi.advanceTimersByTime(8000)
    })

    expect(screen.queryByRole('status')).toBeNull()
  })

  it('点撤销触发回调并隐藏 toast', async () => {
    vi.useRealTimers()
    const onUndo = vi.fn()
    render(wrap(<Toast />))

    act(() => {
      showToast('已切到 glm-5.2', onUndo)
    })

    const btn = screen.getByRole('button', { name: '撤销' })
    await userEvent.click(btn)

    expect(onUndo).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('status')).toBeNull()
  })
})
