import { describe, expect, it } from 'vitest'
import { i18n } from '@lingui/core'
import {
  adoptBlockers, findConflicts, groupByMachine, groupByPath,
  needsReview, parseUnifiedDiff, type DriftEvent,
} from '@/lib/inbox'

i18n.load('zh', {})
i18n.activate('zh')

function ev(over: Partial<DriftEvent>): DriftEvent {
  return {
    id: 'e1', machine: 'm1', config_set: 's1', path: 'a', kind: 'modified',
    state: 'open', diff: '', truncated: false, restore_partial: false,
    binding_drift: false, binding_url: '',
    created: '2026-07-31T12:00:00Z',
    ...over,
  }
}

describe('分组', () => {
  it('按机器分组，组内按路径排序', () => {
    const groups = groupByMachine([
      ev({ id: '1', machine: 'm2', path: 'z' }),
      ev({ id: '2', machine: 'm1', path: 'b' }),
      ev({ id: '3', machine: 'm1', path: 'a' }),
    ])
    expect(groups.map((g) => g.machine)).toEqual(['m1', 'm2'])
    expect(groups[0].events.map((e) => e.path)).toEqual(['a', 'b'])
  })

  it('按路径分组，用于跨机器对比', () => {
    const groups = groupByPath([
      ev({ id: '1', machine: 'm1', path: 'a' }),
      ev({ id: '2', machine: 'm2', path: 'a' }),
      ev({ id: '3', machine: 'm1', path: 'b' }),
    ])
    expect(groups[0].path).toBe('a')
    expect(groups[0].events).toHaveLength(2)
  })
})

describe('findConflicts', () => {
  // 两台机器对同一文件的改动，静默取其一是最容易让人丢工作成果的操作
  it('同路径不同机器算冲突', () => {
    const conflicts = findConflicts([
      ev({ id: '1', machine: 'm1', path: 'a' }),
      ev({ id: '2', machine: 'm2', path: 'a' }),
    ])
    expect(conflicts).toHaveLength(1)
    expect(conflicts[0].path).toBe('a')
    expect(conflicts[0].events).toHaveLength(2)
  })

  it('同路径同机器不算冲突', () => {
    expect(findConflicts([
      ev({ id: '1', machine: 'm1', path: 'a' }),
      ev({ id: '2', machine: 'm1', path: 'b' }),
    ])).toEqual([])
  })
})

describe('adoptBlockers', () => {
  it('干净的选择没有阻止理由', () => {
    expect(adoptBlockers([ev({ id: '1' }), ev({ id: '2', path: 'b' })])).toEqual([])
  })

  it('跨机器同路径冲突被阻止', () => {
    const blockers = adoptBlockers([
      ev({ id: '1', machine: 'm1', path: 'a' }),
      ev({ id: '2', machine: 'm2', path: 'a' }),
    ])
    expect(blockers).toHaveLength(1)
    expect(blockers[0]).toContain('a')
  })

  it('跨配置集被阻止', () => {
    const blockers = adoptBlockers([
      ev({ id: '1', config_set: 's1' }),
      ev({ id: '2', config_set: 's2', path: 'b' }),
    ])
    expect(blockers.some((b) => b.includes('配置集'))).toBe(true)
  })

  it('truncated 无法收编——内容根本没上来', () => {
    const blockers = adoptBlockers([ev({ id: '1', truncated: true })])
    expect(blockers).toHaveLength(1)
  })

  it('空选择被阻止', () => {
    expect(adoptBlockers([])).toHaveLength(1)
  })
})

describe('needsReview', () => {
  it('restore_partial 的条目要先人工确认，但不是阻止', () => {
    const selected = [ev({ id: '1', restore_partial: true }), ev({ id: '2', path: 'b' })]
    expect(needsReview(selected).map((e) => e.id)).toEqual(['1'])
    expect(adoptBlockers(selected)).toEqual([])
  })
})

describe('parseUnifiedDiff', () => {
  it('分出增删与上下文', () => {
    const lines = parseUnifiedDiff(
      '--- a（基线）\n+++ a（本机）\n@@ -1,2 +1,2 @@\n 上下文\n-旧的\n+新的\n',
    )
    expect(lines.filter((l) => l.type === 'add').map((l) => l.text)).toEqual(['新的'])
    expect(lines.filter((l) => l.type === 'del').map((l) => l.text)).toEqual(['旧的'])
    expect(lines.filter((l) => l.type === 'ctx').map((l) => l.text)).toEqual(['上下文'])
    expect(lines.filter((l) => l.type === 'meta')).toHaveLength(3)
  })

  it('空 diff 得到空数组', () => {
    expect(parseUnifiedDiff('')).toEqual([])
  })

  // 内容里以 +/- 开头的行不能被误判成增删标记
  it('去掉标记后保留原文', () => {
    const lines = parseUnifiedDiff('@@ -1 +1 @@\n+- 列表项\n')
    expect(lines.filter((l) => l.type === 'add')[0].text).toBe('- 列表项')
  })
})

describe('绑定漂移', () => {
  it('绑定漂移不能收编', () => {
    const e = ev({ id: 'e1', path: '.claude/settings.json', binding_drift: true })
    expect(adoptBlockers([e]).join(' ')).toContain('绑定')
  })

  it('普通漂移不受影响', () => {
    expect(adoptBlockers([ev({ id: 'e1' })])).toEqual([])
  })
})
