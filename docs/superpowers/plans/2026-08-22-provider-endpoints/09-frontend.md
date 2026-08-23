# 子计划 09 · 前端

**前置**：03（记录形状）、08（路由契约）。02 Task 2（`placeholder.ts`）已做完。
**读这份之前先读** [00-overview.md](00-overview.md) 的全局接口契约的「前端」一节。

**交付物**：类型与 store 跟进双端点；`ProviderDialog` 重做成「平台信息 + 两个
端点分区」；`Providers` 页改成一条两行；凭据页整块拆除；`BindingBar` 里没配
claude 端点的 provider 置灰；`lib/binding.ts` 的 env 片段改端点限定；
导入向导的抽取改成选 provider + 端点。

**一条贯穿全篇的文案纪律**（spec §9）：**openai 端点本期无人消费**。它是
「记下来的配置」，页面上可建可管但不产生任何注入。对话框的 OpenAI 分区
必须有一行灰字说明，否则用户会以为填了就生效。

---

### Task 1: 类型与 store

**Files:**
- Modify: `hub/internal/site/src/types/collections.ts`
- Modify: `hub/internal/site/src/stores/providers.ts`
- Rename: `hub/internal/site/src/stores/credentials.ts` → `stores/variables.ts`（只留变量那一半）
- Modify: `hub/internal/site/src/components/VariablesEditor.tsx:4`（import 路径）
- Test: `hub/internal/site/src/stores/providers.test.ts`（新建）

**Interfaces:**
- Consumes: 后端 `providers` 记录的新形状
- Produces: `EndpointRecord` / `ClaudeEndpointRecord` / `OpenAIEndpointRecord` /
  `ProviderRecord`（见 00-overview）；`$variables` / `subscribeVariables` 迁到新文件

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/site/src/stores/providers.test.ts`：

```ts
import { describe, expect, it } from 'vitest'
import { sanitizeProvider } from '@/stores/providers'
import type { ProviderRecord } from '@/types/collections'

describe('sanitizeProvider', () => {
  it('保留两个端点与末四位', () => {
    const raw = {
      id: 'p1',
      name: '智谱 GLM',
      preset: 'zhipu',
      note: '',
      key_last4: '3456',
      claude: {
        base_url: 'https://open.bigmodel.cn/api/anthropic',
        auth_field: 'ANTHROPIC_AUTH_TOKEN',
        key_last4: '3456',
        models: ['glm-5.2'],
        defaults: { main: 'glm-5.2', opus: 'glm-5.2', sonnet: 'glm-5.2', haiku: 'glm-4.7' },
      },
      claude_key_cipher: '不该出现在这里',
      created: '',
      updated: '',
    } as unknown as ProviderRecord

    const got = sanitizeProvider(raw)
    expect(got.claude?.base_url).toBe('https://open.bigmodel.cn/api/anthropic')
    expect(got.claude?.key_last4).toBe('3456')
    expect(got.key_last4).toBe('3456')
  })

  /**
   * 密文是顶层 Hidden 字段，PocketBase 不会下发它。
   * 这条测试是双保险：万一哪天有人把 Hidden 摘了，白名单式的 sanitize
   * 仍然不会把它放进 store。
   */
  it('任何 cipher 字段都不进 store', () => {
    const raw = {
      id: 'p1',
      name: 'x',
      preset: '',
      note: '',
      key_last4: '',
      key_cipher: 'leak',
      claude_key_cipher: 'leak',
      openai_key_cipher: 'leak',
      claude: null,
      openai: null,
      created: '',
      updated: '',
    } as unknown as ProviderRecord

    expect(JSON.stringify(sanitizeProvider(raw))).not.toContain('leak')
  })

  it('端点为 null 时不炸', () => {
    const raw = {
      id: 'p1', name: 'x', preset: '', note: '', key_last4: '',
      claude: null, openai: null, created: '', updated: '',
    } as ProviderRecord
    const got = sanitizeProvider(raw)
    expect(got.claude).toBeNull()
    expect(got.openai).toBeNull()
  })
})
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
cd hub/internal/site && npx vitest run src/stores/providers.test.ts
```

Expected: FAIL —— `sanitizeProvider` 未导出。

- [ ] **Step 3: 实现**

`types/collections.ts`：

```ts
/** 一个协议端点。密文永不下发到前端——它是后端的 Hidden 字段。 */
export interface EndpointRecord {
  base_url: string
  auth_field: string
  /** 实际生效的那把 key 的末四位，UI 回显用 */
  key_last4?: string
  models: string[] | null
}

export interface ClaudeEndpointRecord extends EndpointRecord {
  defaults: ModelSlots | null
}

export interface OpenAIEndpointRecord extends EndpointRecord {
  default_model: string
}

/** 一条记录 = 一家平台，内含两个协议端点（M1.6 spec §2.1）。 */
export interface ProviderRecord {
  id: string
  name: string
  preset: string
  note: string
  /** 平台级 key 的末四位 */
  key_last4: string
  claude: ClaudeEndpointRecord | null
  openai: OpenAIEndpointRecord | null
  created: string
  updated: string
}

/** 内置预设。一条平台两组端点（M1.6 spec §5.6）。 */
export interface PresetEndpoint {
  base_url: string
  auth_field: string
  models: string[]
  defaults: ModelSlots
  default_model: string
}

export interface ProviderPreset {
  id: string
  name: string
  claude: PresetEndpoint
  openai: PresetEndpoint
  website_url?: string
  api_key_url?: string
  icon?: string
  icon_color?: string
  collector_type?: string
  collector_mode?: string
}
```

删除 `CredentialRecord` 接口与 `COLLECTION_CREDENTIALS` 常量。
`VariableRecord` 与 `COLLECTION_VARIABLES` **保留**。

`EventKind` 联合类型里的三个 `credential.*` 字符串**保留**——库里的历史事件
还带着它们，删了就渲染不出来（spec §6.2）。加一行注释说明：

```ts
  // 以下三个 kind 在 M1.6 之后不再产生，但历史事件仍带着它们，
  // 留在联合类型里是为了让收件箱与事件流能把老记录渲染出来。
  | 'credential.created'
  | 'credential.rotated'
  | 'credential.deleted'
