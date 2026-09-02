import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { ReviewDialog, buildOverrideTargets } from '@/pages/Inbox'
import type { DriftEvent } from '@/lib/inbox'
import type { DriftEventRecord } from '@/types/collections'

i18n.load('zh', {})
i18n.activate('zh')

function ev(over: Partial<DriftEvent>): DriftEvent {
  return {
    id: 'e1', machine: 'm1', config_set: 's1', path: '.claude/settings.json',
    kind: 'modified', state: 'open', diff: '', truncated: false,
    restore_partial: true, binding_drift: false, binding_url: '',
    created: '2026-08-24T02:15:09Z',
    ...over,
  }
}

/** 受控组件，测的是真实的勾选联动，不是回调被调了几次。 */
function Harness({ events, onConfirm = vi.fn() }: { events: DriftEvent[]; onConfirm?: () => void }) {
  const [checked, setChecked] = useState<Set<string>>(new Set())
  return (
    <I18nProvider i18n={i18n}>
      <ReviewDialog
        events={events}
        checked={checked}
        busy={false}
        onToggle={(id) =>
          setChecked((prev) => {
            const next = new Set(prev)
            if (next.has(id)) next.delete(id)
            else next.add(id)
            return next
          })
        }
        onCancel={() => {}}
        onConfirm={onConfirm}
      />
    </I18nProvider>
  )
}

describe('ReviewDialog', () => {
  it('每条只有一个复选框——两个会让人不知道该勾哪个', () => {
    render(<Harness events={[ev({})]} />)
    expect(screen.getAllByRole('checkbox')).toHaveLength(1)
  })

  it('勾上条目后「确认收编」才可点', async () => {
    const user = userEvent.setup()
    const onConfirm = vi.fn()
    render(<Harness events={[ev({})]} onConfirm={onConfirm} />)

    const confirm = screen.getByRole('button', { name: /确认收编|Adopt/ })
    expect(confirm).toBeDisabled()

    await user.click(screen.getByRole('checkbox'))
    expect(screen.getByRole('checkbox')).toBeChecked()
    expect(confirm).toBeEnabled()

    await user.click(confirm)
    expect(onConfirm).toHaveBeenCalledOnce()
  })

  it('多条要逐条勾，勾不全不许收编', async () => {
    const user = userEvent.setup()
    render(<Harness events={[ev({}), ev({ id: 'e2', path: '.claude/CLAUDE.md' })]} />)

    const confirm = screen.getByRole('button', { name: /确认收编|Adopt/ })
    await user.click(screen.getByLabelText('.claude/settings.json'))
    expect(confirm).toBeDisabled()

    await user.click(screen.getByLabelText('.claude/CLAUDE.md'))
    expect(confirm).toBeEnabled()
  })
})

describe('buildOverrideTargets', () => {
  const rec = (over: Partial<DriftEventRecord> = {}): DriftEventRecord => ({
    id: 'e1', machine: 'm1', config_set: 's1', path: '.claude/settings.json',
    kind: 'modified', base_hash: 'h-base', current_blob: 'b1', mode: 420,
    diff: '', restore_partial: false, truncated: false, binding_drift: false,
    binding_url: '', state: 'open', resolved_revision: '', resolved_at: '',
    created: '2026-09-01T00:00:00Z', updated: '2026-09-01T00:00:00Z',
    ...over,
  })

  it('两侧都是 JSON 对象 → 切成 selector 列表', () => {
    const [target] = buildOverrideTargets(
      [rec()],
      { 'h-base': '{"env":{"A":"中台"}}' },
      { e1: '{"env":{"A":"本机"}}' },
    )
    expect(target.points?.map((p) => p.selector)).toEqual(['env.A'])
    expect(target.hunkCount).toBe(0)
  })

  it('非 JSON → points 为 null，按 @@ 块数给 hunkCount', () => {
    const diff = ['@@ -1,3 +1,3 @@', '-a', '+A'].join('\n')
    const [target] = buildOverrideTargets(
      [rec({ diff })],
      { 'h-base': '# 标题' },
      { e1: '# 新标题' },
    )
    expect(target.points).toBeNull()
    expect(target.hunkCount).toBe(1)
  })
})
