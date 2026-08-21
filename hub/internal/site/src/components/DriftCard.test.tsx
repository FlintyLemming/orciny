import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { DriftCard } from '@/components/DriftCard'
import type { DriftEvent } from '@/lib/inbox'

i18n.load('zh', {})
i18n.activate('zh')

function wrap(ui: React.ReactNode) {
  return <I18nProvider i18n={i18n}>{ui}</I18nProvider>
}

function ev(over: Partial<DriftEvent>): DriftEvent {
  return {
    id: 'e1', machine: 'm1', config_set: 's1', path: 'a', kind: 'modified',
    state: 'open', diff: '', truncated: false, restore_partial: false,
    binding_drift: false, binding_url: '',
    created: '2026-07-31T12:00:00Z',
    ...over,
  }
}

describe('DriftCard', () => {
  it('truncated 的条目禁用收编并给出说明', () => {
    render(wrap(<DriftCard event={ev({ truncated: true })} selected={false} onToggle={() => {}} />))
    expect(screen.getByText(/未能安全脱敏|could not be safely/)).toBeInTheDocument()
    expect(screen.getByRole('checkbox')).toBeDisabled()
  })

  it('restore_partial 的条目给出复核提示但可选', () => {
    render(wrap(<DriftCard event={ev({ restore_partial: true })} selected={false} onToggle={() => {}} />))
    expect(screen.getByText(/需人工复核|needs review/)).toBeInTheDocument()
    expect(screen.getByRole('checkbox')).toBeEnabled()
  })

  it('superseded 的条目标注「已被覆盖」且仍可重新收编', () => {
    render(wrap(<DriftCard event={ev({ state: 'superseded' })} selected={false} onToggle={() => {}} />))
    expect(screen.getByText(/已被覆盖|superseded/)).toBeInTheDocument()
    expect(screen.getByRole('checkbox')).toBeEnabled()
  })

  it('渲染 diff 的增删行', () => {
    render(wrap(
      <DriftCard
        event={ev({ diff: '@@ -1 +1 @@\n-旧\n+新\n' })}
        selected={false}
        onToggle={() => {}}
      />,
    ))
    expect(screen.getByText('旧')).toBeInTheDocument()
    expect(screen.getByText('新')).toBeInTheDocument()
  })
})

describe('DriftCard 绑定漂移', () => {
  it('绑定漂移的收编勾选框置灰并说明原因', () => {
    render(
      wrap(
        <DriftCard
          event={ev({
            path: '.claude/settings.json',
            binding_drift: true,
            binding_url: 'https://api.moonshot.cn/anthropic',
          })}
          selected={false}
          onToggle={() => {}}
        />,
      ),
    )
    expect(screen.getByRole('checkbox')).toBeDisabled()
    // 地址那一行三档都要显示
    expect(screen.getByText(/https:\/\/api\.moonshot\.cn\/anthropic/)).toBeInTheDocument()
  })

  it('第三档（反查都不命中）显示兜底文案', () => {
    render(
      wrap(
        <DriftCard
          event={ev({
            path: '.claude/settings.json',
            binding_drift: true,
            binding_url: 'https://某中转.test/v1',
          })}
          bindingMatch={{
            url: 'https://某中转.test/v1',
            match: { kind: 'none', exact: false },
          }}
          selected={false}
          onToggle={() => {}}
        />,
      ),
    )
    expect(screen.getByText(/无法识别/)).toBeInTheDocument()
  })

  it('第一档（命中已有 Provider）给「改成它」', () => {
    render(
      wrap(
        <DriftCard
          event={ev({
            path: '.claude/settings.json',
            binding_drift: true,
            binding_url: 'https://api.moonshot.cn/anthropic',
          })}
          bindingMatch={{
            url: 'https://api.moonshot.cn/anthropic',
            match: {
              kind: 'provider',
              exact: true,
              provider_id: 'p1',
              provider_name: 'Kimi 官方',
            },
          }}
          selected={false}
          onToggle={() => {}}
          onRebind={() => {}}
        />,
      ),
    )
    expect(screen.getByRole('button', { name: /改成它/ })).toBeInTheDocument()
  })

  it('普通漂移的勾选框照常可用', () => {
    render(wrap(<DriftCard event={ev({ path: 'CLAUDE.md' })} selected={false} onToggle={() => {}} />))
    expect(screen.getByRole('checkbox')).toBeEnabled()
  })
})