```

`stores/providers.ts`：`sanitize` 改名导出为 `sanitizeProvider`，改成白名单式：

```ts
/**
 * 白名单式过滤：只把已知的非敏感字段放进 store。
 *
 * 密文本来就是后端的 Hidden 字段、PocketBase 不会下发；这里再挡一道，
 * 是因为「有一天有人把 Hidden 摘了」的代价是把 key 的密文塞进浏览器内存。
 */
export function sanitizeProvider(raw: ProviderRecord): ProviderRecord {
  return {
    id: raw.id,
    name: raw.name,
    preset: raw.preset,
    note: raw.note,
    key_last4: raw.key_last4 ?? '',
    claude: raw.claude
      ? {
          base_url: raw.claude.base_url,
          auth_field: raw.claude.auth_field,
          key_last4: raw.claude.key_last4,
          models: raw.claude.models,
          defaults: raw.claude.defaults,
        }
      : null,
    openai: raw.openai
      ? {
          base_url: raw.openai.base_url,
          auth_field: raw.openai.auth_field,
          key_last4: raw.openai.key_last4,
          models: raw.openai.models,
          default_model: raw.openai.default_model,
        }
      : null,
    created: raw.created,
    updated: raw.updated,
  }
}
```

`loadProviders` 里去掉 `expand: 'credential'`，`list.map(sanitize)` 改名。

`stores/credentials.ts` → `stores/variables.ts`：

```bash
cd hub/internal/site/src && git mv stores/credentials.ts stores/variables.ts
```

删掉文件里凭据那一半（`$credentials` / `$credentialsLoading` / `$credentialsError` /
`loadCreds` / `subscribeCredentials` / `reloadCredentials` / `byName`），
只留「机器变量」那一节。import 里去掉 `COLLECTION_CREDENTIALS` 与 `CredentialRecord`。

`components/VariablesEditor.tsx:4` 的 import 改成 `@/stores/variables`。

- [ ] **Step 4: 运行测试确认通过**

```bash
cd hub/internal/site && npx vitest run src/stores/providers.test.ts
```

Expected: PASS。（其余文件此时还有 TS 报错——Task 2–5 依次修。）

- [ ] **Step 5: 提交**

```bash
git add hub/internal/site/src/types hub/internal/site/src/stores hub/internal/site/src/components/VariablesEditor.tsx
git commit -m "feat(web): 前端类型与 store 跟进双端点"
```

---

### Task 2: `lib/binding.ts` 的 env 片段

**Files:**
- Modify: `hub/internal/site/src/lib/binding.ts`
- Test: `hub/internal/site/src/lib/binding.test.ts`

**Interfaces:**
- Consumes: `lib/placeholder.ts` 的 `PROVIDER_KEYS`
- Produces: `envSnippet` / `insertEnvSnippet` 写出端点限定占位符；
  `hasProviderRefs` 不变

- [ ] **Step 1: 写失败的测试**

改写 `hub/internal/site/src/lib/binding.test.ts` 里 env 片段相关的断言：

```ts
it('env 片段写的是端点限定占位符', () => {
  expect(envSnippet('ANTHROPIC_AUTH_TOKEN')).toEqual({
    ANTHROPIC_BASE_URL: '{{provider.claude.base_url}}',
    ANTHROPIC_AUTH_TOKEN: '{{provider.claude.auth_token}}',
    ANTHROPIC_MODEL: '{{provider.claude.model}}',
    ANTHROPIC_DEFAULT_OPUS_MODEL: '{{provider.claude.model_opus}}',
    ANTHROPIC_DEFAULT_SONNET_MODEL: '{{provider.claude.model_sonnet}}',
    ANTHROPIC_DEFAULT_HAIKU_MODEL: '{{provider.claude.model_haiku}}',
  })
})

it('auth_field 决定承载 key 的那一行的键名', () => {
  expect(envSnippet('ANTHROPIC_API_KEY').ANTHROPIC_API_KEY)
    .toBe('{{provider.claude.auth_token}}')
  expect(envSnippet('ANTHROPIC_API_KEY').ANTHROPIC_AUTH_TOKEN).toBeUndefined()
})

it('insertEnvSnippet 合进既有 env，保留其它键', () => {
  const out = insertEnvSnippet(
    '{"env":{"MY_OWN":"keep"},"other":1}', 'ANTHROPIC_AUTH_TOKEN')
  const parsed = JSON.parse(out)
  expect(parsed.env.MY_OWN).toBe('keep')
  expect(parsed.other).toBe(1)
  expect(parsed.env.ANTHROPIC_BASE_URL).toBe('{{provider.claude.base_url}}')
})

it('内容不是合法 JSON 时原样返回——宁可不动，不可写坏', () => {
  expect(insertEnvSnippet('{ 坏掉的', 'ANTHROPIC_AUTH_TOKEN')).toBe('{ 坏掉的')
})

it('hasProviderRefs 认端点限定名', () => {
  expect(hasProviderRefs('{{provider.claude.base_url}}')).toBe(true)
  expect(hasProviderRefs('{{provider.openai.api_key}}')).toBe(true)
  expect(hasProviderRefs('{{provider.base_url}}')).toBe(false)
  expect(hasProviderRefs('{{{{provider.claude.base_url}}}}')).toBe(false)
})
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
cd hub/internal/site && npx vitest run src/lib/binding.test.ts
```

Expected: FAIL。

- [ ] **Step 3: 实现**

Modify `hub/internal/site/src/lib/binding.ts`：

```ts
/**
 * env 片段的六个键名 → 占位符。authField 决定承载 key 的那一行的键名。
 *
 * 六个全部是 **claude 端点**：受管范围只有 .claude/**，openai 端点本期
 * 没有消费者（M1.6 spec §1.3）。接 Codex 时会另起一套片段。
 */
