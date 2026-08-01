# 子计划 16 · 前端（阶段二）：收件箱、三方对比、机器详情

**前置**：14、15
**读这份之前先读** [00-overview.md](00-overview.md)。

**交付物**：收件箱页（按机器/文件分组、diff 预览、收编/恢复/忽略、多选合并收编、「已被覆盖」筛选）、跨机器冲突的三方对比、机器详情补全（对齐状态、apply 回执历史、机器变量、本机漂移、`degraded` 红色告警与解除）。

沿用子计划 10 立的规矩：**只测纯逻辑**，Vitest + RTL，不测 realtime 订阅与 PB SDK 交互。

---

### Task 1: 收件箱的纯逻辑

**Files:**
- Create: `src/lib/inbox.ts` + `src/lib/inbox.test.ts`

**Interfaces:**
- Produces:
  ```ts
  export type DriftState = 'open' | 'adopted' | 'restored' | 'ignored' | 'superseded'
  export type DriftKind = 'added' | 'modified' | 'deleted'

  export interface DriftEvent {
    id: string
    machine: string
    config_set: string
    path: string
    kind: DriftKind
    state: DriftState
    diff: string
    truncated: boolean
    restore_partial: boolean
    created: string
  }

  export interface Conflict { path: string; events: DriftEvent[] }

  export function groupByMachine(events: DriftEvent[]): { machine: string; events: DriftEvent[] }[]
  export function groupByPath(events: DriftEvent[]): { path: string; events: DriftEvent[] }[]
  export function findConflicts(selected: DriftEvent[]): Conflict[]
  export function needsReview(selected: DriftEvent[]): DriftEvent[]
  export function adoptBlockers(selected: DriftEvent[]): string[]
  export function parseUnifiedDiff(diff: string): DiffLine[]
  export interface DiffLine { type: 'add' | 'del' | 'ctx' | 'meta'; text: string }
  ```

`adoptBlockers` 返回**阻止收编的理由**（空数组 = 可以收编）：跨配置集、跨机器同路径冲突、含 `truncated` 条目。`needsReview` 单独返回含 `restore_partial` 的条目——它们不是阻止，而是要先勾一遍确认。

- [ ] **Step 1: 写失败的测试**

Create `src/lib/inbox.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import {
  adoptBlockers, findConflicts, groupByMachine, groupByPath,
  needsReview, parseUnifiedDiff, type DriftEvent,
} from '@/lib/inbox'

function ev(over: Partial<DriftEvent>): DriftEvent {
  return {
    id: 'e1', machine: 'm1', config_set: 's1', path: 'a', kind: 'modified',
    state: 'open', diff: '', truncated: false, restore_partial: false,
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
```

- [ ] **Step 2: 跑测试确认失败并实现**

Run: `cd hub/internal/site && npm test`

`adoptBlockers` 的实现要点：返回的是**中文的理由句子**（经 Lingui），前端直接列出来给用户看，而不是错误码。

- [ ] **Step 3: 提交**

```bash
git add hub/internal/site/src/lib/
git commit -m "feat: 收件箱的分组、冲突判定与 diff 解析"
```

---

### Task 2: 三方对比

**Files:**
- Create: `src/lib/threeWay.ts` + `.test.ts`
- Create: `src/components/ThreeWayCompare.tsx`

**Interfaces:**
- Produces:
  ```ts
  export interface Side { label: string; content: string }
  export interface ThreeWayRow {
    base: string | null
    left: string | null
    right: string | null
    /** 三边是否一致 */
    same: boolean
    /** left 与 right 是否互相冲突（都改了同一行且改法不同） */
    conflicting: boolean
  }
  export function threeWayRows(base: string, left: string, right: string): ThreeWayRow[]
  ```

**为什么要它（spec §8.3）**：两台机器对同一文件的改动，UI **直接阻止提交**，要求先看三方对比（基线 / 机器 A / 机器 B）再选一个。做成硬阻止而不是警告，因为静默取其一是最容易让人丢工作成果的操作。

- [ ] **Step 1: 写失败的测试**

```ts
import { describe, expect, it } from 'vitest'
import { threeWayRows } from '@/lib/threeWay'

describe('threeWayRows', () => {
  it('三边一致的行标记为 same', () => {
    const rows = threeWayRows('一样\n', '一样\n', '一样\n')
    expect(rows).toHaveLength(1)
    expect(rows[0].same).toBe(true)
    expect(rows[0].conflicting).toBe(false)
  })

  it('只有一边改了不算冲突', () => {
    const rows = threeWayRows('原文\n', '左边改了\n', '原文\n')
    expect(rows[0].same).toBe(false)
    expect(rows[0].conflicting).toBe(false)
  })

  it('两边都改且改法不同才算冲突', () => {
    const rows = threeWayRows('原文\n', 'A 的改法\n', 'B 的改法\n')
    expect(rows[0].conflicting).toBe(true)
  })

  it('两边改成一样的不算冲突', () => {
    const rows = threeWayRows('原文\n', '同样的改法\n', '同样的改法\n')
    expect(rows[0].conflicting).toBe(false)
    expect(rows[0].same).toBe(false)
  })

  it('新增行：base 为 null', () => {
    const rows = threeWayRows('', '新增\n', '')
    expect(rows[0].base).toBeNull()
    expect(rows[0].left).toBe('新增')
  })

  it('删除行：对应侧为 null', () => {
    const rows = threeWayRows('要删的\n', '', '要删的\n')
    expect(rows[0].left).toBeNull()
    expect(rows[0].right).toBe('要删的')
  })
})
```

- [ ] **Step 2: 实现**

用 jsdiff 分别算 `base→left` 与 `base→right`，再按 base 的行号对齐成一张三列表。

- [ ] **Step 3: 提交**

