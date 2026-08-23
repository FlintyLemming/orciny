# 子计划 02 · protocol 词法端点限定

**前置**：无（与 01 可并行）
**读这份之前先读** [00-overview.md](00-overview.md) 的 Global Constraints 与全局接口契约。

**交付物**：`protocol` 的九个端点限定名、`EndpointOf`、`RefCred` 与 `parseRef` 的
`cred` 分支删除；前端 `lib/placeholder.ts` 的同步改造。

**为什么先做这个**：协议是 hub 与 agent 的公共依赖，词法必须先定死
（spec §8 第 2 步）。**hub 与 agent 两侧要逐位一致**——让语义依赖上下文，
就是让「两侧一致」从「比较字符串」退化成「比较两套推断实现」（spec §3.2 第 2 条）。

**前端的 `lib/placeholder.ts` 为什么也在这个子计划里**（spec §8 把前端整体排在
第 9 步）：它是同一套词法的第三份实现，只有它自己的单测，不依赖任何后端改动。
和 Go 侧分开做的话，中间那段时间编辑器会对每一个新写法挂假告警。

**不做别名。** 旧写法 `{{provider.base_url}}` 不保留兼容——留一个只在过渡期
有意义的别名，代价是它会一直活到有人专门去删它（spec §3.1）。

---

### Task 1: 九个端点限定名与 `EndpointOf`

**Files:**
- Modify: `protocol/placeholder.go`
- Test: `protocol/placeholder_test.go`（追加，不改已有的 machine / 转义用例）

**Interfaces:**
- Consumes: 无
- Produces: `protocol.ProviderKeys`（九项）、`protocol.EndpointOf(name string) string`；
  `protocol.RefCred` **删除**

- [ ] **Step 1: 写失败的测试**

追加到 `protocol/placeholder_test.go`：