export function envSnippet(authField: AuthField): Record<string, string> {
  return {
    ANTHROPIC_BASE_URL: '{{provider.claude.base_url}}',
    [authField]: '{{provider.claude.auth_token}}',
    ANTHROPIC_MODEL: '{{provider.claude.model}}',
    ANTHROPIC_DEFAULT_OPUS_MODEL: '{{provider.claude.model_opus}}',
    ANTHROPIC_DEFAULT_SONNET_MODEL: '{{provider.claude.model_sonnet}}',
    ANTHROPIC_DEFAULT_HAIKU_MODEL: '{{provider.claude.model_haiku}}',
  }
}
```

`hasProviderRefs` / `insertEnvSnippet` / `emptySlots` / `fillAllSlots` /
`isPassthrough` 一行不改——`hasProviderRefs` 靠 `parsePlaceholders`，02 已经
把词法换掉了。

- [ ] **Step 4: 运行测试确认通过**

```bash
cd hub/internal/site && npx vitest run src/lib/binding.test.ts
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/site/src/lib/binding.ts hub/internal/site/src/lib/binding.test.ts
git commit -m "feat(web): env 片段改用端点限定占位符"
```

---

### Task 3: `lib/api.ts` 与凭据页拆除

**Files:**
- Modify: `hub/internal/site/src/lib/api.ts`
- Delete: `hub/internal/site/src/pages/Credentials.tsx`
- Modify: `hub/internal/site/src/components/Sidebar.tsx:5,18`
- Modify: `hub/internal/site/src/router.tsx:9,14`
- Modify: `hub/internal/site/src/App.tsx:12,28`
- Test: `hub/internal/site/src/components/Sidebar.test.tsx`（新建）

**Interfaces:**
- Consumes: 08 定下的路由
- Produces: `ProviderBody` / `EndpointBody`；`extractKey` / `matchProviderIn`；
  三个 `*Credential` 函数删除

**`MachineDetail` 的机器变量编辑器保留**（spec §5.4）——它管的是 `{{var.*}}`，
与凭据无关。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/site/src/components/Sidebar.test.tsx`：

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { Sidebar } from '@/components/Sidebar'

i18n.load('en', {})
i18n.activate('en')

describe('Sidebar', () => {
  it('不再有凭据入口', () => {
    render(
      <I18nProvider i18n={i18n}>
        <Sidebar />
      </I18nProvider>,
    )
    expect(screen.queryByText('凭据')).toBeNull()
    expect(screen.getByText('AI 服务')).toBeTruthy()
  })
})
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
cd hub/internal/site && npx vitest run src/components/Sidebar.test.tsx
```

Expected: FAIL —— 「凭据」还在。

- [ ] **Step 3: 实现**

`lib/api.ts`：

删 `createCredential` / `rotateCredential` / `deleteCredential`。
`extractCredential` 换成：

```ts
/** 把草稿里某处的值抽成某个 provider 端点的 key（M1.6 spec §5.5）。 */
export function extractKey(
  setId: string,
  path: string,
  location: string,
  provider: string,
  endpoint: 'claude' | 'openai',
) {
  return postJSON(`/api/orciny/config-sets/${setId}/extract`, {
    path, location, provider, endpoint,
  })
}

/** 反查文件里的 base_url，供抽取向导预选 provider。 */
export function matchProviderIn(setId: string, path: string) {
  return getJSON<ProviderMatch>(
    `/api/orciny/config-sets/${setId}/provider-match?path=${encodeURIComponent(path)}`,
  )
}
```

`ProviderBody` 改成双端点。**`key` 用可选属性表达三态**：不传 = 不修改，
传 `''` = 清空，传值 = 替换。

```ts
export interface EndpointBody {
  base_url: string
  auth_field?: string
  models: string[]
  /** 省略 = 不修改；'' = 清空；有值 = 替换（M1.6 spec §5.2） */
  key?: string
  defaults?: ModelSlots
  default_model?: string
}

export interface ProviderBody {
  name: string
  preset: string
  note: string
  /** 平台级 key，三态同上 */
  key?: string
  claude: EndpointBody
  openai: EndpointBody
}
```

`createProviderFromDrift` 的 `from_drift` 加 `endpoint`：

```ts
export function createProviderFromDrift(
  body: ProviderBody & {
    from_drift: { event: string; location: string; endpoint: 'claude' | 'openai' }
  },
) {
  return postJSON<{ id: string }>('/api/orciny/providers', body)
}
```

`setBinding` / `fixAuthField` / `matchBindingDrift` / `rebindDrift` 不变。

```bash
rm hub/internal/site/src/pages/Credentials.tsx
```

`Sidebar.tsx`：删 `KeyRound` import 与那一行 item。
`router.tsx`：`RouteKey` 联合类型与 `known` 数组里删 `'credentials'`。
`App.tsx`：删 `Credentials` 的 import 与那一行渲染。

- [ ] **Step 4: 运行测试确认通过**

```bash
cd hub/internal/site && npx vitest run src/components/Sidebar.test.tsx
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add -A hub/internal/site/src
git commit -m "feat(web): 拆掉凭据页与它的 API"
```

---

### Task 4: `ProviderDialog` 重做

**Files:**
- Modify: `hub/internal/site/src/components/ProviderDialog.tsx`（整体重写）
- Test: `hub/internal/site/src/components/ProviderDialog.test.tsx`（整体重写）

**Interfaces:**
- Consumes: Task 1 的类型、Task 3 的 `ProviderBody`
- Produces: `ProviderDialog` 的 props 去掉 `credentials`

布局（spec §5.2）：

```
平台      [预设网格：智谱 / Z.ai / Kimi / 火山 / ZenMux / MiniMax / Anthropic / 自定义]
名称      [____________]
API key   [············]  ← 平台级，两个端点默认都用它
备注      [____________]

▸ Claude 端点                                        已配置 ●
    base_url    [____________]
    鉴权字段    [ANTHROPIC_AUTH_TOKEN ▾]
    模型清单    [tag 输入]
    默认四槽    [主模型 ▾]  ▸高级（分开设置 / 透传）
    单独的 key  [············]  ← 留空则用平台级

▸ OpenAI 端点                                        未配置 ○
    base_url    [____________]
    鉴权字段    [OPENAI_API_KEY]
    模型清单    [tag 输入]
    默认模型    [▾]
    单独的 key  [············]
