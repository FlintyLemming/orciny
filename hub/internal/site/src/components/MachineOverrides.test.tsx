import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { $overrides } from '@/stores/overrides'
import { MachineOverrides } from '@/components/MachineOverrides'
import type { MachineOverrideRecord } from '@/types/collections'

i18n.load('zh', {})
i18n.activate('zh')

vi.mock('@/stores/overrides', async (orig) => {
  const mod = await orig<typeof import('@/stores/overrides')>()
  return { ...mod, subscribeOverrides: () => () => {} }
})

const dropOverride = vi.fn().mockResolvedValue(undefined)
vi.mock('@/lib/api', () => ({ dropOverride: (id: string) => dropOverride(id) }))

function ov(over: Partial<MachineOverrideRecord> = {}): MachineOverrideRecord {
  return {
    id: 'o1', machine: 'm1', path: '.claude/settings.json', kind: 'json_key',
    selector: 'env.A', base_value: '"中台"', mine_value: '"本机"',
    base_blob: '', mine_blob: '', attention: '', shadowed_value: '',
    shadowed_blob: '', shadowed_rev: '', origin_drift: '', note: '',
    created: '2026-09-01T00:00:00Z', updated: '2026-09-01T00:00:00Z',
    ...over,
  }
}

function view() {
  return render(
    <I18nProvider i18n={i18n}>
      <MachineOverrides machineId="m1" />
    </I18nProvider>,
  )
}

beforeEach(() => {
  dropOverride.mockClear()
  $overrides.set([])
})

describe('MachineOverrides', () => {
  it('没有覆盖层时给一句话', () => {
    view()
    expect(screen.getByText(/没有本机覆盖/)).toBeInTheDocument()
  })

  it('按路径分组并显示差异点数', () => {
    $overrides.set([ov(), ov({ id: 'o2', selector: 'env.B' })])
    view()
    expect(screen.getByText('.claude/settings.json')).toBeInTheDocument()
    expect(screen.getByText(/2 处/)).toBeInTheDocument()
  })

  it('别的机器的覆盖层不出现', () => {
    $overrides.set([ov({ machine: 'm2', path: '.claude/别的.json' })])
    view()
    expect(screen.queryByText('.claude/别的.json')).not.toBeInTheDocument()
  })

  it('有 attention 的条目高亮并写明原因', () => {
    $overrides.set([ov({ attention: 'hub_changed' })])
    view()
    expect(screen.getByText(/中台也改了同一处/)).toBeInTheDocument()
  })

  it('逐条删除调 dropOverride', async () => {
    const user = userEvent.setup()
    $overrides.set([ov()])
    view()
    await user.click(screen.getByRole('button', { name: /删除/ }))
    expect(dropOverride).toHaveBeenCalledWith('o1')
  })
})
