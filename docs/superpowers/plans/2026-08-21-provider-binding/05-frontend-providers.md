# 子计划 05 · 前端：AI 服务页与服务绑定区

**前置**：04
**读这份之前先读** [00-overview.md](00-overview.md) 的 Global Constraints 与全局接口契约。

**交付物**：前端类型与占位符词法扩展；「AI 服务」页（列表 + 新建/编辑对话框）；配置集详情页的「服务绑定」区（含插入 env 片段）；编辑器补全与告警认 `provider.*`；发布对话框区分错误与警告并接一键修复。

**只测纯逻辑**（M1 spec §10.2 的既有取舍）：占位符前端词法、env 片段生成、四槽同填/透传的状态转换、校验问题的分级。**不测** realtime 订阅与 PB SDK 交互。

**测试命令**：`cd hub/internal/site && npm test`

---

### Task 1: 前端类型、占位符词法与 API 封装

**Files:**
- Modify: `hub/internal/site/src/types/collections.ts`
- Modify: `hub/internal/site/src/lib/placeholder.ts`
- Modify: `hub/internal/site/src/lib/placeholder.test.ts`
- Modify: `hub/internal/site/src/lib/api.ts`

**Interfaces:**
- Consumes: 后端的 `providers` collection 与六个端点
- Produces: `ProviderRecord` / `ModelSlots` / `Binding` / `ProviderPreset` / `COLLECTION_PROVIDERS`；`PROVIDER_KEYS`；`listPresets` / `createProvider` / `updateProvider` / `deleteProvider` / `setBinding` / `fixAuthField`

**为什么前端要有第二份占位符词法**（M1 已定的取舍，照抄进注释）：编辑器要在**内容还没提交到 hub** 的时候就给出告警与补全，那时没有任何后端可以问。两份实现的一致性靠双方各自的测试用同一组例子来保证。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/site/src/lib/placeholder.test.ts`：

```ts
describe('provider 占位符', () => {
  it('认得六个内置名', () => {
    const { refs, errors } = parsePlaceholders(
      '{{provider.base_url}} {{provider.auth_token}} {{provider.model}} ' +
        '{{provider.model_opus}} {{provider.model_sonnet}} {{provider.model_haiku}}',
    )
    expect(errors).toEqual([])
    expect(refs.map((r) => `${r.kind}.${r.name}`)).toEqual([
      'provider.base_url',
      'provider.auth_token',
      'provider.model',
      'provider.model_opus',
      'provider.model_sonnet',
      'provider.model_haiku',
    ])
  })

  it('白名单外的名字报错，与 Go 侧对称', () => {
    const { refs, errors } = parsePlaceholders('{{provider.temperature}}')
    expect(refs).toEqual([])
    expect(errors[0]).toContain('provider.temperature')
  })

  it('PROVIDER_KEYS 与 Go 侧 protocol.ProviderKeys 逐字符一致', () => {
    expect(PROVIDER_KEYS).toEqual([
      'base_url', 'auth_token', 'model', 'model_opus', 'model_sonnet', 'model_haiku',
    ])
  })

  it('provider.* 永远算已定义——它由三条绑定校验判定', () => {
    expect(undefinedRefs('{{provider.base_url}}{{cred.gone}}', new Set())).toEqual([
      { kind: 'cred', name: 'gone' },
    ])
  })

  it('补全列表带上六个 provider 内置名', () => {
    expect(completions('provider.', [])).toEqual([
      'provider.auth_token',
      'provider.base_url',
      'provider.model',
      'provider.model_haiku',
      'provider.model_opus',
      'provider.model_sonnet',
    ])
  })

  it('转义的 {{{{provider.x}} 不算引用', () => {
    expect(parsePlaceholders('写法是 {{{{provider.base_url}}').refs).toEqual([])
  })
})
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
cd hub/internal/site && npx vitest run src/lib/placeholder.test.ts
```

Expected: FAIL，`PROVIDER_KEYS` 未导出、`provider` 前缀被当成未知前缀。

- [ ] **Step 3: 实现**

`hub/internal/site/src/lib/placeholder.ts`：

```ts
export type RefKind = 'cred' | 'var' | 'machine' | 'provider'

/** {{machine.*}} 允许的全部名字，与 Go 侧 protocol.MachineKeys 一致。 */
export const MACHINE_KEYS = ['name', 'hostname', 'os', 'arch'] as const