```

三条硬要求：

1. 端点分区默认折叠，`base_url` 非空的展开。
2. 右侧的「已配置 / 未配置」是**状态显示，不是开关**——清空 `base_url`
   就是取消配置（spec §2.2）。**不要**加 checkbox 或 toggle。
3. 编辑既有 provider 时密码框显示为空，占位文案「留空则不修改，填写即替换」，
   末四位在右侧以 `····a1b2` 灰字展示。

- [ ] **Step 1: 写失败的测试**

整体重写 `ProviderDialog.test.tsx`：

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { ProviderDialog } from '@/components/ProviderDialog'
import type { ProviderPreset, ProviderRecord } from '@/types/collections'

i18n.load('en', {})
i18n.activate('en')

function wrap(ui: React.ReactNode) {
  return <I18nProvider i18n={i18n}>{ui}</I18nProvider>
}

const presets: ProviderPreset[] = [
  {
    id: 'zhipu',
    name: 'Zhipu GLM',
    claude: {
      base_url: 'https://open.bigmodel.cn/api/anthropic',
      auth_field: 'ANTHROPIC_AUTH_TOKEN',
      models: ['glm-5.2', 'glm-4.7'],
      defaults: { main: 'glm-5.2', opus: 'glm-5.2', sonnet: 'glm-5.2', haiku: 'glm-4.7' },
      default_model: '',
    },
    openai: {
      base_url: 'https://open.bigmodel.cn/api/paas/v4',
      auth_field: 'OPENAI_API_KEY',
      models: ['glm-5.2'],
      defaults: { main: '', opus: '', sonnet: '', haiku: '' },
      default_model: 'glm-5.2',
    },
  },
  {
    id: 'anthropic',
    name: 'Anthropic Official',
    claude: {
      base_url: 'https://api.anthropic.com',
      auth_field: 'ANTHROPIC_API_KEY',
      models: ['claude-opus-5'],
      defaults: { main: '', opus: '', sonnet: '', haiku: '' },
      default_model: '',
    },
    openai: {
      base_url: '', auth_field: 'OPENAI_API_KEY', models: [],
      defaults: { main: '', opus: '', sonnet: '', haiku: '' }, default_model: '',
    },
  },
]

const editing: ProviderRecord = {
  id: 'p1',
  name: '智谱 GLM · 个人',
  preset: 'zhipu',
  note: '',
  key_last4: 'a1b2',
  claude: {
    base_url: 'https://open.bigmodel.cn/api/anthropic',
    auth_field: 'ANTHROPIC_AUTH_TOKEN',
    key_last4: 'a1b2',
    models: ['glm-5.2'],
    defaults: { main: 'glm-5.2', opus: 'glm-5.2', sonnet: 'glm-5.2', haiku: 'glm-4.7' },
  },
  openai: null,
  created: '',
  updated: '',
}

describe('ProviderDialog', () => {
  it('选预设时两个端点一起带出', async () => {
    render(wrap(<ProviderDialog presets={presets} onClose={vi.fn()} onSaved={vi.fn()} />))
    await userEvent.click(screen.getByText('Zhipu GLM'))

    expect(screen.getByLabelText('claude base_url')).toHaveValue(
      'https://open.bigmodel.cn/api/anthropic')
    expect(screen.getByLabelText('openai base_url')).toHaveValue(
      'https://open.bigmodel.cn/api/paas/v4')
    expect(screen.getByLabelText('openai auth_field')).toHaveValue('OPENAI_API_KEY')
  })

  it('平台没有 openai 口时那一侧留空并说明', async () => {
    render(wrap(<ProviderDialog presets={presets} onClose={vi.fn()} onSaved={vi.fn()} />))
    await userEvent.click(screen.getByText('Anthropic Official'))

    expect(screen.getByLabelText('openai base_url')).toHaveValue('')
    expect(screen.getByText(/该平台未提供 OpenAI 端点/)).toBeTruthy()
  })

  it('端点状态是显示不是开关：清空 base_url 即未配置', async () => {
    render(wrap(<ProviderDialog presets={presets} onClose={vi.fn()} onSaved={vi.fn()} />))
    await userEvent.click(screen.getByText('Zhipu GLM'))
    expect(screen.getByTestId('openai-status')).toHaveTextContent('已配置')

    await userEvent.clear(screen.getByLabelText('openai base_url'))
    expect(screen.getByTestId('openai-status')).toHaveTextContent('未配置')

    // 没有任何 checkbox / switch 可以单独开关端点
    expect(screen.queryByRole('switch')).toBeNull()
  })

  it('OpenAI 分区带「本期不产生注入」的说明', async () => {
    render(wrap(<ProviderDialog presets={presets} onClose={vi.fn()} onSaved={vi.fn()} />))
    expect(screen.getByTestId('openai-inert-note')).toBeTruthy()
  })

  it('编辑态密码框为空，提示留空则不修改，右侧显示末四位', () => {
    render(wrap(
      <ProviderDialog presets={presets} editing={editing} onClose={vi.fn()} onSaved={vi.fn()} />,
    ))
    const key = screen.getByLabelText('API key')
    expect(key).toHaveValue('')
    expect(key).toHaveAttribute('placeholder', expect.stringContaining('留空则不修改'))
    expect(screen.getByText('····a1b2')).toBeTruthy()
  })

  it('编辑态保存时不带 key 字段——留空 = 不修改', async () => {
    const save = vi.fn().mockResolvedValue(undefined)
    render(wrap(
      <ProviderDialog
        presets={presets} editing={editing}
        onClose={vi.fn()} onSaved={vi.fn()} onSubmit={save}
      />,
    ))
    await userEvent.click(screen.getByText('保存'))

    const body = save.mock.calls[0][0]
    expect('key' in body).toBe(false)
    expect('key' in body.claude).toBe(false)
  })

  it('填了端点级 key 就带上它', async () => {
    const save = vi.fn().mockResolvedValue(undefined)
    render(wrap(
      <ProviderDialog
        presets={presets} editing={editing}
        onClose={vi.fn()} onSaved={vi.fn()} onSubmit={save}
      />,
    ))
    await userEvent.type(screen.getByLabelText('claude 单独的 key'), 'sk-claude-abcdef12')
    await userEvent.click(screen.getByText('保存'))

    expect(save.mock.calls[0][0].claude.key).toBe('sk-claude-abcdef12')
  })

  it('新建时配了 base_url 却没有任何 key 则禁止保存', async () => {
    render(wrap(<ProviderDialog presets={presets} onClose={vi.fn()} onSaved={vi.fn()} />))
    await userEvent.click(screen.getByText('Zhipu GLM'))
    await userEvent.type(screen.getByLabelText('名称'), '智谱个人')

    expect(screen.getByText('保存')).toBeDisabled()

    await userEvent.type(screen.getByLabelText('API key'), 'sk-zhipu-abcdef12')
    expect(screen.getByText('保存')).not.toBeDisabled()
  })

  it('编辑态显示重注入提示', () => {
    render(wrap(
      <ProviderDialog
        presets={presets} editing={editing} boundSetCount={2}
        onClose={vi.fn()} onSaved={vi.fn()}
      />,
    ))
    expect(screen.getByTestId('reinject-hint')).toBeTruthy()
  })
})
```