```go
func TestProviderKeysAreEndpointQualified(t *testing.T) {
	require.Equal(t, []string{
		"claude.base_url", "claude.auth_token",
		"claude.model", "claude.model_opus", "claude.model_sonnet", "claude.model_haiku",
		"openai.base_url", "openai.api_key",
		"openai.model",
	}, protocol.ProviderKeys,
		"顺序即 UI 展示顺序，也是 agent 还原的 rankOf 定序依据，不要重排")
}

func TestParseAcceptsAllNineProviderKeys(t *testing.T) {
	for _, k := range protocol.ProviderKeys {
		segs, err := protocol.Parse([]byte("{{provider." + k + "}}"))
		require.NoError(t, err, "provider.%s 必须 parse 通过", k)
		require.Len(t, segs, 1)
		require.NotNil(t, segs[0].Ref)
		require.Equal(t, protocol.RefProvider, segs[0].Ref.Kind)
		require.Equal(t, k, segs[0].Ref.Name, "Ref.Name 承载两段名")
	}
}

// 旧写法不保留别名（spec §3.1）：本期是破坏性迁移。
func TestParseRejectsOldUnqualifiedProviderNames(t *testing.T) {
	for _, body := range []string{
		"provider.base_url", "provider.auth_token", "provider.model",
		"provider.model_opus", "provider.model_sonnet", "provider.model_haiku",
	} {
		_, err := protocol.Parse([]byte("{{" + body + "}}"))
		require.ErrorIs(t, err, protocol.ErrBadPlaceholder, "%s 必须被拒绝", body)
		require.Contains(t, err.Error(), "不是内置名")
	}
}

func TestParseRejectsCredPrefix(t *testing.T) {
	_, err := protocol.Parse([]byte("{{cred.zhipu}}"))
	require.ErrorIs(t, err, protocol.ErrBadPlaceholder)
	require.Contains(t, err.Error(), "未知前缀")
}

func TestParseRejectsUnknownEndpointQualifiedName(t *testing.T) {
	for _, body := range []string{
		"provider.claude.temperature", // 端点对、字段不对
		"provider.gemini.base_url",    // 字段对、端点不对
		"provider.claude",             // 缺字段段
	} {
		_, err := protocol.Parse([]byte("{{" + body + "}}"))
		require.ErrorIs(t, err, protocol.ErrBadPlaceholder, "%s 必须被拒绝", body)
	}
}

// var 分支的字符集校验不动：var 是用户自定义名，仍需 validName。
func TestVarStillRejectsDotsInName(t *testing.T) {
	_, err := protocol.Parse([]byte("{{var.a.b}}"))
	require.ErrorIs(t, err, protocol.ErrBadPlaceholder)
	require.Contains(t, err.Error(), "名字非法")
}

func TestEndpointOf(t *testing.T) {
	require.Equal(t, "claude", protocol.EndpointOf("claude.base_url"))
	require.Equal(t, "claude", protocol.EndpointOf("claude.model_haiku"))
	require.Equal(t, "openai", protocol.EndpointOf("openai.api_key"))
	require.Equal(t, "", protocol.EndpointOf("base_url"), "旧写法没有端点段")
	require.Equal(t, "", protocol.EndpointOf("gemini.base_url"), "不在白名单里")
	require.Equal(t, "", protocol.EndpointOf(""))
}

func TestRefsDeduplicatesAcrossBothEndpoints(t *testing.T) {
	content := []byte(`{
  "env": {
    "ANTHROPIC_BASE_URL": "{{provider.claude.base_url}}",
    "ANTHROPIC_AUTH_TOKEN": "{{provider.claude.auth_token}}",
    "OPENAI_BASE_URL": "{{provider.openai.base_url}}",
    "OPENAI_API_KEY": "{{provider.openai.api_key}}",
    "AGAIN": "{{provider.claude.base_url}}"
  }
}`)
	refs, err := protocol.Refs(content)
	require.NoError(t, err)

	var got []string
	for _, r := range refs {
		got = append(got, r.String())
	}
	// 去重 + 按 String() 升序
	require.Equal(t, []string{
		"provider.claude.auth_token",
		"provider.claude.base_url",
		"provider.openai.api_key",
		"provider.openai.base_url",
	}, got)
}

func TestRenderRoundTripWithBothEndpoints(t *testing.T) {
	content := []byte(`base={{provider.claude.base_url}} key={{provider.openai.api_key}}`)
	segs, err := protocol.Parse(content)
	require.NoError(t, err)
	out, err := protocol.Render(segs, func(r protocol.Ref) (string, bool) {
		switch r.Name {
		case "claude.base_url":
			return "https://open.bigmodel.cn/api/anthropic", true
		case "openai.api_key":
			return "sk-openai-abcdef", true
		}
		return "", false
	})
	require.NoError(t, err)
	require.Equal(t,
		"base=https://open.bigmodel.cn/api/anthropic key=sk-openai-abcdef",
		string(out))
}
```

同时**删掉**已有测试文件里所有引用 `protocol.RefCred` 或 `{{cred.` 的用例
（它们断言的是本期被废止的行为）。用这条命令找出来：

```bash
grep -rn "RefCred\|{{cred\." protocol/
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./protocol/ -run 'Provider|Cred|Endpoint|Refs|Render' -v
```

Expected: FAIL —— `ProviderKeys` 仍是六项、`EndpointOf` 未定义、`{{cred.x}}` 仍然通过。

- [ ] **Step 3: 实现**

Modify `protocol/placeholder.go`：

① `RefKind` 常量块删掉 `RefCred`，并写明编号退休：

```go
type RefKind uint8

// RefCred（原值 1）随 {{cred.*}} 一起废止（M1.6 spec §3.5）。
// 编号 1 **退休不复用**：agent 与 hub 的存量数据里还有按老编号写下的东西，
// 让新语义顶着旧编号是最难查的一类 bug。
const (
	RefVar      RefKind = 2
	RefMachine  RefKind = 3
	RefProvider RefKind = 4
)

func (k RefKind) String() string {
	switch k {
	case RefVar:
		return "var"
	case RefMachine:
		return "machine"
	case RefProvider:
		return "provider"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(k))
	}
}
```