/** {{provider.*}} 允许的全部名字，与 Go 侧 protocol.ProviderKeys 逐字符一致。 */
export const PROVIDER_KEYS = [
  'base_url', 'auth_token', 'model', 'model_opus', 'model_sonnet', 'model_haiku',
] as const
```

`parsePlaceholders` 的分支链追加（放在 `machine` 之后）：

```ts
    } else if (prefix === 'provider') {
      if ((PROVIDER_KEYS as readonly string[]).includes(name)) {
        push(refs, seen, { kind: 'provider', name })
      } else {
        errors.push(`provider.${name} 不是内置名（只有 ${PROVIDER_KEYS.join(' / ')}）`)
      }
    } else {
```

`undefinedRefs`：

```ts
/**
 * known 的元素形如 "cred.foo" / "var.bar"。
 * machine.* 是内置值；provider.* 的「已定义」由三条绑定校验判定
 * （M1.5 spec §7），不走未定义引用这条路——否则每个绑了服务的配置集
 * 都会在编辑器里挂满假告警。
 */
export function undefinedRefs(text: string, known: Set<string>): Ref[] {
  return parsePlaceholders(text).refs.filter(
    (r) => r.kind !== 'machine' && r.kind !== 'provider' && !known.has(`${r.kind}.${r.name}`),
  )
}
```

`completions`：

```ts
/** 补全候选：已有的凭据与变量，加上内置的 machine.* 与 provider.*。 */
export function completions(prefix: string, known: string[]): string[] {
  const all = [
    ...known,
    ...MACHINE_KEYS.map((k) => `machine.${k}`),
    ...PROVIDER_KEYS.map((k) => `provider.${k}`),
  ]
  return all.filter((c) => c.startsWith(prefix)).sort()
}
```

`hub/internal/site/src/types/collections.ts` 追加：

```ts
export type AuthField = 'ANTHROPIC_AUTH_TOKEN' | 'ANTHROPIC_API_KEY'

export interface ModelSlots {
  main: string
  opus: string
  sonnet: string
  haiku: string
}

/** 配置集的服务绑定。单数，不是数组（M1.5 spec §2.2）。 */
export interface Binding {
  provider: string
  models: ModelSlots
}

export interface ProviderRecord {
  id: string
  name: string
  preset: string
  base_url: string
  auth_field: AuthField
  /** credentials 记录 id；key 本身永不下发到前端 */
  credential: string
  models: string[] | null
  defaults: ModelSlots | null
  note: string
  created: string
  updated: string
  expand?: { credential?: CredentialRecord }
}

/** 内置预设。编译进 hub 二进制，只读（M1.5 spec §2.3）。 */
export interface ProviderPreset {
  id: string
  name: string
  base_url: string
  auth_field: AuthField
  models: string[]
  defaults: ModelSlots
  website_url?: string
  api_key_url?: string
  icon?: string
  icon_color?: string
  collector_type?: string
  collector_mode?: string
}

export const COLLECTION_PROVIDERS = 'providers'
```

`ConfigSetRecord` 与 `RevisionRecord` 追加字段：

```ts
  // ConfigSetRecord
  draft_binding: Binding | null
  head_provider: string
  // 加进 expand：'head,head_provider'
  expand?: { head?: RevisionRecord; head_provider?: ProviderRecord }

  // RevisionRecord
  binding: Binding | null
  refs: { creds: string[]; vars: string[]; provider_keys: string[] } | null
```

`draft_refs` 同样补 `provider_keys`。

`ValidateProblem` 追加：

```ts
export interface ValidateProblemFix {
  kind: 'replace_env_key'
  from: string
  to: string
}

export interface ValidateProblem {
  path: string
  kind: string
  detail: string
  /** 真 = 展示但不阻断发布（M1.5 spec §7 第 2 条） */
  warning?: boolean
  /** 非空 = 给「一键修复」 */
  fix?: ValidateProblemFix
}
```

`hub/internal/site/src/lib/api.ts` 追加：

```ts
export function listProviderPresets() {
  return getJSON<ProviderPreset[]>('/api/orciny/provider-presets')
}

export function createProvider(body: ProviderBody) {
  return postJSON<{ id: string }>('/api/orciny/providers', body)
}

export function updateProvider(id: string, body: ProviderBody) {
  return putJSON(`/api/orciny/providers/${id}`, body)
}

export function deleteProvider(id: string) {
  return deleteJSON(`/api/orciny/providers/${id}`)
}

/** provider 传空串即解绑——与「设置」同一个端点。 */
export function setBinding(setId: string, binding: Binding | null) {
  return putJSON(
    `/api/orciny/config-sets/${setId}/binding`,
    binding ?? { provider: '', models: { main: '', opus: '', sonnet: '', haiku: '' } },
  )
}

export function fixAuthField(setId: string) {
  return postJSON(`/api/orciny/config-sets/${setId}/fix-auth-field`)
}

export interface ProviderBody {
  name: string
  preset: string
  base_url: string
  auth_field: AuthField
  credential: string
  models: string[]
  defaults: ModelSlots
  note: string
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
cd hub/internal/site && npm test && npx tsc --noEmit
```

Expected: 全部 PASS，类型检查通过。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/site/src/ && git commit -m "feat(web): provider 占位符词法、类型与 API 封装"
```

---

### Task 2: 「AI 服务」页

**Files:**
- Create: `hub/internal/site/src/stores/providers.ts`
- Create: `hub/internal/site/src/pages/Providers.tsx`
- Create: `hub/internal/site/src/components/ProviderDialog.tsx`
- Create: `hub/internal/site/src/components/ProviderDialog.test.tsx`
- Modify: `hub/internal/site/src/router.tsx`
- Modify: `hub/internal/site/src/components/Sidebar.tsx`
- Modify: `hub/internal/site/src/App.tsx`
- Modify: `hub/internal/site/src/pages/Credentials.tsx`（「被谁引用」显示 Provider）

**Interfaces:**
- Consumes: Task 1 的类型与 API
- Produces: 路由 key `providers`；`$providers` / `subscribeProviders` / `reloadProviders`

**侧边栏位置紧挨「凭据」**（spec §8.1）——它们是同一类东西：**被配置集引用的资源**，不是配置本身。

**新建流程**（spec §8.1）：选平台（预设网格 + 「自定义」）→ 自动带出 base_url、模型列表、`auth_field` → 填 key（新建凭据 或 选已有凭据）→ 保存。

**编辑时必须明确提示**：「这会立即重注入到 N 台机器，不产生新版本」——让「不产生新版本」这件事对用户可见，而不是一个隐藏语义。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/site/src/components/ProviderDialog.test.tsx`，照 `AddFileDialog.test.tsx` 的写法：

```tsx
const presets: ProviderPreset[] = [
  {
    id: 'zhipu', name: 'Zhipu GLM',
    base_url: 'https://open.bigmodel.cn/api/anthropic',
    auth_field: 'ANTHROPIC_AUTH_TOKEN',
    models: ['glm-5.1', 'glm-4.7'],
    defaults: { main: 'glm-5.1', opus: 'glm-5.1', sonnet: 'glm-5.1', haiku: 'glm-5.1' },
  },
  {
    id: 'anthropic', name: 'Anthropic 官方',
    base_url: 'https://api.anthropic.com',
    auth_field: 'ANTHROPIC_AUTH_TOKEN',
    models: ['claude-opus-4-6'],
    defaults: { main: '', opus: '', sonnet: '', haiku: '' },
  },
]

it('选平台自动带出 base_url、auth_field 与模型清单', async () => {
  render(<ProviderDialog presets={presets} credentials={[]} onClose={vi.fn()} onSaved={vi.fn()} />)
  await userEvent.click(screen.getByRole('button', { name: /Zhipu GLM/ }))

  expect(screen.getByLabelText(/base_url/i)).toHaveValue('https://open.bigmodel.cn/api/anthropic')
  expect(screen.getByLabelText(/鉴权字段|auth_field/i)).toHaveValue('ANTHROPIC_AUTH_TOKEN')
  expect(screen.getByText('glm-5.1')).toBeInTheDocument()
})

it('选「自定义」时 base_url 留空、可自由填写', async () => {
  render(<ProviderDialog presets={presets} credentials={[]} onClose={vi.fn()} onSaved={vi.fn()} />)
  await userEvent.click(screen.getByRole('button', { name: /自定义/ }))
  expect(screen.getByLabelText(/base_url/i)).toHaveValue('')
})

it('编辑既有服务配置时明确提示会立即重注入且不产生新版本', () => {
  render(
    <ProviderDialog
      presets={presets}
      credentials={[]}
      editing={{ id: 'p1', name: '智谱 GLM · 个人', preset: 'zhipu',
        base_url: 'https://open.bigmodel.cn/api/anthropic',
        auth_field: 'ANTHROPIC_AUTH_TOKEN', credential: 'c1',
        models: ['glm-5.1'], defaults: presets[0].defaults, note: '',
        created: '', updated: '' }}
      boundSetCount={3}
      onClose={vi.fn()}
      onSaved={vi.fn()}
    />,
  )
  expect(screen.getByText(/不产生新版本/)).toBeInTheDocument()
  expect(screen.getByText(/3/)).toBeInTheDocument()
})

it('缺 base_url 或 凭据 时保存按钮禁用', async () => {
  render(<ProviderDialog presets={presets} credentials={[]} onClose={vi.fn()} onSaved={vi.fn()} />)
  await userEvent.click(screen.getByRole('button', { name: /Zhipu GLM/ }))
  expect(screen.getByRole('button', { name: /保存/ })).toBeDisabled()
})
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
cd hub/internal/site && npx vitest run src/components/ProviderDialog.test.tsx
```

Expected: FAIL，模块不存在。

- [ ] **Step 3: 实现**

Create `hub/internal/site/src/stores/providers.ts`，照 `stores/credentials.ts` 的形状（`$providers` / `$providersLoading` / `$providersError` / `subscribeProviders` / `reloadProviders`），`getFullList` 带 `sort: 'name'` 与 `expand: 'credential'`。

> 订阅事件可能带 `credential` 的 expand，其中含 `cipher_value`。照 `stores/credentials.ts` 的做法，在 subscribe 回调里把敏感字段丢掉，只留 `id` / `name` / `last4`。

Create `hub/internal/site/src/components/ProviderDialog.tsx`：

props：

```tsx
export function ProviderDialog({
  presets,
  credentials,
  editing,
  boundSetCount,
  onClose,
  onSaved,
}: {
  presets: ProviderPreset[]
  credentials: CredentialRecord[]
  /** 非空 = 编辑既有服务配置 */
  editing?: ProviderRecord
  /** 编辑时显示「会立即重注入到 N 台机器」 */
  boundSetCount?: number
  onClose: () => void
  onSaved: () => void
})
```

结构：

1. **平台网格**：`presets` 每条一个按钮（图标 + 名字），外加一个「自定义」按钮。点击即 `applyPreset(p)`：填 `base_url` / `auth_field` / `models` / `defaults`，`preset` 记 id。「自定义」清空这些并把 `preset` 置空串。
2. **base_url 输入框**（`aria-label="base_url"`）。
3. **鉴权字段下拉**（`aria-label="鉴权字段"`），两个选项。
4. **凭据**：一个下拉选已有凭据 + 一个「新建凭据」分支（名字 + 值，值走 `createCredential` 后再拿回 id）。
5. **模型清单**：可增删的 chip 列表。
6. **编辑态提示条**（`editing` 非空时）：

```tsx
<p className="…">
  <Trans>
    保存后会立即重注入到绑定了这个服务配置的 {boundSetCount} 个配置集所属的机器，
    不产生新版本。
  </Trans>
</p>
```

7. 保存按钮：`disabled={!name || !baseURL || !credential}`。

Create `hub/internal/site/src/pages/Providers.tsx`：卡片列表，每张显示 图标 / 名称 / 平台 / base_url / 凭据末四位 / 「被 N 个配置集引用」。「被 N 个」由 `$configSets` 里 `head_provider === p.id || draft_binding?.provider === p.id` 的条数算出——前端已经订了配置集列表，不必再开端点。

`router.tsx`：`RouteKey` 与 `known` 都加 `'providers'`。

`Sidebar.tsx` 的 `items` 在 `credentials` 之前插入：

```tsx
  { key: 'providers', icon: Plug, label: <Trans>AI 服务</Trans> },
```

（`Plug` 从 `lucide-react` 引入；若该名不存在，用 `Cable` 或 `Router`。）

`App.tsx` 加 `case 'providers': return <Providers />`。

`Credentials.tsx`：spec §5.3 要求凭据页的「被谁引用」也显示 Provider。现在这条信息只在删除被拒时通过错误消息露出来（`refsHint`）。在每行凭据后面补一个只读角标：

```tsx
// 被 AI 服务配置引用的凭据删不掉（M1.5 spec §5.3）——先告诉用户，
// 别等他点了删除再报错。
const usedBy = providers.filter((p) => p.credential === c.id)
```

```tsx
{usedBy.length > 0 && (
  <span className="rounded bg-wash px-1.5 py-0.5 text-[10px] text-ink3">
    <Trans>被 {usedBy.length} 个 AI 服务配置引用</Trans>
  </span>
)}
```

数据来自 `subscribeProviders()`——页面本来就要订一份，不必新开端点。

- [ ] **Step 4: 运行测试确认通过**

```bash
cd hub/internal/site && npm test && npx tsc --noEmit
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/site/src/ && git commit -m "feat(web): AI 服务页与新建/编辑对话框"
```

---

### Task 3: 配置集的「服务绑定」区

**Files:**
- Create: `hub/internal/site/src/lib/binding.ts`
- Create: `hub/internal/site/src/lib/binding.test.ts`
- Create: `hub/internal/site/src/components/BindingBar.tsx`
- Create: `hub/internal/site/src/components/BindingBar.test.tsx`
- Modify: `hub/internal/site/src/pages/ConfigSetDetail.tsx`

**Interfaces:**
- Consumes: Task 1 的类型与 API、Task 2 的 `$providers`
- Produces: `emptySlots()`、`fillAllSlots(model)`、`isPassthrough(slots)`、`ENV_SNIPPET`、`insertEnvSnippet(settingsText, authField)`、`hasProviderRefs(text)`

**布局**（spec §8.2）：置于文件树上方。

```
服务绑定  [ 智谱 GLM · 个人  ▾ ]  [ glm-5.1 ▾ ]  [ 高级 ▾ ]  [ 插入 env 片段 ]
```

- 默认只显示主模型下拉，**选定即四槽同填**（spec §2.3 的实证依据：34 个预设全是这么干的）
- 「高级」展开 opus / sonnet / haiku 三个槽，以及「透传模式」开关（= 四槽清空，对应那 35 个中转预设）
- 「插入 env 片段」把占位符形态的 env 块写进草稿的 `settings.json`。**只在首次绑定、且文件里还没有 `{{provider.*}}` 时出现**
- 改绑定 → 落进 `draft_binding` → 走正常的草稿/发布流程 → 新 Revision

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/site/src/lib/binding.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import {
  emptySlots, fillAllSlots, isPassthrough, hasProviderRefs, insertEnvSnippet,
} from '@/lib/binding'

describe('模型槽', () => {
  it('选一个模型即四槽同填', () => {
    expect(fillAllSlots('glm-5.1')).toEqual({
      main: 'glm-5.1', opus: 'glm-5.1', sonnet: 'glm-5.1', haiku: 'glm-5.1',
    })
  })

  it('透传模式 = 四槽全空', () => {
    expect(isPassthrough(emptySlots())).toBe(true)
    expect(isPassthrough(fillAllSlots('glm-5.1'))).toBe(false)
    // 半填不是透传——它是配置错误，UI 要拦住。
    expect(isPassthrough({ main: 'a', opus: '', sonnet: '', haiku: '' })).toBe(false)
  })
})

describe('env 片段', () => {
  it('往空 settings.json 里插入六个键的占位符块', () => {
    const out = insertEnvSnippet('{}', 'ANTHROPIC_AUTH_TOKEN')
    const parsed = JSON.parse(out)
    expect(parsed.env.ANTHROPIC_BASE_URL).toBe('{{provider.base_url}}')
    expect(parsed.env.ANTHROPIC_AUTH_TOKEN).toBe('{{provider.auth_token}}')
    expect(parsed.env.ANTHROPIC_MODEL).toBe('{{provider.model}}')
    expect(parsed.env.ANTHROPIC_DEFAULT_OPUS_MODEL).toBe('{{provider.model_opus}}')
    expect(parsed.env.ANTHROPIC_DEFAULT_SONNET_MODEL).toBe('{{provider.model_sonnet}}')
    expect(parsed.env.ANTHROPIC_DEFAULT_HAIKU_MODEL).toBe('{{provider.model_haiku}}')
  })

  it('用 API_KEY 鉴权的平台插的是 ANTHROPIC_API_KEY', () => {
    const parsed = JSON.parse(insertEnvSnippet('{}', 'ANTHROPIC_API_KEY'))
    expect(parsed.env.ANTHROPIC_API_KEY).toBe('{{provider.auth_token}}')
    expect(parsed.env.ANTHROPIC_AUTH_TOKEN).toBeUndefined()
  })

  it('保留 env 之外与 env 之内的既有键', () => {
    const parsed = JSON.parse(
      insertEnvSnippet('{"permissions":{"allow":["Bash"]},"env":{"MY_VAR":"1"}}',
        'ANTHROPIC_AUTH_TOKEN'),
    )
    expect(parsed.permissions.allow).toEqual(['Bash'])
    expect(parsed.env.MY_VAR).toBe('1')
    expect(parsed.env.ANTHROPIC_BASE_URL).toBe('{{provider.base_url}}')
  })

  it('内容不是合法 JSON 时原样返回，绝不写坏用户的文件', () => {
    expect(insertEnvSnippet('{ 这不是 JSON', 'ANTHROPIC_AUTH_TOKEN')).toBe('{ 这不是 JSON')
  })
})

describe('hasProviderRefs', () => {
  it('认得引用', () => {
    expect(hasProviderRefs('{"a":"{{provider.base_url}}"}')).toBe(true)
    expect(hasProviderRefs('{"a":"{{cred.k}}"}')).toBe(false)
    // 转义的不算
    expect(hasProviderRefs('写法是 {{{{provider.base_url}}')).toBe(false)
  })
})
```

Create `hub/internal/site/src/components/BindingBar.test.tsx`：

```tsx
const provider: ProviderRecord = {
  id: 'p1', name: '智谱 GLM · 个人', preset: 'zhipu',
  base_url: 'https://open.bigmodel.cn/api/anthropic',
  auth_field: 'ANTHROPIC_AUTH_TOKEN', credential: 'c1',
  models: ['glm-5.1', 'glm-4.7'], defaults: null, note: '', created: '', updated: '',
}

it('选主模型时四槽同填', async () => {
  const onChange = vi.fn()
  render(<BindingBar providers={[provider]} binding={{ provider: 'p1', models: emptySlots() }}
    settingsText="{}" onChange={onChange} onInsertSnippet={vi.fn()} />)

  await userEvent.selectOptions(screen.getByLabelText(/主模型/), 'glm-5.1')
  expect(onChange).toHaveBeenCalledWith({
    provider: 'p1',
    models: { main: 'glm-5.1', opus: 'glm-5.1', sonnet: 'glm-5.1', haiku: 'glm-5.1' },
  })
})

it('高级里能分开设三个槽', async () => {
  const onChange = vi.fn()
  render(<BindingBar providers={[provider]}
    binding={{ provider: 'p1', models: fillAllSlots('glm-5.1') }}
    settingsText="{}" onChange={onChange} onInsertSnippet={vi.fn()} />)

  await userEvent.click(screen.getByRole('button', { name: /高级/ }))
  await userEvent.selectOptions(screen.getByLabelText(/haiku/i), 'glm-4.7')
  expect(onChange).toHaveBeenCalledWith({
    provider: 'p1',
    models: { main: 'glm-5.1', opus: 'glm-5.1', sonnet: 'glm-5.1', haiku: 'glm-4.7' },
  })
})

it('「插入 env 片段」只在文件里还没有 provider 引用时出现', () => {
  const { rerender } = render(<BindingBar providers={[provider]}
    binding={{ provider: 'p1', models: fillAllSlots('glm-5.1') }}
    settingsText="{}" onChange={vi.fn()} onInsertSnippet={vi.fn()} />)
  expect(screen.getByRole('button', { name: /插入 env 片段/ })).toBeInTheDocument()

  rerender(<BindingBar providers={[provider]}
    binding={{ provider: 'p1', models: fillAllSlots('glm-5.1') }}
    settingsText='{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}'
    onChange={vi.fn()} onInsertSnippet={vi.fn()} />)
  expect(screen.queryByRole('button', { name: /插入 env 片段/ })).toBeNull()
})

it('未绑定时不显示模型下拉与插入按钮', () => {
  render(<BindingBar providers={[provider]} binding={null}
    settingsText="{}" onChange={vi.fn()} onInsertSnippet={vi.fn()} />)
  expect(screen.queryByLabelText(/主模型/)).toBeNull()
  expect(screen.queryByRole('button', { name: /插入 env 片段/ })).toBeNull()
})
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
cd hub/internal/site && npx vitest run src/lib/binding.test.ts src/components/BindingBar.test.tsx
```

Expected: FAIL，模块不存在。

- [ ] **Step 3: 实现**

Create `hub/internal/site/src/lib/binding.ts`：

```ts
/**
 * 服务绑定的纯逻辑：模型槽、env 片段、provider 引用检测。
 *
 * 与 UI 分开是为了能单测——组件里只留渲染与事件。
 */

import { parsePlaceholders } from '@/lib/placeholder'
import type { AuthField, ModelSlots } from '@/types/collections'

export function emptySlots(): ModelSlots {
  return { main: '', opus: '', sonnet: '', haiku: '' }
}

/**
 * 选一个模型 → 四槽同填。
 *
 * 这是默认交互（M1.5 spec §2.3）：cc-switch 的 34 个带模型的预设，
 * 四个槽填的都是同一个值。分开设置放进「高级」。
 */
export function fillAllSlots(model: string): ModelSlots {
  return { main: model, opus: model, sonnet: model, haiku: model }
}

/** 透传模式 = 四槽全空。半填不算——那是配置错误。 */
export function isPassthrough(s: ModelSlots): boolean {
  return !s.main && !s.opus && !s.sonnet && !s.haiku
}

/** 内容里是否已经有 {{provider.*}} 引用（转义的不算）。 */
export function hasProviderRefs(text: string): boolean {
  return parsePlaceholders(text).refs.some((r) => r.kind === 'provider')
}

/** env 片段的六个键名 → 占位符。authField 决定第二行的键名。 */
export function envSnippet(authField: AuthField): Record<string, string> {
  return {
    ANTHROPIC_BASE_URL: '{{provider.base_url}}',
    [authField]: '{{provider.auth_token}}',
    ANTHROPIC_MODEL: '{{provider.model}}',
    ANTHROPIC_DEFAULT_OPUS_MODEL: '{{provider.model_opus}}',
    ANTHROPIC_DEFAULT_SONNET_MODEL: '{{provider.model_sonnet}}',
    ANTHROPIC_DEFAULT_HAIKU_MODEL: '{{provider.model_haiku}}',
  }
}

/**
 * 把 env 片段合进 settings.json 文本，保留既有键。
 *
 * 内容不是合法 JSON 时**原样返回**：宁可不动，不可写坏（M1 的既定立场）。
 */
export function insertEnvSnippet(settingsText: string, authField: AuthField): string {
  let parsed: Record<string, unknown>
  try {
    parsed = JSON.parse(settingsText || '{}') as Record<string, unknown>
  } catch {
    return settingsText
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    return settingsText
  }
  const env = { ...((parsed.env as Record<string, string> | undefined) ?? {}) }
  Object.assign(env, envSnippet(authField))
  return JSON.stringify({ ...parsed, env }, null, 2)
}
```

Create `hub/internal/site/src/components/BindingBar.tsx`：

```tsx
export function BindingBar({
  providers,
  binding,
  settingsText,
  onChange,
  onInsertSnippet,
}: {
  providers: ProviderRecord[]
  /** null = 未绑定 */
  binding: Binding | null
  /** 草稿里 .claude/settings.json 的当前文本；决定要不要显示「插入 env 片段」 */
  settingsText: string
  onChange: (b: Binding | null) => void
  onInsertSnippet: () => void
})
```

渲染要点：

- 服务下拉：一个「未绑定」选项 + 每条 Provider。选中时 `onChange({ provider: id, models: emptySlots() })`；选「未绑定」时 `onChange(null)`。
- 绑定存在时才渲染主模型下拉（`aria-label` 含「主模型」），选项来自该 Provider 的 `models`；`onChange({ ...binding, models: fillAllSlots(v) })`。
- 「高级」按钮 toggle 出三个下拉（`aria-label` 分别含 `opus` / `sonnet` / `haiku`）与「透传模式」勾选框（勾上即 `onChange({ ...binding, models: emptySlots() })`）。
- 「插入 env 片段」按钮的显示条件：`binding !== null && !hasProviderRefs(settingsText)`。

`ConfigSetDetail.tsx` 接线：

- `useEffect(() => subscribeProviders(), [])`
- 从 `$configSets` 里取当前配置集的 `draft_binding`，渲染 `<BindingBar>` 在 `<FileTree>` 上方。
- `onChange` → `await setBinding(id, b)` → reload 配置集 → 把草稿状态置 dirty（沿用该文件既有的 `nextDraftState` 调用方式）。
- `onInsertSnippet` → 取当前 `.claude/settings.json` 草稿内容 → `insertEnvSnippet(text, provider.auth_field)` → `setDraftFile(id, '.claude/settings.json', next, 0o600)` → 刷新编辑器。若草稿里还没有这个文件，就用 `insertEnvSnippet('{}', authField)` 新建它。

- [ ] **Step 4: 运行测试确认通过**

```bash
cd hub/internal/site && npm test && npx tsc --noEmit
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/site/src/ && git commit -m "feat(web): 配置集的服务绑定区与 env 片段插入"
```

---

### Task 4: 发布对话框区分错误与警告，并接一键修复

**Files:**
- Modify: `hub/internal/site/src/components/PublishDialog.tsx`
- Create: `hub/internal/site/src/components/PublishDialog.test.tsx`

**Interfaces:**
- Consumes: `ValidateProblem.warning` / `.fix`、`fixAuthField`
- Produces: 无新导出符号

**为什么要分级**：`binding_unused` 是警告——「绑了但没用」可能是用户刚绑完还没插 env 片段，不该阻断发布（spec §7 第 2 条）。现在的 `canPublish(draftState, problems.length)` 会把它当成硬错误。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/site/src/components/PublishDialog.test.tsx`：

```tsx
vi.mock('@/lib/api', () => ({
  validateConfigSet: vi.fn(),
  publishConfigSet: vi.fn(),
  fixAuthField: vi.fn(),
}))

it('只有警告时仍然可以发布', async () => {
  vi.mocked(validateConfigSet).mockResolvedValue([
    { path: '', kind: 'binding_unused', detail: '绑了但没用', warning: true },
  ])
  render(<PublishDialog setId="s1" draftState="dirty" affectedMachines={2}
    onClose={vi.fn()} onPublished={vi.fn()} />)

  expect(await screen.findByText(/绑了但没用/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /^发布$/ })).toBeEnabled()
})

it('有错误时禁止发布', async () => {
  vi.mocked(validateConfigSet).mockResolvedValue([
    { path: '.claude/settings.json', kind: 'binding_missing', detail: '没有服务绑定' },
  ])
  render(<PublishDialog setId="s1" draftState="dirty" affectedMachines={2}
    onClose={vi.fn()} onPublished={vi.fn()} />)

  expect(await screen.findByText(/没有服务绑定/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /^发布$/ })).toBeDisabled()
})