> 组件加一个可选的 `onSubmit?: (body: ProviderBody) => Promise<void>` prop，
> 默认走 `createProvider` / `updateProvider`。测试注入它来断言请求体——
> 这比 mock `@/lib/api` 模块干净，也把「三态怎么编码」这件事变成可测的。

- [ ] **Step 2: 运行测试确认它失败**

```bash
cd hub/internal/site && npx vitest run src/components/ProviderDialog.test.tsx
```

Expected: FAIL。

- [ ] **Step 3: 实现**

整体重写 `ProviderDialog.tsx`。要点：

**props**

```tsx
export function ProviderDialog({
  presets,
  editing,
  boundSetCount = 0,
  onClose,
  onSaved,
  onSubmit,
}: {
  presets: ProviderPreset[]
  /** 非空 = 编辑既有服务配置 */
  editing?: ProviderRecord
  /** 编辑时显示「会立即重注入到 N 个配置集所属的机器」 */
  boundSetCount?: number
  onClose: () => void
  onSaved: () => void
  /** 注入用。默认走 createProvider / updateProvider。 */
  onSubmit?: (body: ProviderBody) => Promise<void>
}) {
```

**state**：平台信息一组（`preset` / `name` / `platformKey` / `note`），
两个端点各一组。端点级 state 用一个小结构，避免十几个平铺的 useState：

```tsx
interface EndpointDraft {
  baseURL: string
  authField: string
  models: string[]
  key: string          // '' = 没动过（编辑态）或没填（新建态）
  cleared: boolean     // 用户点过「清除」→ 提交 key: ''
  defaults: ModelSlots // 仅 claude
  defaultModel: string // 仅 openai
}
```

**「清除」按钮**：端点级 key 右侧一个小链接，点了把 `cleared` 置 true 并
显示「保存后回落平台级」。没有它，用户没有办法把一个已设的端点级 key 撤掉
（留空是「不修改」）。

**三态编码**（这是本任务的核心，写错了会悄悄清掉用户的 key）：

```tsx
/**
 * key 的三态（M1.6 spec §5.2）：
 *   没填且没点清除 → 字段**不出现在请求体里** = 不修改
 *   点了清除       → key: '' = 清空
 *   填了           → key: 值 = 替换
 *
 * 新建态没有「不修改」可言，但同一套编码照样成立：没填就不传，
 * 后端的「配了 base_url 必须有 key」会把该拦的拦下。
 */
function keyField(draft: { key: string; cleared: boolean }): { key?: string } {
  if (draft.key !== '') return { key: draft.key }
  if (draft.cleared) return { key: '' }
  return {}
}
```

**折叠**：`useState(() => endpoint.base_url !== '')` 决定初始展开。
状态徽标：

```tsx
<span data-testid="claude-status" className="text-xs text-ink3">
  {claude.baseURL ? <Trans>已配置</Trans> : <Trans>未配置</Trans>}
</span>
```

**OpenAI 分区的两行说明**：

```tsx
<p data-testid="openai-inert-note" className="mb-2 text-xs text-ink3">
  <Trans>
    OpenAI 端点本期只是「记下来的配置」：可以建、可以管，但还不会注入到任何机器。
    受管范围目前只有 .claude/**。
  </Trans>
</p>
{presetHasNoOpenAI && (
  <p className="mb-2 text-xs text-ink3">
    <Trans>该平台未提供 OpenAI 端点。你仍然可以手填一个。</Trans>
  </p>
)}
```

**`applyPreset`**：两个端点一起带出。

```tsx
function applyPreset(p: ProviderPreset) {
  setPreset(p.id)
  if (!name) setName(p.name)
  setClaude({
    baseURL: p.claude.base_url,
    authField: p.claude.auth_field || 'ANTHROPIC_AUTH_TOKEN',
    models: p.claude.models,
    key: '', cleared: false,
    defaults: p.claude.defaults,
    defaultModel: '',
  })
  setOpenai({
    baseURL: p.openai.base_url,
    authField: p.openai.auth_field || 'OPENAI_API_KEY',
    models: p.openai.models,
    key: '', cleared: false,
    defaults: emptySlots(),
    defaultModel: p.openai.default_model,
  })
}
```

**`canSave`**：名称非空 + 「每个配了 base_url 的端点都能拿到一把 key」：

```tsx
/** 与后端 providers.validate 同一条规则，在按钮上先挡一次（spec §2.3）。 */
function endpointHasKey(ep: EndpointDraft): boolean {
  if (ep.key !== '') return true
  if (!ep.cleared && hasExistingEndpointKey(ep)) return true
  return platformKeyAvailable
}
```

其中 `platformKeyAvailable` = 平台级密码框填了、或（编辑态且没点清除且
`editing.key_last4` 非空）；`hasExistingEndpointKey` 同理看
`editing?.claude?.key_last4`。

**密码框**：