② `ProviderKeys` 换成九项：

```go
// ProviderKeys 是 {{provider.*}} 允许的全部名字（M1.6 spec §3.1）。
//
// 名字是**端点限定**的两段名：端点段 + 字段段。为什么是显式前缀而不是
// 按文件路径推断（spec §3.2）：发布校验要能说出「这个文件引用了 claude
// 端点」，隐式方案下 hub 得先有一套「路径属于哪个工具」的推断；而 agent
// 的还原靠 key 名判断秘密档，两个端点的 key 不同时，同一个
// provider.auth_token 在不同文件里会指向不同的密文。
//
// Ref.Name 是**字段名，不是 Provider 名**：绑定在配置集里唯一，不需要指名。
// 顺序即 UI 展示顺序，也是 agent 还原的 rankOf 定序依据，不要重排。
var ProviderKeys = []string{
	"claude.base_url", "claude.auth_token",
	"claude.model", "claude.model_opus", "claude.model_sonnet", "claude.model_haiku",
	"openai.base_url", "openai.api_key",
	"openai.model",
}

// EndpointOf 取出端点限定名的端点段。
// 不在 ProviderKeys 白名单里的一律返回空串——调用方据此判定「这不是一个
// 端点限定名」，不必自己再切一次字符串。
func EndpointOf(name string) string {
	for _, k := range ProviderKeys {
		if k != name {
			continue
		}
		if i := strings.Index(name, "."); i > 0 {
			return name[:i]
		}
		return ""
	}
	return ""
}
```

③ `parseRef`：把 `validName` 从 provider 分支上摘掉，`cred` 分支删掉。
**注意顺序**——`validName` 现在必须在 `switch` 内部按分支调用，不能再在
`switch` 之前统一调用，否则带点的 provider 名会先被字符集拦下（spec §3.3）：

```go
func parseRef(body string) (Ref, error) {
	prefix, name, ok := strings.Cut(body, ".")
	if !ok {
		return Ref{}, fmt.Errorf("%w: %q 缺少 . 分隔", ErrBadPlaceholder, body)
	}
	switch prefix {
	case "var":
		if !validName(name) {
			return Ref{}, fmt.Errorf("%w: %q 的名字非法（只允许 [A-Za-z0-9_-]+）",
				ErrBadPlaceholder, body)
		}
		return Ref{Kind: RefVar, Name: name}, nil
	case "machine":
		if !validName(name) {
			return Ref{}, fmt.Errorf("%w: %q 的名字非法（只允许 [A-Za-z0-9_-]+）",
				ErrBadPlaceholder, body)
		}
		for _, k := range MachineKeys {
			if k == name {
				return Ref{Kind: RefMachine, Name: name}, nil
			}
		}
		return Ref{}, fmt.Errorf("%w: machine.%s 不是内置名（只有 %v）",
			ErrBadPlaceholder, name, MachineKeys)
	case "provider":
		// 不走 validName：端点限定名中间有点，字符集校验会把它拦下。
		// 这是**收紧不是放宽**——白名单本来就比字符集严格（spec §3.3）。
		for _, k := range ProviderKeys {
			if k == name {
				return Ref{Kind: RefProvider, Name: name}, nil
			}
		}
		return Ref{}, fmt.Errorf("%w: provider.%s 不是内置名（只有 %v）",
			ErrBadPlaceholder, name, ProviderKeys)
	default:
		return Ref{}, fmt.Errorf("%w: 未知前缀 %q", ErrBadPlaceholder, prefix)
	}
}
```