it('带 fix 的问题给「一键修复」，点了之后重新校验', async () => {
  vi.mocked(validateConfigSet)
    .mockResolvedValueOnce([{
      path: '.claude/settings.json', kind: 'auth_field_mismatch',
      detail: '键名对不上',
      fix: { kind: 'replace_env_key', from: 'ANTHROPIC_API_KEY', to: 'ANTHROPIC_AUTH_TOKEN' },
    }])
    .mockResolvedValueOnce([])
  vi.mocked(fixAuthField).mockResolvedValue(undefined)

  render(<PublishDialog setId="s1" draftState="dirty" affectedMachines={2}
    onClose={vi.fn()} onPublished={vi.fn()} />)

  await userEvent.click(await screen.findByRole('button', { name: /一键修复/ }))
  expect(fixAuthField).toHaveBeenCalledWith('s1')
  // 修复文案要说清改了什么
  expect(screen.queryByText(/键名对不上/)).toBeNull()
})
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
cd hub/internal/site && npx vitest run src/components/PublishDialog.test.tsx
```

Expected: FAIL，警告也被当成硬错误 / 没有修复按钮。

- [ ] **Step 3: 实现**

`PublishDialog.tsx`：

```tsx
  const blocking = problems.filter((p) => !p.warning)
  const warnings = problems.filter((p) => p.warning)
  const ok = canPublish(draftState, blocking.length) && !publishing && !loading