```tsx
<label className="mb-1 block text-xs text-ink3" htmlFor="provider-key">
  <Trans>API key</Trans>
</label>
<div className="mb-3 flex items-center gap-2">
  <input
    id="provider-key"
    aria-label="API key"
    type="password"
    className="flex-1 rounded border border-line bg-wash px-2 py-1.5 font-mono text-sm"
    placeholder={editing ? t`留空则不修改，填写即替换` : t`两个端点默认都用它`}
    value={platformKey}
    onChange={(e) => setPlatformKey(e.target.value)}
  />
  {editing?.key_last4 && <span className="text-xs text-ink3">····{editing.key_last4}</span>}
</div>
```

**保存**：

```tsx
async function handleSave() {
  setBusy(true)
  setError('')
  try {
    const body: ProviderBody = {
      name: name.trim(),
      preset,
      note,
      ...keyField({ key: platformKey, cleared: platformCleared }),
      claude: {
        base_url: claude.baseURL.trim(),
        auth_field: claude.authField,
        models: claude.models,
        defaults: claude.defaults,
        ...keyField(claude),
      },
      openai: {
        base_url: openai.baseURL.trim(),
        auth_field: openai.authField,
        models: openai.models,
        default_model: openai.defaultModel,
        ...keyField(openai),
      },
    }
    if (onSubmit) await onSubmit(body)
    else if (editing) await updateProvider(editing.id, body)
    else await createProvider(body)
    onSaved()
  } catch (e) {
    setError(e instanceof ApiError || e instanceof Error ? e.message : String(e))
    setBusy(false)
  }
}
```

编辑态的重注入提示（`data-testid="reinject-hint"`）与模型 tag 输入沿用
现有实现，只是搬进各自的端点分区；claude 分区的「默认四槽」复用
`fillAllSlots` / `isPassthrough`（`lib/binding.ts` 已有）。

`createCredential` 相关的 import 与「选已有 / 新建凭据」双模切换整块删除。

- [ ] **Step 4: 运行测试确认通过**

```bash
cd hub/internal/site && npx vitest run src/components/ProviderDialog.test.tsx
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/site/src/components/ProviderDialog.tsx hub/internal/site/src/components/ProviderDialog.test.tsx
git commit -m "feat(web): 服务配置对话框改成平台信息加两个端点分区"
```

---

### Task 5: `Providers` 页一条两行 + `BindingBar` 置灰

**Files:**
- Modify: `hub/internal/site/src/pages/Providers.tsx`
- Modify: `hub/internal/site/src/components/BindingBar.tsx`
- Modify: `hub/internal/site/src/pages/ConfigSetDetail.tsx:11,39,55,70-77,154-160`
- Test: `hub/internal/site/src/pages/Providers.test.tsx`（新建）
- Test: `hub/internal/site/src/components/BindingBar.test.tsx`

**Interfaces:**
- Consumes: Task 1 的类型
- Produces: 无新接口

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/site/src/pages/Providers.test.tsx`——列表渲染需要 store，
测试只覆盖**纯渲染的那一段**。把列表项抽成一个可单测的组件
`ProviderRow`（同文件导出即可）：

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { ProviderRow } from '@/pages/Providers'
import type { ProviderRecord } from '@/types/collections'

i18n.load('en', {})
i18n.activate('en')

const base: ProviderRecord = {
  id: 'p1', name: '智谱 GLM', preset: 'zhipu', note: '', key_last4: 'a1b2',
  claude: {
    base_url: 'https://open.bigmodel.cn/api/anthropic',
    auth_field: 'ANTHROPIC_AUTH_TOKEN',
    key_last4: 'a1b2',
    models: ['glm-5.2', 'glm-4.7', 'glm-5-turbo', 'glm-5.2[1m]'],
    defaults: null,
  },
  openai: null,
  created: '', updated: '',
}

function row(p: ProviderRecord, n = 2) {
  return render(
    <I18nProvider i18n={i18n}>
      <ProviderRow provider={p} boundCount={n} onEdit={vi.fn()} onDelete={vi.fn()} />
    </I18nProvider>,
  )
}

describe('ProviderRow', () => {
  it('两个端点各一行，claude 行显示鉴权字段、末四位与模型数', () => {
    row(base)
    expect(screen.getByText('https://open.bigmodel.cn/api/anthropic')).toBeTruthy()
    expect(screen.getByText(/ANTHROPIC_AUTH_TOKEN/)).toBeTruthy()
    expect(screen.getByText(/····a1b2/)).toBeTruthy()
    expect(screen.getByText(/4 个模型/)).toBeTruthy()
  })

  /**
   * 未配置的端点显示为置灰的「未配置」行，而不是整行不显示——
   * 用户要能一眼看出「这条还有另一个口没填」（M1.6 spec §5.1）。
   */
  it('未配置的端点显示成置灰的「未配置」行', () => {
    row(base)
    const openai = screen.getByTestId('endpoint-openai')
    expect(openai).toHaveTextContent('OpenAI')
    expect(openai).toHaveTextContent('未配置')
  })

  it('显示引用数', () => {
    row(base, 3)
    expect(screen.getByText(/被 3 个配置集引用/)).toBeTruthy()
  })
})
```

追加到 `BindingBar.test.tsx`（并把既有的 `provider` fixture 换成双端点形状）：

```tsx
const noClaude: ProviderRecord = {
  id: 'p2', name: '只有 OpenAI 的中转', preset: '', note: '', key_last4: 'ffff',
  claude: null,
  openai: {
    base_url: 'https://relay.example/v1', auth_field: 'OPENAI_API_KEY',
    key_last4: 'ffff', models: ['gpt-5.2'], default_model: 'gpt-5.2',
  },
  created: '', updated: '',
}

it('没配 claude 端点的 provider 在下拉里置灰', () => {
  render(wrap(
    <BindingBar
      providers={[provider, noClaude]}
      binding={null}
      settingsText="{}"
      onChange={vi.fn()}
      onInsertSnippet={vi.fn()}
    />,
  ))
  const opt = screen.getByRole('option', { name: /只有 OpenAI 的中转/ })
  expect(opt).toBeDisabled()
  expect(opt).toHaveAttribute('title', expect.stringContaining('还没有 Claude 端点'))
})

it('模型下拉取 claude 端点的模型清单', () => {
  render(wrap(
    <BindingBar
      providers={[provider]}
      binding={{ provider: 'p1', models: emptySlots() }}
      settingsText="{}"
      onChange={vi.fn()}
      onInsertSnippet={vi.fn()}
    />,
  ))
  expect(screen.getByRole('option', { name: 'glm-5.2' })).toBeTruthy()
})
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
cd hub/internal/site && npx vitest run src/pages/Providers.test.tsx src/components/BindingBar.test.tsx
```