④ 文件头的包注释里，`Refs` 那段「下发时按它过滤凭据」改成「下发时按它裁剪
provider 的值」。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./protocol/ -v
```

Expected: PASS，全部用例（含既有的转义与 machine 用例）通过。

此时 `hub` 与 `agent` 还编译不过（它们还在引用 `protocol.RefCred`）——
那是 04 / 05 / 06 的活。用下面这条确认**只有**预期的那些包坏了：

```bash
go build ./... 2>&1 | grep -c RefCred
```

Expected: 输出一个非零数字，且 `go vet ./protocol/` 干净。

- [ ] **Step 5: 提交**

```bash
git add protocol/
git commit -m "feat(protocol): 占位符词法改为端点限定，废止 cred 前缀"
```

---

### Task 2: 前端 `lib/placeholder.ts` 同步

**Files:**
- Modify: `hub/internal/site/src/lib/placeholder.ts`
- Test: `hub/internal/site/src/lib/placeholder.test.ts`

**Interfaces:**
- Consumes: 无
- Produces: `PROVIDER_KEYS`（九项）、`endpointOf(name)`；`RefKind` 去掉 `'cred'`

三份实现的一致性靠双方各自的测试用同一组例子来保证（转义、非法名、白名单）。
下面的用例与 Task 1 的 Go 用例是刻意逐条对应的。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/site/src/lib/placeholder.test.ts`，并**删掉**其中所有
`cred.` 相关的既有用例：

```ts
import { describe, expect, it } from 'vitest'
import {
  PROVIDER_KEYS,
  endpointOf,
  parsePlaceholders,
  undefinedRefs,
  completions,
} from '@/lib/placeholder'

describe('端点限定的 provider 词法', () => {
  it('九个内置名与 Go 侧逐字符一致、顺序相同', () => {
    expect([...PROVIDER_KEYS]).toEqual([
      'claude.base_url',
      'claude.auth_token',
      'claude.model',
      'claude.model_opus',
      'claude.model_sonnet',
      'claude.model_haiku',
      'openai.base_url',
      'openai.api_key',
      'openai.model',
    ])
  })

  it('九个名字全部 parse 通过', () => {
    for (const k of PROVIDER_KEYS) {
      const { refs, errors } = parsePlaceholders(`{{provider.${k}}}`)
      expect(errors).toEqual([])
      expect(refs).toEqual([{ kind: 'provider', name: k }])
    }
  })

  it('旧写法报「不是内置名」', () => {
    const { errors } = parsePlaceholders('{{provider.base_url}}')
    expect(errors.join()).toContain('不是内置名')
  })

  it('cred 前缀报「未知前缀」', () => {
    const { refs, errors } = parsePlaceholders('{{cred.zhipu}}')
    expect(refs).toEqual([])
    expect(errors.join()).toContain('未知前缀')
  })

  it('端点对但字段不对也报错', () => {
    expect(parsePlaceholders('{{provider.claude.temperature}}').errors).toHaveLength(1)
  })

  it('var 的名字仍然做字符集校验', () => {
    expect(parsePlaceholders('{{var.a.b}}').errors.join()).toContain('名字非法')
  })

  it('endpointOf 只认白名单里的名字', () => {
    expect(endpointOf('claude.base_url')).toBe('claude')
    expect(endpointOf('openai.api_key')).toBe('openai')
    expect(endpointOf('base_url')).toBe('')
    expect(endpointOf('gemini.base_url')).toBe('')
  })

  it('provider.* 不参与未定义引用判定', () => {
    const text = '{{provider.openai.api_key}} {{var.workspace}}'
    expect(undefinedRefs(text, new Set(['var.workspace']))).toEqual([])
    expect(undefinedRefs(text, new Set())).toEqual([{ kind: 'var', name: 'workspace' }])
  })

  it('补全候选里是端点限定名', () => {
    expect(completions('provider.openai.', [])).toEqual([
      'provider.openai.api_key',
      'provider.openai.base_url',
      'provider.openai.model',
    ])
  })
})
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
cd hub/internal/site && npx vitest run src/lib/placeholder.test.ts
```