```bash
git add hub/internal/site/src/lib/ hub/internal/site/src/components/
git commit -m "feat: 三方对比"
```

---

### Task 3: 收件箱页

**Files:**
- Create: `src/pages/Inbox.tsx`
- Create: `src/components/DriftCard.tsx` + `.test.tsx`
- Create: `src/stores/drift.ts`
- Modify: `src/router.tsx`、`src/components/Sidebar.tsx`（把 `<Placeholder milestone="M1" />` 换成真页面，侧栏加未处理数量角标）

**要点**

- 默认只显示 `state = 'open'`；筛选器提供「已被覆盖」（`superseded`）、「已收编」、「已恢复」、「已忽略」。
- 「已被覆盖」的条目仍可看 diff、仍可**重新收编**（spec §7.7 的第二处红利）。
- 多选后底部出现操作条：收编 / 恢复 / 忽略；`adoptBlockers` 非空时收编按钮禁用并列出理由。
- `needsReview` 非空时弹一个复核对话框，逐条显示 diff 并要求勾选确认，然后调 `AdoptDriftReviewed`。
- `truncated` 的条目卡片上显示醒目提示：「该文件含未能安全脱敏的凭据，请在 Web 上手工处理」，并禁用收编。

- [ ] **Step 1: 写 DriftCard 的测试**

```tsx
describe('DriftCard', () => {
  it('truncated 的条目禁用收编并给出说明', () => {
    render(<DriftCard event={ev({ truncated: true })} selected={false} onToggle={() => {}} />)
    expect(screen.getByText(/未能安全脱敏|could not be safely/)).toBeInTheDocument()
    expect(screen.getByRole('checkbox')).toBeDisabled()
  })

  it('restore_partial 的条目给出复核提示但可选', () => {
    render(<DriftCard event={ev({ restore_partial: true })} selected={false} onToggle={() => {}} />)
    expect(screen.getByText(/需人工复核|needs review/)).toBeInTheDocument()
    expect(screen.getByRole('checkbox')).toBeEnabled()
  })

  it('superseded 的条目标注「已被覆盖」且仍可重新收编', () => {
    render(<DriftCard event={ev({ state: 'superseded' })} selected={false} onToggle={() => {}} />)
    expect(screen.getByText(/已被覆盖|superseded/)).toBeInTheDocument()
    expect(screen.getByRole('checkbox')).toBeEnabled()
  })

  it('渲染 diff 的增删行', () => {
    render(<DriftCard event={ev({ diff: '@@ -1 +1 @@\n-旧\n+新\n' })} selected={false} onToggle={() => {}} />)
    expect(screen.getByText('旧')).toBeInTheDocument()
    expect(screen.getByText('新')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 实现并跑通**

Run: `cd hub/internal/site && npm test`

- [ ] **Step 3: 提交**

```bash
git add hub/internal/site/src/
git commit -m "feat: 收件箱页与漂移卡片"
```

---

### Task 4: 机器详情补全

**Files:**
- Modify: `src/pages/MachineDetail.tsx`
- Create: `src/components/DegradedBanner.tsx` + `.test.tsx`
- Create: `src/components/ApplyHistory.tsx`

**补上（spec §11）**：配置对齐状态、apply 回执历史（从 `events` 里筛 `apply.*`）、机器变量编辑、本机漂移列表、`degraded` 红色告警与解除按钮。

**`degraded` 的文案要说清后果**：「本机的一次配置应用失败，且自动回滚也未成功。为避免进一步破坏，orciny 已停止向这台机器应用任何配置。请先在机器上确认 `~/.claude` 的状态，再点击解除——解除后本机会转为「先看看」模式，差异会进收件箱而不是直接覆盖。」

- [ ] **Step 1: 写测试**

```tsx
describe('DegradedBanner', () => {
  it('非 degraded 时不渲染', () => {
    const { container } = render(<DegradedBanner state="aligned" lastError="" onClear={() => {}} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('degraded 时显示原因与后果', () => {
    render(<DegradedBanner state="degraded" lastError="permission denied" onClear={() => {}} />)
    expect(screen.getByText(/permission denied/)).toBeInTheDocument()
    expect(screen.getByText(/停止向这台机器应用|stopped applying/)).toBeInTheDocument()
    expect(screen.getByText(/先看看|survey/)).toBeInTheDocument()
  })

  // 解除是有后果的操作，不能一键就走
  it('解除需要二次确认', async () => {
    const onClear = vi.fn()
    render(<DegradedBanner state="degraded" lastError="x" onClear={onClear} />)
    await userEvent.click(screen.getByRole('button', { name: /解除|Clear/ }))
    expect(onClear).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: /确认|Confirm/ }))
    expect(onClear).toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: 实现并跑通**

Run: `cd hub/internal/site && npm test`

- [ ] **Step 3: 提交**

```bash
git add hub/internal/site/src/
git commit -m "feat: 机器详情补全与 degraded 告警"
```

---

### Task 5: 前端收尾

- [ ] **Step 1: 清理占位**

确认侧栏里 M1 范围的页面都不再是 `<Placeholder milestone="M1" />`；M2 的（用量、订阅）保持占位。

- [ ] **Step 2: 语言包**

```bash
cd hub/internal/site && npm run extract && npm run compile
```

检查 `src/locales/en.po` 与 `zh.po` 里没有空翻译（新加的字符串都要填英文）。

- [ ] **Step 3: 全量检查**

```bash
cd hub/internal/site && npx tsc --noEmit && npm test && npm run build
cd - && git checkout -- hub/internal/site/dist/index.html
go test -tags=testing ./...
```

- [ ] **Step 4: 提交**

```bash
git add hub/internal/site/
git commit -m "chore: 补齐 M1 语言包与占位清理"
```
