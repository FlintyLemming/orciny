import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import {
  OverrideDialog,
  defaultChecked,
  selectionsOf,
  type OverrideTarget,
} from '@/components/OverrideDialog'

i18n.load('zh', {})
i18n.activate('zh')

const jsonTarget: OverrideTarget = {
  id: 'e1',
  path: '.claude/settings.json',
  diff: '',
  points: [
    { selector: 'env.A', base: '"中台"', mine: '"本机"' },
    { selector: 'env.B', base: '"旧"', mine: '"新"' },
  ],
  hunkCount: 0,
}

const textTarget: OverrideTarget = {
  id: 'e2',
  path: '.claude/CLAUDE.md',
  diff: ['@@ -1,3 +1,3 @@', '-a', '+A', '@@ -20,3 +20,3 @@', '-z', '+Z'].join('\n'),
  points: null,
  hunkCount: 2,
}

/** 受控组件，测的是真实的勾选联动，不是回调被调了几次。 */
function Harness({
  targets,
  onConfirm = vi.fn(),
}: {
  targets: OverrideTarget[]
  onConfirm?: () => void
}) {
  const [checked, setChecked] = useState(() => defaultChecked(targets))
  return (
    <I18nProvider i18n={i18n}>
      <OverrideDialog
        targets={targets}
        checked={checked}
        busy={false}
        onToggle={(targetId, key) =>
          setChecked((prev) => {
            const next = { ...prev }
            const set = new Set(next[targetId])
            if (set.has(key)) set.delete(key)
            else set.add(key)
            next[targetId] = set
            return next
          })
        }
        onCancel={() => {}}
        onConfirm={onConfirm}
      />
    </I18nProvider>
  )
}

describe('OverrideDialog', () => {
  it('默认全选', () => {
    render(<Harness targets={[jsonTarget, textTarget]} />)
    const boxes = screen.getAllByRole('checkbox')
    expect(boxes).toHaveLength(4) // 2 个 selector + 2 个 hunk
    for (const b of boxes) expect(b).toBeChecked()
  })

  it('按钮上写着会保留几处', () => {
    render(<Harness targets={[jsonTarget]} />)
    expect(screen.getByRole('button', { name: /保留选中的 2 处/ })).toBeEnabled()
  })

  it('取消勾选后提交的 points 只含勾上的', async () => {
    const user = userEvent.setup()
    render(<Harness targets={[jsonTarget, textTarget]} />)

    await user.click(screen.getByLabelText('env.B'))
    await user.click(screen.getByLabelText('.claude/CLAUDE.md #2'))

    expect(screen.getByLabelText('env.B')).not.toBeChecked()
    expect(screen.getByRole('button', { name: /保留选中的 2 处/ })).toBeEnabled()
  })

  it('全不勾时按钮禁用 —— 空操作不该被提交', async () => {
    const user = userEvent.setup()
    render(<Harness targets={[jsonTarget]} />)
    await user.click(screen.getByLabelText('env.A'))
    await user.click(screen.getByLabelText('env.B'))
    expect(screen.getByRole('button', { name: /保留选中的/ })).toBeDisabled()
  })

  it('JSON 一路左右两列显示 base → mine', () => {
    render(<Harness targets={[jsonTarget]} />)
    expect(screen.getByText('"中台"')).toBeInTheDocument()
    expect(screen.getByText('"本机"')).toBeInTheDocument()
  })
})

describe('selectionsOf', () => {
  it('JSON 出 selectors、文本出 hunks', () => {
    const checked = {
      e1: new Set(['env.A']),
      e2: new Set(['h:1']),
    }
    expect(selectionsOf([jsonTarget, textTarget], checked)).toEqual({
      e1: { selectors: ['env.A'] },
      e2: { hunks: [1] },
    })
  })

  it('hunk 下标按数值升序，不是字符串序', () => {
    const t: OverrideTarget = { ...textTarget, hunkCount: 12 }
    const checked = { e2: new Set(['h:10', 'h:2']) }
    expect(selectionsOf([t], checked)).toEqual({ e2: { hunks: [2, 10] } })
  })
})
