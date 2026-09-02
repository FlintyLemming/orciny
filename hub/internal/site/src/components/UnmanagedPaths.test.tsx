import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { UnmanagedPaths } from '@/components/UnmanagedPaths'

i18n.load('zh', {})
i18n.activate('zh')

const rules = [
  { id: 'r1', machine: 'm1', path: '.claude/settings.json', note: '', created: '' },
  { id: 'r2', machine: '', path: '.claude/全局.json', note: '', created: '' },
]

vi.mock('@/lib/pb', () => ({
  pb: {
    collection: () => ({ getFullList: () => Promise.resolve(rules) }),
    filter: (s: string) => s,
  },
}))

const remanage = vi.fn().mockResolvedValue(undefined)
vi.mock('@/lib/api', () => ({ remanage: (m: string, p: string) => remanage(m, p) }))

function view() {
  return render(
    <I18nProvider i18n={i18n}>
      <UnmanagedPaths machineId="m1" />
    </I18nProvider>,
  )
}

beforeEach(() => remanage.mockClear())

describe('UnmanagedPaths', () => {
  it('列出机器级与全局的规则，并标明来源', async () => {
    view()
    expect(await screen.findByText('.claude/settings.json')).toBeInTheDocument()
    expect(screen.getByText('.claude/全局.json')).toBeInTheDocument()
    // 精确匹配「全局」这个来源标签本身——用 /全局/ 正则会同时命中
    // 路径 .claude/全局.json 与「全局规则请到设置里解除」。
    expect(screen.getByText('全局')).toBeInTheDocument()
  })

  it('「恢复受管」调对接口', async () => {
    const user = userEvent.setup()
    view()
    await user.click(await screen.findByRole('button', { name: /恢复受管/ }))
    await waitFor(() =>
      expect(remanage).toHaveBeenCalledWith('m1', '.claude/settings.json'),
    )
  })

  it('全局规则不给「恢复受管」按钮 —— 那会悄悄改掉全机队的行为', async () => {
    view()
    await screen.findByText('.claude/全局.json')
    expect(screen.getAllByRole('button', { name: /恢复受管/ })).toHaveLength(1)
  })
})