Expected: FAIL —— `endpointOf` 未导出、`PROVIDER_KEYS` 仍是六项、`cred` 仍被接受。

- [ ] **Step 3: 实现**

Modify `hub/internal/site/src/lib/placeholder.ts`：

```ts
export type RefKind = 'var' | 'machine' | 'provider'
```

```ts
/** {{provider.*}} 允许的全部名字，与 Go 侧 protocol.ProviderKeys 逐字符一致。 */
export const PROVIDER_KEYS = [
  'claude.base_url',
  'claude.auth_token',
  'claude.model',
  'claude.model_opus',
  'claude.model_sonnet',
  'claude.model_haiku',
  'openai.base_url',
  'openai.api_key',
  'openai.model',
] as const

/** 取出端点限定名的端点段。不在白名单里返回空串，与 Go 侧 EndpointOf 同义。 */
export function endpointOf(name: string): string {
  if (!(PROVIDER_KEYS as readonly string[]).includes(name)) return ''
  const i = name.indexOf('.')
  return i > 0 ? name.slice(0, i) : ''
}
```

`parsePlaceholders` 的分支：`prefix === 'cred'` 那一支删掉，`var` 单独一支
保留 `NAME_RE` 校验，`provider` 分支**跳过** `NAME_RE`：

```ts
    if (dot < 0) {
      errors.push(`{{${body}}} 缺少 . 分隔`)
    } else if (prefix === 'provider') {
      // provider 走白名单，不走字符集——端点限定名中间有点。
      if ((PROVIDER_KEYS as readonly string[]).includes(name)) {
        push(refs, seen, { kind: 'provider', name })
      } else {
        errors.push(`provider.${name} 不是内置名（只有 ${PROVIDER_KEYS.join(' / ')}）`)
      }
    } else if (!NAME_RE.test(name)) {
      errors.push(`{{${body}}} 的名字非法（只允许 [A-Za-z0-9_-]）`)
    } else if (prefix === 'var') {
      push(refs, seen, { kind: 'var', name })
    } else if (prefix === 'machine') {
      if ((MACHINE_KEYS as readonly string[]).includes(name)) {
        push(refs, seen, { kind: 'machine', name })
      } else {
        errors.push(`machine.${name} 不是内置名（只有 ${MACHINE_KEYS.join(' / ')}）`)
      }
    } else {
      errors.push(`未知前缀 ${prefix}`)
    }
```

`undefinedRefs` 的注释与判断把 `cred` 拿掉（现在只有 `var` 会被判未定义）：

```ts
/**
 * known 的元素形如 "var.bar"。
 *
 * machine.* 是内置值；provider.* 的「已定义」由绑定与端点校验判定
 * （M1.6 spec §4），不走未定义引用这条路——否则每个绑了服务的配置集
 * 都会在编辑器里挂满假告警。
 */
export function undefinedRefs(text: string, known: Set<string>): Ref[] {
  return parsePlaceholders(text).refs.filter(
    (r) => r.kind === 'var' && !known.has(`var.${r.name}`),
  )
}
```

`completions` 不变（它已经是 `PROVIDER_KEYS.map((k) => \`provider.${k}\`)`）。

- [ ] **Step 4: 运行测试确认通过**

```bash
cd hub/internal/site && npx vitest run src/lib/placeholder.test.ts
```

Expected: PASS。

其余前端文件此时会有 TypeScript 报错（`CredentialRecord` 相关的还没动），
那是 09 的活。只确认词法这一份干净：

```bash
cd hub/internal/site && npx tsc --noEmit src/lib/placeholder.ts --strict --skipLibCheck --moduleResolution bundler --module esnext
```

Expected: 无输出。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/site/src/lib/placeholder.ts hub/internal/site/src/lib/placeholder.test.ts
git commit -m "feat(web): 前端占位符词法跟进端点限定名"
```
