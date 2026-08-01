# 子计划 10 · 前端（阶段一）：编辑器、发布、版本、凭据、指派、导入向导

**前置**：04、08、09
**读这份之前先读** [00-overview.md](00-overview.md)。

**交付物**：Vitest + React Testing Library 体系、HTTP 路由层、配置集列表与详情（文件树 + CodeMirror + 校验 + 占位符补全 + 草稿状态机）、发布/版本历史/回滚、凭据页、机器变量、指派对话框、导入向导四步。

**M0 的欠账在此兑现**：M0 spec §10.3 明示「M1 引入编辑器（有真实的校验逻辑和状态机）时再建前端测试体系」。

**只测纯逻辑**（spec §10.2）：占位符解析与告警、JSON 校验与错误定位、草稿状态机、diff 分组、敏感项掩码。**不测** realtime 订阅与 PB SDK 交互——那是 M0 已定的取舍，靠真机验收覆盖。

**选型（spec §11）**：CodeMirror 6（按需引入，JSON/Markdown 语法 + lint）+ jsdiff（编辑器里的实时草稿 diff）。不选 Monaco：3–5 MB 打包体积会顶到 hub 二进制 < 30 MB 的硬目标。

---

### Task 1: HTTP 路由层

**Files:**
- Create: `hub/internal/routes/config.go`
- Modify: `hub/internal/routes/routes.go`
- Modify: `hub/hub.go`（`routes.Deps` 传 `Hub` 自身）
- Test: `hub/internal/routes/config_test.go`

**Interfaces:**
- Consumes: `hub/api.go` 的公开入口
- Produces: 00-overview「HTTP API」表里的全部端点，`Bind(apis.RequireSuperuserAuth())`

路由不持有状态，只做编解码与状态码映射。为避免 `routes` import `hub`（会成环），`Deps` 里放一个接口：

```go
// Admin 是路由层需要的管理动作，由 *hub.Hub 实现。
type Admin interface {
    AssignConfigSet(machineID, setID, mode string) error
    PublishConfigSet(setID, note string) (string, error)
    RollbackConfigSet(setID, revisionID string) (string, error)
    CreateCredential(name, value, note string) error
    RotateCredential(name, value string) error
    DeleteCredential(name string) error
    StartImport(machineID string) (string, string, error)
}
```

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/routes/config_test.go`，用该目录既有的 `routes_test.go` 起 router 的方式，断言三件事：

```go
// 全部管理端点必须要求 superuser（M0 spec §5.3 的纪律）。
func TestConfigRoutesRequireSuperuser(t *testing.T) {
	srv := newRouterServer(t, routes.Deps{Admin: &fakeAdmin{}})
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/orciny/config-sets", `{"name":"x"}`},
		{"POST", "/api/orciny/config-sets/abc/publish", `{"note":"x"}`},
		{"POST", "/api/orciny/config-sets/abc/rollback", `{"revision":"r1"}`},
		{"POST", "/api/orciny/assignments", `{"machine":"m","config_set":"s","mode":"apply"}`},
		{"POST", "/api/orciny/credentials", `{"name":"k","value":"sk-12345678"}`},
		{"POST", "/api/orciny/machines/m/import", `{}`},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "%s %s", c.method, c.path)
	}
}

