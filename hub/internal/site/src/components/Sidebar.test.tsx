import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { atom } from 'nanostores'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { Sidebar } from '@/components/Sidebar'

// 侧栏为了角标订了一份漂移计数，那会开一条 PB realtime 连接——
// jsdom 里没有 EventSource。这里只测导航项，把订阅换成空操作。
vi.mock('@/stores/drift', () => ({
  $openDriftCount: atom(0),
  subscribeDrifts: () => () => {},
}))

i18n.load('en', {})
i18n.activate('en')

describe('Sidebar', () => {
  it('不再有凭据入口', () => {
    render(
      <I18nProvider i18n={i18n}>
        <Sidebar />
      </I18nProvider>,
    )
    expect(screen.queryByText('凭据')).toBeNull()
    expect(screen.getByText('AI 服务')).toBeTruthy()
  })
})