Expected: FAIL。

- [ ] **Step 3: 实现**

`pages/Providers.tsx`：把 `<li>` 抽成导出的 `ProviderRow`，渲染成一条两行：

```tsx
/** 一条 = 一家平台，两个端点各一行（M1.6 spec §5.1）。 */
export function ProviderRow({
  provider: p,
  boundCount,
  onEdit,
  onDelete,
}: {
  provider: ProviderRecord
  boundCount: number
  onEdit: () => void
  onDelete: () => void
}) {
  const { t } = useLingui()
  return (
    <li className="flex items-start gap-3 rounded border border-line bg-surface px-4 py-3">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium">{p.name}</span>
          {p.preset && (
            <span className="rounded bg-wash px-1.5 py-0.5 text-[10px] text-ink3">
              {p.preset}
            </span>
          )}
        </div>

        <EndpointLine
          testid="endpoint-claude"
          label="Claude"
          endpoint={p.claude}
          detail={
            p.claude && (
              <>
                <span className="font-mono">{p.claude.auth_field}</span>
                {p.claude.key_last4 && <span> · ····{p.claude.key_last4}</span>}
                <span> · {t`${p.claude.models?.length ?? 0} 个模型`}</span>
              </>
            )
          }
        />
        <EndpointLine
          testid="endpoint-openai"
          label="OpenAI"
          endpoint={p.openai}
          detail={
            p.openai && (
              <>
                <span className="font-mono">{p.openai.auth_field}</span>
                {p.openai.key_last4 && <span> · ····{p.openai.key_last4}</span>}
                <span> · {p.openai.default_model || t`未指定默认模型`}</span>
              </>
            )
          }
        />

        <div className="mt-1 text-[11px] text-ink3">
          <Trans>被 {boundCount} 个配置集引用</Trans>
        </div>
      </div>
      {/* 编辑 / 删除按钮沿用现有实现 */}
    </li>
  )
}

/**
 * 未配置的端点显示为置灰的「未配置」行，而不是整行不显示——
 * 用户要能一眼看出「这条还有另一个口没填」，那正是本期新增的可操作项。
 */
function EndpointLine({
  testid, label, endpoint, detail,
}: {
  testid: string
  label: string
  endpoint: { base_url: string } | null
  detail: React.ReactNode
}) {
  const configured = Boolean(endpoint?.base_url)
  return (
    <div data-testid={testid} className="mt-1 flex gap-2 text-xs">
      <span className="w-14 shrink-0 text-ink3">{label}</span>
      {configured ? (
        <div className="min-w-0">
          <div className="truncate font-mono text-ink3">{endpoint!.base_url}</div>
          <div className="text-[11px] text-ink3">{detail}</div>
        </div>
      ) : (
        <span className="text-ink3/50">
          <Trans>未配置</Trans>
        </span>
      )}
    </div>
  )
}
```

`Providers.tsx` 的其余部分：删掉 `$credentials` / `subscribeCredentials` 的
import 与使用，`<ProviderDialog credentials={credentials} …>` 去掉那个 prop。
`boundCount` 的算法一行不改。

`components/BindingBar.tsx`：

```tsx
  const provider = providers.find((p) => p.id === binding?.provider)
  // 模型清单取 claude 端点：绑定本期恒指 claude（M1.6 spec §1.3）。
  const models = provider?.claude?.models ?? []
```

下拉选项置灰：

```tsx
          {providers.map((p) => {
            // 没配 claude 端点的 provider 绑不上：让不可能成功的操作在点下去
            // 之前就说明原因，而不是点完弹一个发布校验错误（M1.6 spec §5.3）。
            const usable = Boolean(p.claude?.base_url)
            return (
              <option
                key={p.id}
                value={p.id}
                disabled={!usable}
                title={usable ? undefined : t`这条服务配置还没有 Claude 端点`}
              >
                {p.name}
                {usable ? '' : t` （无 Claude 端点）`}
              </option>
            )
          })}
```

`pages/ConfigSetDetail.tsx`：

- import 从 `@/stores/credentials` 改成 `@/stores/variables`，`$credentials` /
  `subscribeCredentials` 的使用删除
- `knownRefs` 只留变量那一半：

```tsx
  const knownRefs = useMemo(() => {
    const refs: string[] = []
    for (const v of set?.draft_refs?.vars ?? []) refs.push(`var.${v}`)
    return refs
  }, [set])
```

- `handleInsertSnippet` 里 `provider.auth_field` 改成
  `provider.claude?.auth_field`，并在端点缺失时直接返回：

```tsx
    const authField = provider.claude?.auth_field
    if (!authField) return // 没配 claude 端点，插不了片段（下拉里本来也是置灰的）
    const next = insertEnvSnippet(settingsText || '{}', authField as AuthField)
```

- [ ] **Step 4: 运行测试确认通过**

```bash
cd hub/internal/site && npm test && npx tsc --noEmit
```

Expected: 全部 PASS，`tsc` 无输出。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/site/src
git commit -m "feat(web): AI 服务页一条两行，绑定下拉置灰无 Claude 端点的记录"
```

---

### Task 6: 导入向导的抽取改成选 provider + 端点

**Files:**
- Modify: `hub/internal/site/src/pages/ImportWizard.tsx:8,45,109-135,326-336`
- Modify: `hub/internal/site/src/components/FindingList.tsx`（文案）
- Create: `hub/internal/site/src/components/ExtractDialog.tsx`
- Test: `hub/internal/site/src/components/ExtractDialog.test.tsx`

**Interfaces:**
- Consumes: Task 3 的 `extractKey` / `matchProviderIn`、Task 1 的 `ProviderRecord`
- Produces: `ExtractDialog`

原来的 `PromptDialog`（输入一个凭据名）不再适用：现在要选**哪条 provider 的
哪个端点**。反查命中时预选那条 provider（spec §5.5 第 2 步）。

**非 AI 类的 key 没有抽取去处了**（spec §9）：`FindingList` 的「抽取为凭据」
按钮改成「抽成服务配置的 key」，并在 `keep` 那一档的说明里写清替代方案。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/site/src/components/ExtractDialog.test.tsx`：

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { ExtractDialog } from '@/components/ExtractDialog'
import type { ProviderRecord } from '@/types/collections'