// mode 必须显式给出，缺了就是 400 —— UI 上是强制二选一（spec §7.6）。
func TestAssignRejectsMissingMode(t *testing.T) {
	admin := &fakeAdmin{}
	srv := newRouterServer(t, routes.Deps{Admin: admin})
	rec := doSuperuser(t, srv, "POST", "/api/orciny/assignments",
		`{"machine":"m","config_set":"s"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Empty(t, admin.assigns)
}

// 业务错误映射成 4xx，而不是 500 —— 前端要拿它做提示。
func TestCredentialInUseMapsTo409(t *testing.T) {
	admin := &fakeAdmin{deleteErr: credentials.ErrInUse}
	srv := newRouterServer(t, routes.Deps{Admin: admin})
	rec := doSuperuser(t, srv, "DELETE", "/api/orciny/credentials/k", "")
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "引用")
}
```

`fakeAdmin` 记录调用并可注入错误；`newRouterServer` / `doSuperuser` 按该目录既有的测试辅助补出来（`doSuperuser` 用 `tests.TestApp` 生成的 superuser token 打请求）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/routes/ -run Config -v`
Expected: FAIL，`routes.Deps.Admin undefined`

- [ ] **Step 3: 实现**

Create `hub/internal/routes/config.go`。要点：

- 全部端点 `Bind(apis.RequireSuperuserAuth())`。
- 错误映射：`credentials.ErrInUse` → 409；`ErrShortValue` / `ErrBadName` / mode 非法 / JSON 解析失败 → 400；`ErrNotFound` / `ErrNoAssignment` → 404；`machines.ErrOffline` → 503（「机器不在线，请稍后再试」）；其余 → 500。
- `GET /api/orciny/blobs/{hash}` 直接吐原始内容，`Content-Type: text/plain; charset=utf-8`，供编辑器读文件。
- `GET /api/orciny/config-sets/{id}/diff?from=&to=`：`from`/`to` 取 revision id 或字面量 `draft`，返回 `{"changes": []FileChange, "diffs": {path: unifiedDiff}}`。

`routes.Register` 里注册这一组；`hub.go` 把 `Admin: h` 传进 `routes.Deps`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/
git commit -m "feat: 配置集与凭据的管理 API"
```

---

### Task 2: Vitest 体系

**Files:**
- Modify: `hub/internal/site/package.json`
- Create: `hub/internal/site/vitest.config.ts`
- Create: `hub/internal/site/src/test/setup.ts`
- Create: `hub/internal/site/src/lib/placeholder.test.ts`（首个用例，验证体系跑得起来）
- Create: `hub/internal/site/src/lib/placeholder.ts`
- Modify: `Makefile`、`.github/workflows/ci.yml`

**Interfaces:**
- Produces:
  ```ts
  // src/lib/placeholder.ts —— 与 Go 侧 protocol/placeholder.go 同一套词法
  export type RefKind = 'cred' | 'var' | 'machine'
  export interface Ref { kind: RefKind; name: string }
  export interface ParseResult { refs: Ref[]; errors: string[] }
  export function parsePlaceholders(text: string): ParseResult
  export function undefinedRefs(text: string, known: Set<string>): Ref[]
  export function completions(prefix: string, known: string[]): string[]
  export const MACHINE_KEYS: readonly string[]
  ```

- [ ] **Step 1: 装依赖并配置**

```bash
cd hub/internal/site
npm i -D vitest @vitest/coverage-v8 jsdom @testing-library/react @testing-library/dom @testing-library/jest-dom
npm i codemirror @codemirror/state @codemirror/view @codemirror/lang-json @codemirror/lang-markdown @codemirror/lint @codemirror/autocomplete diff
npm i -D @types/diff
```

`package.json` 的 `scripts` 加：

```json
    "test": "vitest run",
    "test:watch": "vitest"
```

Create `vitest.config.ts`：

```ts
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import path from 'node:path'

// 与 vite.config.ts 分开：测试不需要 lingui / tailwind 插件，
// 它们在 jsdom 下只会拖慢启动并引入无关的失败点。
export default defineConfig({
  plugins: [react()],
  resolve: { alias: { '@': path.resolve(__dirname, 'src') } },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
  },
})
```

Create `src/test/setup.ts`：

```ts
import '@testing-library/jest-dom/vitest'
```

`Makefile` 的 `test` 目标改成：

```make
test:
	go test -tags=testing ./...
	cd hub/internal/site && npm test
```

`.github/workflows/ci.yml` 的前端步骤追加 `npm test`（在 `npm ci` 之后、`npm run build` 之前）。

- [ ] **Step 2: 写失败的测试**

Create `src/lib/placeholder.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import { completions, parsePlaceholders, undefinedRefs } from '@/lib/placeholder'

describe('parsePlaceholders', () => {
  it('提取三种引用', () => {
    const { refs, errors } = parsePlaceholders(
      'a{{cred.my-key_1}}b{{var.ws}}c{{machine.hostname}}',
    )
    expect(errors).toEqual([])
    expect(refs.map((r) => `${r.kind}.${r.name}`)).toEqual([
      'cred.my-key_1',
      'var.ws',
      'machine.hostname',
    ])
  })

  it('去重', () => {
    const { refs } = parsePlaceholders('{{cred.a}}{{cred.a}}')
    expect(refs).toHaveLength(1)
  })

  // 转义规则必须与 Go 侧逐位一致，否则编辑器的告警与 hub 的发布校验会打架
  it('{{{{ 是字面量，不产生引用', () => {
    const { refs, errors } = parsePlaceholders('写作 {{{{cred.x}} 表示字面量')
    expect(refs).toEqual([])
    expect(errors).toEqual([])
  })

  it('报出语法错误', () => {
    expect(parsePlaceholders('{{cred.x').errors).toHaveLength(1)
    expect(parsePlaceholders('{{cred.}}').errors).toHaveLength(1)
    expect(parsePlaceholders('{{unknown.x}}').errors).toHaveLength(1)
    expect(parsePlaceholders('{{machine.secret}}').errors).toHaveLength(1)
  })
})

describe('undefinedRefs', () => {
  it('只报未定义的，machine.* 永远算已定义', () => {
    const got = undefinedRefs(
      '{{cred.known}}{{cred.gone}}{{var.ws}}{{machine.os}}',
      new Set(['cred.known', 'var.ws']),
    )
    expect(got.map((r) => `${r.kind}.${r.name}`)).toEqual(['cred.gone'])
  })
})

describe('completions', () => {
  it('按前缀过滤', () => {
    expect(completions('cred.an', ['cred.anthropic_key', 'cred.other', 'var.ws']))
      .toEqual(['cred.anthropic_key'])
  })

  it('空前缀给出全部候选加内置的 machine.*', () => {
    const got = completions('', ['cred.k'])
    expect(got).toContain('cred.k')
    expect(got).toContain('machine.hostname')
  })
})
```

- [ ] **Step 3: 跑测试确认失败**

Run: `cd hub/internal/site && npm test`
Expected: FAIL，`Cannot find module '@/lib/placeholder'`

- [ ] **Step 4: 实现**

Create `src/lib/placeholder.ts`：

```ts
/**
 * 占位符的前端词法。
 *
 * 与 Go 侧 `protocol/placeholder.go` 是同一套规则的两份实现——这是有意为之
 * 的重复：编辑器要在**内容还没提交到 hub** 的时候就给出告警与补全，
 * 而那时没有任何后端可以问。两份实现的一致性靠双方各自的测试用同一组
 * 例子来保证（转义、非法名、machine 白名单）。
 */

export type RefKind = 'cred' | 'var' | 'machine'

export interface Ref {
  kind: RefKind
  name: string
}

export interface ParseResult {
  refs: Ref[]
  errors: string[]
}

/** {{machine.*}} 允许的全部名字，与 Go 侧 protocol.MachineKeys 一致。 */
export const MACHINE_KEYS = ['name', 'hostname', 'os', 'arch'] as const

const NAME_RE = /^[A-Za-z0-9_-]+$/

export function parsePlaceholders(text: string): ParseResult {
  const refs: Ref[] = []
  const errors: string[] = []
  const seen = new Set<string>()

  let i = 0
  while (i < text.length) {
    if (!text.startsWith('{{', i)) {
      i++
      continue
    }
    if (text.startsWith('{{{{', i)) {
      i += 4 // 字面的 "{{"
      continue
    }
    const end = text.indexOf('}}', i + 2)
    if (end < 0) {
      errors.push(`偏移 ${i} 处有未闭合的 {{`)
      break
    }
    const body = text.slice(i + 2, end)
    const dot = body.indexOf('.')
    const prefix = dot < 0 ? '' : body.slice(0, dot)
    const name = dot < 0 ? '' : body.slice(dot + 1)

    if (dot < 0) {
      errors.push(`{{${body}}} 缺少 . 分隔`)
    } else if (!NAME_RE.test(name)) {
      errors.push(`{{${body}}} 的名字非法（只允许 [A-Za-z0-9_-]）`)
    } else if (prefix === 'cred' || prefix === 'var') {
      push(refs, seen, { kind: prefix, name })
    } else if (prefix === 'machine') {
      if ((MACHINE_KEYS as readonly string[]).includes(name)) {
        push(refs, seen, { kind: 'machine', name })
      } else {
        errors.push(`machine.${name} 不是内置名（只有 ${MACHINE_KEYS.join(' / ')}）`)
      }
    } else {
      errors.push(`未知前缀 ${prefix}`)
    }
    i = end + 2
  }
  return { refs, errors }
}

function push(refs: Ref[], seen: Set<string>, r: Ref) {
  const key = `${r.kind}.${r.name}`
  if (seen.has(key)) return
  seen.add(key)
  refs.push(r)
}

/** known 的元素形如 "cred.foo" / "var.bar"。machine.* 永远算已定义。 */
export function undefinedRefs(text: string, known: Set<string>): Ref[] {
  return parsePlaceholders(text).refs.filter(
    (r) => r.kind !== 'machine' && !known.has(`${r.kind}.${r.name}`),
  )
}

/** 补全候选：已有的凭据与变量，加上内置的 machine.*。 */
export function completions(prefix: string, known: string[]): string[] {
  const all = [...known, ...MACHINE_KEYS.map((k) => `machine.${k}`)]
  return all.filter((c) => c.startsWith(prefix)).sort()
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `cd hub/internal/site && npm test`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add hub/internal/site/ Makefile .github/
git commit -m "test: 建立前端 Vitest 体系并补占位符词法"
```

---

### Task 3: 纯逻辑模块——JSON 校验、草稿状态机、diff 分组

**Files:**
- Create: `src/lib/jsonLint.ts` + `.test.ts`
- Create: `src/lib/draftState.ts` + `.test.ts`
- Create: `src/lib/diffView.ts` + `.test.ts`

**Interfaces:**
- Produces:
  ```ts
  // jsonLint.ts
  export interface JsonProblem { line: number; column: number; message: string }
  export function lintJson(text: string): JsonProblem[]

  // draftState.ts
  export type DraftState = 'clean' | 'dirty' | 'publishing' | 'failed'
  export type DraftEvent =
    | { type: 'edit' } | { type: 'save' } | { type: 'publish' }
    | { type: 'published' } | { type: 'failed'; error: string }
    | { type: 'discard' }
  export function nextDraftState(s: DraftState, e: DraftEvent): DraftState
  export function canPublish(s: DraftState, problems: number): boolean

  // diffView.ts
  export type ChangeKind = 'added' | 'removed' | 'modified'
  export interface FileChange { path: string; kind: ChangeKind; from_hash: string; to_hash: string }
  export interface ChangeGroup { kind: ChangeKind; label: string; files: FileChange[] }
  export function groupChanges(changes: FileChange[]): ChangeGroup[]
  export function inlineDiff(before: string, after: string): DiffRow[]
  export interface DiffRow { type: 'add' | 'del' | 'ctx'; text: string }
  ```

- [ ] **Step 1: 写失败的测试**

Create `src/lib/jsonLint.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import { lintJson } from '@/lib/jsonLint'

describe('lintJson', () => {
  it('合法 JSON 无问题', () => {
    expect(lintJson('{"a": 1}')).toEqual([])
  })

  it('空内容不算错', () => {
    expect(lintJson('')).toEqual([])
    expect(lintJson('   \n ')).toEqual([])
  })

  // 定位是这个函数存在的理由：只说「JSON 错误」等于没说
  it('给出行列', () => {
    const problems = lintJson('{\n  "a": 1,\n  "b":\n}')
    expect(problems).toHaveLength(1)
    expect(problems[0].line).toBeGreaterThanOrEqual(3)
    expect(problems[0].message).not.toEqual('')
  })

  // 含占位符的 JSON 在编辑器里是常态，不能因此报错
  it('占位符先替成字符串再校验', () => {
    expect(lintJson('{"key": "{{cred.k}}"}')).toEqual([])
    expect(lintJson('{"n": {{var.count}}}')).toEqual([])
  })
})
```

Create `src/lib/draftState.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import { canPublish, nextDraftState } from '@/lib/draftState'

describe('nextDraftState', () => {
  it('编辑让草稿变脏，保存不清脏（脏指的是与 head 有别）', () => {
    expect(nextDraftState('clean', { type: 'edit' })).toBe('dirty')
    expect(nextDraftState('dirty', { type: 'save' })).toBe('dirty')
  })

  it('发布成功回到 clean，失败进 failed', () => {
    expect(nextDraftState('dirty', { type: 'publish' })).toBe('publishing')
    expect(nextDraftState('publishing', { type: 'published' })).toBe('clean')
    expect(nextDraftState('publishing', { type: 'failed', error: 'x' })).toBe('failed')
  })

  it('失败之后继续编辑回到 dirty', () => {
    expect(nextDraftState('failed', { type: 'edit' })).toBe('dirty')
  })

  it('丢弃回到 clean', () => {
    expect(nextDraftState('dirty', { type: 'discard' })).toBe('clean')
    expect(nextDraftState('failed', { type: 'discard' })).toBe('clean')
  })

  // 发布中不接受编辑：内容会与已提交的清单对不上
  it('发布中忽略编辑事件', () => {
    expect(nextDraftState('publishing', { type: 'edit' })).toBe('publishing')
  })
})

describe('canPublish', () => {
  it('有校验问题就不许发布', () => {
    expect(canPublish('dirty', 0)).toBe(true)
    expect(canPublish('dirty', 1)).toBe(false)
    expect(canPublish('publishing', 0)).toBe(false)
    expect(canPublish('clean', 0)).toBe(false)
  })
})
```

Create `src/lib/diffView.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import { groupChanges, inlineDiff } from '@/lib/diffView'

describe('groupChanges', () => {
  it('按类型分组，组内按路径排序', () => {
    const groups = groupChanges([
      { path: 'b', kind: 'modified', from_hash: '1', to_hash: '2' },
      { path: 'a', kind: 'added', from_hash: '', to_hash: '3' },
      { path: 'c', kind: 'modified', from_hash: '4', to_hash: '5' },
      { path: 'd', kind: 'removed', from_hash: '6', to_hash: '' },
    ])
    expect(groups.map((g) => g.kind)).toEqual(['added', 'modified', 'removed'])
    expect(groups[1].files.map((f) => f.path)).toEqual(['b', 'c'])
  })

  it('空组不出现', () => {
    expect(groupChanges([{ path: 'a', kind: 'added', from_hash: '', to_hash: '1' }]))
      .toHaveLength(1)
  })
})

describe('inlineDiff', () => {
  it('给出增删与上下文行', () => {
    const rows = inlineDiff('第一行\n第二行\n', '第一行\n改过的第二行\n')
    expect(rows.some((r) => r.type === 'del' && r.text.includes('第二行'))).toBe(true)
    expect(rows.some((r) => r.type === 'add' && r.text.includes('改过的'))).toBe(true)
    expect(rows.some((r) => r.type === 'ctx' && r.text.includes('第一行'))).toBe(true)
  })

  it('相同内容全是上下文行', () => {
    expect(inlineDiff('一样\n', '一样\n').every((r) => r.type === 'ctx')).toBe(true)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd hub/internal/site && npm test`
Expected: FAIL，三个模块都不存在

- [ ] **Step 3: 实现**

`jsonLint.ts` 的关键点：先把占位符替换成合法 JSON 片段再交给 `JSON.parse`，因为编辑器里的内容常常带占位符：

```ts
/**
 * JSON 校验，带行列定位。
 *
 * 含占位符的 JSON 在编辑器里是常态（{"key": "{{cred.k}}"}），
 * 甚至会出现在值的位置上（{"n": {{var.count}}}）。校验前先把占位符
 * 替换成一个合法的字面量，否则用户每敲一个占位符就看到一片红。
 */
export function lintJson(text: string): JsonProblem[] {
  if (text.trim() === '') return []
  const normalized = text
    .replace(/\{\{\{\{/g, '__ESCAPED__')
    .replace(/\{\{[^}]*\}\}/g, '"__PLACEHOLDER__"')
    .replace(/__ESCAPED__/g, '{{')
  try {
    JSON.parse(normalized)
    return []
  } catch (e) {
    return [toProblem(text, e as Error)]
  }
}
```

`toProblem` 从 `SyntaxError` 消息里抓 `position N`（V8 与 Safari 的格式不同，两者都要认），再把偏移换算成行列；抓不到就退回 `{line: 1, column: 1}` 并保留原始消息。

`draftState.ts` 是一张纯查表的状态机；`diffView.ts` 的 `inlineDiff` 用 jsdiff 的 `diffLines`。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd hub/internal/site && npm test`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/internal/site/src/lib/
git commit -m "feat: 前端 JSON 校验、草稿状态机与 diff 分组"
```

---

### Task 4: 类型契约与 store

**Files:**
- Modify: `src/types/collections.ts`
- Create: `src/stores/configsets.ts`、`src/stores/credentials.ts`
- Create: `src/lib/api.ts`

**Interfaces:**
- Produces: `ConfigSetRecord` / `RevisionRecord` / `AssignmentRecord` / `CredentialRecord` / `VariableRecord` / `DriftEventRecord` / `IgnoreRuleRecord` / `FileEntry` / `Finding`；`EventKind` 联合补上 15 个新取值；`COLLECTION_*` 常量

`src/lib/api.ts` 把 `/api/orciny/*` 的调用收在一处（`postJSON` / `getJSON` + 错误消息提取），组件不直接 `fetch`。

- [ ] **Step 1: 更新类型**

在 `src/types/collections.ts` 里追加各 record 接口，字段名与 00-overview 的数据模型逐字一致；`EventKind` 补上 `'configset.published' | … | 'import.completed'`。

- [ ] **Step 2: 写 store**

沿用 M0 已立的写法（M0 教训 W / X）：**订阅存 Promise 而不是解析后的退订函数**，切换目标时用自增票号防乱序。

- [ ] **Step 3: 类型检查**

Run: `cd hub/internal/site && npx tsc --noEmit`
Expected: 无错误

- [ ] **Step 4: 提交**

```bash
git add hub/internal/site/src/
git commit -m "feat: M1 collection 类型与 store"
```

---

### Task 5: 配置集列表与详情（编辑器）

**Files:**
- Create: `src/pages/ConfigSets.tsx`、`src/pages/ConfigSetDetail.tsx`
- Create: `src/components/FileTree.tsx`、`src/components/CodeEditor.tsx`、`src/components/DiffView.tsx`、`src/components/PublishDialog.tsx`、`src/components/VersionHistory.tsx`
- Create: `src/components/FileTree.test.tsx`
- Modify: `src/router.tsx`、`src/components/Sidebar.tsx`

**Interfaces:**
- Consumes: Task 2–4 的纯逻辑模块与 store

**要点**

- `CodeEditor` 按扩展名挂 `@codemirror/lang-json` 或 `lang-markdown`；lint 源接 `lintJson` + `undefinedRefs`；补全源接 `completions`。
- 编辑器里的草稿 diff 用 `inlineDiff`（本地算，内容还没落 blob）；版本间 diff 走 hub 的 `/diff` 接口。
- 发布对话框显示 note 输入 + **影响机器预览**（「将影响 N 台机器」）+ `Validate` 的问题列表；有问题时禁用发布按钮。
- 版本历史支持任意两版 diff 与回滚（回滚二次确认，文案写明「会生成新版本 vN，历史保留」）。
- 列表页提供**克隆**与**删除**（spec §11）。删除要二次确认并说明后果：「该配置集的全部版本历史将被删除，且无法恢复；已指派的机器会失去指派，但**磁盘上的文件不会被动**。」删除后端调 `blobs.GCOrphans(setID)`——这是 blob 层唯一会删东西的时刻（spec §4.2）。
- 列表页显示每个配置集的**暂停下发**开关（`config_sets.paused`）。
- 全部用户可见文案走 Lingui，不得硬编码中文。

- [ ] **Step 1: 写文件树的测试**

`FileTree` 是唯一值得测的组件（把扁平路径列表折成树，且要处理 `.claude.json` 这种根级文件）：

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { FileTree, buildTree } from '@/components/FileTree'

describe('buildTree', () => {
  it('把扁平路径折成树', () => {
    const tree = buildTree([
      '.claude.json',
      '.claude/CLAUDE.md',
      '.claude/skills/foo/SKILL.md',
      '.claude/skills/bar/SKILL.md',
    ])
    expect(tree.map((n) => n.name)).toEqual(['.claude.json', '.claude'])
    const claude = tree[1]
    expect(claude.children.map((n) => n.name)).toEqual(['CLAUDE.md', 'skills'])
    const skills = claude.children[1]
    expect(skills.children.map((n) => n.name)).toEqual(['bar', 'foo'])
  })

  it('目录排在文件之后，各自按名字排序', () => {
    const tree = buildTree(['.claude/z.md', '.claude/a/x.md', '.claude/b.md'])
    expect(tree[0].children.map((n) => n.name)).toEqual(['b.md', 'z.md', 'a'])
  })
})

describe('FileTree', () => {
  it('渲染全部叶子路径', () => {
    render(<FileTree paths={['.claude/CLAUDE.md', '.claude.json']} selected="" onSelect={() => {}} />)
    expect(screen.getByText('CLAUDE.md')).toBeInTheDocument()
    expect(screen.getByText('.claude.json')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认失败并实现**

Run: `cd hub/internal/site && npm test`

- [ ] **Step 3: 构建检查**

Run: `cd hub/internal/site && npm run build && git checkout -- dist/index.html`
Expected: 构建通过；产出体积可接受（`ls -lh dist/assets` 里 CodeMirror 那块应在 150–250 KB gzip 量级）

- [ ] **Step 4: 提交**

```bash
git add hub/internal/site/src/
git commit -m "feat: 配置集列表与编辑器"
```

---

### Task 6: 凭据页、变量与指派

**Files:**
- Create: `src/pages/Credentials.tsx`、`src/components/AssignDialog.tsx`、`src/components/VariablesEditor.tsx`
- Create: `src/components/AssignDialog.test.tsx`
- Modify: `src/pages/MachineDetail.tsx`

**要点**

- 凭据列表**只显示末四位**；新增/轮换的输入框是 password 类型；值 < 8 字符时前端就拦下并说明理由。
- 删除受保护的凭据时把「被哪些配置集引用」列出来。
- `AssignDialog` 强制二选一（spec §7.6），**没有默认选中项**——默认成 apply 会让第二台机器被无声覆盖。选 apply 时显示「将覆盖本机 N 个文件」。

- [ ] **Step 1: 写测试**

```tsx
describe('AssignDialog', () => {
  it('未选模式时提交按钮禁用', () => {
    render(<AssignDialog machineId="m" configSets={[{ id: 's', name: 'x' }]} onSubmit={() => {}} onClose={() => {}} />)
    expect(screen.getByRole('button', { name: /确定|Confirm/ })).toBeDisabled()
  })

  it('选 apply 后提交带上 mode', async () => {
    const onSubmit = vi.fn()
    render(<AssignDialog machineId="m" configSets={[{ id: 's', name: 'x' }]} onSubmit={onSubmit} onClose={() => {}} />)
    await userEvent.click(screen.getByLabelText(/应用配置集|Apply/))
    await userEvent.click(screen.getByRole('button', { name: /确定|Confirm/ }))
    expect(onSubmit).toHaveBeenCalledWith({ configSet: 's', mode: 'apply' })
  })
})
```

- [ ] **Step 2: 实现并跑通**

Run: `cd hub/internal/site && npm test`

- [ ] **Step 3: 提交**

```bash
git add hub/internal/site/src/
git commit -m "feat: 凭据页、机器变量与指派对话框"
```

---

### Task 7: 导入向导

**Files:**
- Create: `src/pages/ImportWizard.tsx`
- Create: `src/components/FindingList.tsx` + `.test.tsx`

**四步（spec §9.1）**：采集 → 勾选纳管范围 → 敏感项抽取 → 发布。

**要点**

- 第 3 步每条给三个动作：**抽取为凭据** / **保留明文**（二次确认，警告「该值将进入不可变的版本历史」）/ **把该文件移出纳管范围**。
- 结构化位置的条目排在最前（`rule === 'structured'`）。

- [ ] **Step 1: 写测试**

```tsx
describe('FindingList', () => {
  // 这条是安全断言：UI 会把 masked 直接渲染出来
  it('只渲染掩码，绝不渲染完整值', () => {
    render(<FindingList findings={[{
      path: '.claude/settings.json', location: 'env.K', key: 'K',
      masked: 'sk-a…mnop', suggested: 'k', rule: 'structured',
    }]} onAction={() => {}} />)
    expect(screen.getByText('sk-a…mnop')).toBeInTheDocument()
    expect(screen.queryByText(/sk-ant-abcdefghijklmnop/)).toBeNull()
  })

  it('结构化位置排在最前', () => {
    render(<FindingList findings={[
      { path: 'a', location: 'x', key: 'x', masked: '…', suggested: 'x', rule: 'value_entropy' },
      { path: 'b', location: 'env.Y', key: 'Y', masked: '…', suggested: 'y', rule: 'structured' },
    ]} onAction={() => {}} />)
    const rows = screen.getAllByRole('listitem')
    expect(rows[0]).toHaveTextContent('env.Y')
  })

  it('保留明文需要二次确认', async () => {
    const onAction = vi.fn()
    render(<FindingList findings={[{
      path: 'a', location: 'env.K', key: 'K', masked: '…', suggested: 'k', rule: 'structured',
    }]} onAction={onAction} />)
    await userEvent.click(screen.getByRole('button', { name: /保留明文|Keep plaintext/ }))
    expect(onAction).not.toHaveBeenCalled()
    expect(screen.getByText(/不可变的版本历史|immutable/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /确认|Confirm/ }))
    expect(onAction).toHaveBeenCalledWith('keep', expect.anything())
  })
})
```

- [ ] **Step 2: 实现并跑通**

Run: `cd hub/internal/site && npm test`

- [ ] **Step 3: 提交**

```bash
git add hub/internal/site/src/
git commit -m "feat: 导入向导四步"
```

---

### Task 8: 阶段一收尾与真机验收

- [ ] **Step 1: 全量测试**

```bash
go test -tags=testing ./...
cd hub/internal/site && npm test && npm run build
cd - && git checkout -- hub/internal/site/dist/index.html
```

- [ ] **Step 2: 体积核对**

Run: `make build && ls -lh dist/`
Expected: `orciny` 与 `orciny-agent` 各 < 30 MB

- [ ] **Step 3: 真机验收 DoD 1、2、7、8、9、10、13、14**

按 spec §12 逐条实测，结果补进 `acceptance.md`。第 8 条（apply 失败回滚）的制造办法：把目标目录改成只读（`chmod 500 ~/.claude`），发布一个改动，观察面板显示失败原因且文件未变。

- [ ] **Step 4: 提交**

```bash
git add docs/
git commit -m "docs: M1 阶段一验收记录"
```
