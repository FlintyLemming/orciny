import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { AttentionBanner } from '@/components/AttentionBanner'
import type { MachineOverrideRecord } from '@/types/collections'

i18n.load('zh', {})
i18n.activate('zh')

function ov(over: Partial<MachineOverrideRecord> = {}): MachineOverrideRecord {
  return {
    id: 'o1', machine: 'm1', path: '.claude/settings.json', kind: 'json_key',
    selector: 'env.A', base_value: '"中台"', mine_value: '"本机"',
    base_blob: '', mine_blob: '', attention: 'hub_changed',
    shadowed_value: '"中台的新值"', shadowed_blob: '', shadowed_rev: 'r2',
    origin_drift: 'e1', note: '', created: '2026-09-01T00:00:00Z',
    updated: '2026-09-01T00:00:00Z',
    ...over,
  }
}

function view(items: MachineOverrideRecord[], handlers: Partial<{
  onDrop: (id: string) => void
  onKeep: (id: string) => void
  onCompare: (o: MachineOverrideRecord) => void
}> = {}) {
  return render(
    <I18nProvider i18n={i18n}>
      <AttentionBanner
        items={items}
        machineName={new Map([['m1', '主力机']])}
        busy={false}
        onDrop={handlers.onDrop ?? vi.fn()}
        onKeep={handlers.onKeep ?? vi.fn()}
        onCompare={handlers.onCompare ?? vi.fn()}
      />
    </I18nProvider>,
  )
}

describe('AttentionBanner', () => {
  it('没有需要看的条目时什么都不渲染', () => {
    const { container } = view([])
    expect(container).toBeEmptyDOMElement()
  })

  it('有条目时显示计数', () => {
    view([ov(), ov({ id: 'o2', selector: 'env.B' })])
    expect(screen.getByText(/2 处本机覆盖挡下了中台更新/)).toBeInTheDocument()
  })

  it('展开后每条一行，带机器名与路径', async () => {
    const user = userEvent.setup()
    view([ov()])
    await user.click(screen.getByRole('button', { name: /展开/ }))
    expect(screen.getByText('主力机')).toBeInTheDocument()
    expect(screen.getByText('.claude/settings.json')).toBeInTheDocument()
    expect(screen.getByText('env.A')).toBeInTheDocument()
  })

  it('「撤掉排除」与「保持」各自调对回调', async () => {
    const user = userEvent.setup()
    const onDrop = vi.fn()
    const onKeep = vi.fn()
    view([ov()], { onDrop, onKeep })
    await user.click(screen.getByRole('button', { name: /展开/ }))

    await user.click(screen.getByRole('button', { name: /撤掉排除/ }))
    expect(onDrop).toHaveBeenCalledWith('o1')

    await user.click(screen.getByRole('button', { name: /保持/ }))
    expect(onKeep).toHaveBeenCalledWith('o1')
  })

  it('四种情形各说各的话', async () => {
    const user = userEvent.setup()
    view([
      ov({ id: 'a', attention: 'hub_changed' }),
      ov({ id: 'b', attention: 'merge_conflict', selector: '' , kind: 'text' }),
      ov({ id: 'c', attention: 'path_gone', selector: 'x' }),
      ov({ id: 'd', attention: 'unmergeable', selector: 'y' }),
    ])
    await user.click(screen.getByRole('button', { name: /展开/ }))
    expect(screen.getByText(/中台也改了同一处/)).toBeInTheDocument()
    expect(screen.getByText(/两边改法不同/)).toBeInTheDocument()
    expect(screen.getByText(/已不在中台的下发范围里/)).toBeInTheDocument()
    expect(screen.getByText(/不是合法的 JSON 对象/)).toBeInTheDocument()
  })
})