```

问题区拆成两块：红框列 `blocking`，标题 `<Trans>校验未通过，无法发布</Trans>`；琥珀框列 `warnings`，标题 `<Trans>提示（不影响发布）</Trans>`。

`blocking` 里带 `fix` 的条目后面挂一个按钮：

```tsx
{p.fix && (
  <button type="button" className="…" disabled={fixing}
    onClick={() => void handleFix(p.fix!)}>
    <Trans>一键修复：把 {p.fix.from} 改成 {p.fix.to}</Trans>
  </button>
)}
```

```tsx
  async function handleFix(fix: ValidateProblemFix) {
    setFixing(true)
    setError('')
    try {
      await fixAuthField(setId)
      // 修复改的是草稿，改完必须重新校验——否则用户看着一个已经修好的错误
      setProblems((await validateConfigSet(setId)) ?? [])
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setFixing(false)
    }
  }
```

> `fix.kind` 目前只有 `replace_env_key` 一种，因此按钮直接调 `fixAuthField`。加第二种修复时再按 `kind` 分派。

- [ ] **Step 4: 运行测试确认通过**

```bash
cd hub/internal/site && npm test && npx tsc --noEmit
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/site/src/ && git commit -m "feat(web): 发布校验区分错误与警告并接 auth_field 一键修复"
```

---

### Task 5: 文案抽取与构建

**Files:**
- Modify: `hub/internal/site/src/locales/zh.po` / `en.po` / `zh.js` / `en.js`（生成物）

- [ ] **Step 1: 抽取新文案**

```bash
cd hub/internal/site && npm run extract
```

- [ ] **Step 2: 补英文翻译**

打开 `src/locales/en.po`，把本子计划新增的条目逐条译成英文（中文条目 `msgstr` 留空即回退到 msgid，不要留空）。术语对齐：AI 服务 = AI Service、服务绑定 = Service Binding、透传模式 = Passthrough、一键修复 = Fix it。

- [ ] **Step 3: 编译并跑全量**

```bash
cd hub/internal/site && npm run compile && npm test && npm run build
```

Expected: 编译无警告、测试全绿、`tsc --noEmit` 通过。

- [ ] **Step 4: 复原构建脏文件**

```bash
git checkout -- hub/internal/site/dist/index.html
```

- [ ] **Step 5: 提交**

```bash
git add hub/internal/site/src/locales/ && git commit -m "chore(web): 抽取并编译 M1.5 文案"
```

---

## 本子计划完成后的状态

- 「AI 服务」页可用：建、改、删、看引用数。
- 配置集能在 UI 上绑定服务与模型、插入 env 片段、发布。
- 编辑器认得 `{{provider.*}}`（补全 + 不误报未定义）。
- 发布校验的错误与警告分级正确，`auth_field` 一键修复可用。
- **绑定漂移还没有任何处理**——那是子计划 06 与 07。