i18n.load('en', {})
i18n.activate('en')

const zhipu: ProviderRecord = {
  id: 'p1', name: '智谱 GLM', preset: 'zhipu', note: '', key_last4: '',
  claude: {
    base_url: 'https://open.bigmodel.cn/api/anthropic',
    auth_field: 'ANTHROPIC_AUTH_TOKEN', models: [], defaults: null,
  },
  openai: null, created: '', updated: '',
}
const other: ProviderRecord = { ...zhipu, id: 'p2', name: '另一家' }

function wrap(ui: React.ReactNode) {
  return <I18nProvider i18n={i18n}>{ui}</I18nProvider>
}

describe('ExtractDialog', () => {
  it('反查命中时预选那条 provider', () => {
    render(wrap(
      <ExtractDialog
        providers={[other, zhipu]}
        suggestedProviderId="p1"
        masked="sk-z…3456"
        onSubmit={vi.fn()}
        onClose={vi.fn()}
      />,
    ))
    expect(screen.getByLabelText('服务配置')).toHaveValue('p1')
  })

  it('默认抽到 claude 端点，可以改成 openai', async () => {
    const onSubmit = vi.fn()
    render(wrap(
      <ExtractDialog
        providers={[zhipu]}
        masked="sk-z…3456"
        onSubmit={onSubmit}
        onClose={vi.fn()}
      />,
    ))
    expect(screen.getByLabelText('端点')).toHaveValue('claude')

    await userEvent.selectOptions(screen.getByLabelText('端点'), 'openai')
    await userEvent.click(screen.getByText('抽取'))
    expect(onSubmit).toHaveBeenCalledWith('p1', 'openai')
  })

  it('一条 provider 都没有时禁止提交并给出去处', () => {
    render(wrap(
      <ExtractDialog providers={[]} masked="sk-z…3456"
        onSubmit={vi.fn()} onClose={vi.fn()} />,
    ))
    expect(screen.getByText('抽取')).toBeDisabled()
    expect(screen.getByText(/先到「AI 服务」页建一条/)).toBeTruthy()
  })

  it('显示被抽取值的掩码，不显示明文', () => {
    render(wrap(
      <ExtractDialog providers={[zhipu]} masked="sk-z…3456"
        onSubmit={vi.fn()} onClose={vi.fn()} />,
    ))
    expect(screen.getByText('sk-z…3456')).toBeTruthy()
  })
})
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
cd hub/internal/site && npx vitest run src/components/ExtractDialog.test.tsx
```

Expected: FAIL —— 组件不存在。

- [ ] **Step 3: 实现**

Create `ExtractDialog.tsx`：一个和 `PromptDialog` 同形的小对话框，
内容是「服务配置」下拉（`aria-label="服务配置"`）+「端点」下拉
（`aria-label="端点"`，选项 `claude` / `openai`）+ 掩码回显 + 取消 / 抽取两个按钮。

初值规则：provider 下拉取 `suggestedProviderId ?? providers[0]?.id ?? ''`，
端点下拉恒为 `'claude'`——受管范围目前只有 `.claude/**`，绝大多数抽取都落在
那一侧。provider 列表为空时禁用「抽取」，并渲染一行
`<Trans>先到「AI 服务」页建一条，再回来抽取。</Trans>`。

`ImportWizard.tsx`：

```tsx
  const [extractTarget, setExtractTarget] = useState<Finding | null>(null)
  const [suggested, setSuggested] = useState<string | undefined>()
  const providers = useStore($providers)
  useEffect(() => subscribeProviders(), [])

  async function onFindingAction(action: FindingAction, f: Finding) {
    if (!setId) return
    if (action === 'extract') {
      setExtractTarget(f)
      // 反查这个文件里的 base_url，命中就预选那条 provider（spec §5.5 第 2 步）。
      // 反查失败不挡路：让用户自己选。
      try {
        const m = await matchProviderIn(setId, f.path)
        setSuggested(m.kind === 'provider' ? m.provider_id : undefined)
      } catch {
        setSuggested(undefined)
      }
      return
    }
    // keep / exclude 两档不变
  }

  async function extractFinding(f: Finding, providerId: string, endpoint: 'claude' | 'openai') {
    if (!setId) return
    const key = `${f.path}:${f.location}`
    setBusy(true)
    setError('')
    try {
      await extractKey(setId, f.path, f.location, providerId, endpoint)
      setHandled((h) => new Set(h).add(key))
      setExtractTarget(null)
      await loadDraft(setId)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
```

渲染处：

```tsx
      {extractTarget && (
        <ExtractDialog
          providers={providers}
          suggestedProviderId={suggested}
          masked={extractTarget.masked}
          busy={busy}
          onSubmit={(pid, ep) => void extractFinding(extractTarget, pid, ep)}
          onClose={() => setExtractTarget(null)}
        />
      )}
```

`FindingList.tsx`：「抽取为凭据」改成「抽成服务配置的 key」，
`keep` 那一档的说明补一句：

```tsx
  <Trans>
    保留明文。非 AI 平台的密钥没有加密去处——可以改用机器变量（不加密），
    或把这个文件移出纳管范围。
  </Trans>
```

- [ ] **Step 4: 运行测试确认通过**

```bash
cd hub/internal/site && npm test && npx tsc --noEmit && npm run build
```

Expected: 全部 PASS，`tsc` 与 `vite build` 无错。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/site/src
git commit -m "feat(web): 导入向导的抽取改成选服务配置与端点"
```
