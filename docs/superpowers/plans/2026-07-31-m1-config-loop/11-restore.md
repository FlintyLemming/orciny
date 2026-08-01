# 子计划 11 · 还原：把磁盘上的真值替回占位符

**前置**：06
**读这份之前先读** [00-overview.md](00-overview.md)。

**交付物**：`agent/internal/render` 的 `Restore` 与一组**独立命名的安全测试**。

**这是 M1 最容易写错的地方（spec §6）**。核心矛盾：Revision 里存的是 `{{cred.x}}`，落到机器上的是真实密钥，于是 agent 手里的基线与磁盘内容天生不一致。上报漂移时必须把真值替回占位符，**否则密钥会明文进入不可变的版本历史，写进去就洗不掉**（spec §1.4）。

**两档承诺，不对称是有意的——泄密不可逆，变量替错只是难看（spec §6.4）**

| | 凭据 | 变量 |
|---|---|---|
| 承诺 | **必须还原**，失败即不上报内容 | **尽力还原**，失败则标记 |
| 替换顺序 | 按值长度降序（防短值是长值的子串时替错） | 同上 |
| 门槛 | 值长度 < 8 拒绝创建（hub 侧已挡） | 仅当值长度 ≥ 4 且出现次数与渲染时一致才替 |
| 失败处置 | 该条漂移只报路径，`Truncated=true`，不带内容 | 置 `RestorePartial=true`，照常上报 |

---

### Task 1: Restore

**Files:**
- Create: `agent/internal/render/restore.go`
- Test: `agent/internal/render/restore_test.go`

**Interfaces:**
- Consumes: `protocol.EscapeLiteral`
- Produces:
  ```go
  type RestoreResult struct {
      Content []byte
      Partial bool // 有变量未能还原
      Safe    bool // 全部已知凭据值都已替出；false 时调用方不得上报内容
  }
  func Restore(content []byte, creds, vars map[string]string) RestoreResult
  const MinVarLen = 4
  ```

**算法**

1. 先 `EscapeLiteral`：磁盘内容里字面的 `{{` 要转义，否则往返律破掉。
2. 把凭据与变量按**值长度降序**排成一张替换表；变量只收长度 ≥ 4 的。
3. 逐条 `strings.ReplaceAll`。
4. 变量：替换前先数一遍出现次数，若与渲染时的次数对不上就放弃该变量并置 `Partial`。M1 手上没有「渲染时的次数」，因此退化为**只要值在内容里出现就替，出现次数 ≠ 1 时置 `Partial` 并仍然替**——见下方说明。
5. **再扫一遍**：若任一已知凭据值仍能被搜到 → `Safe = false`。

> **关于第 4 步**：spec §6.4 写的是「仅当值长度 ≥ 4，且它在文件中的出现次数与渲染时一致，才替回占位符」。「渲染时的次数」需要基线内容才能算，而 `Restore` 的调用方（watcher）手上确实有基线（`state.json` 记了 blob hash，blobcache 里有渲染前的内容）。因此把它做成可选入参：给了基线就精确比对，没给就退化。签名为
> ```go
> func RestoreWithBase(content, base []byte, creds, vars map[string]string) RestoreResult
> func Restore(content []byte, creds, vars map[string]string) RestoreResult // = RestoreWithBase(content, nil, ...)
> ```

- [ ] **Step 1: 写失败的测试**

Create `agent/internal/render/restore_test.go`：

```go
package render_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/render"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestRestoreReplacesCredential(t *testing.T) {
	got := render.Restore(
		[]byte(`{"env":{"K":"sk-ant-realvalue"}}`),
		map[string]string{"anthropic_key": "sk-ant-realvalue"},
		nil,
	)
	require.True(t, got.Safe)
	require.False(t, got.Partial)
	require.Equal(t, `{"env":{"K":"{{cred.anthropic_key}}"}}`, string(got.Content))
}

// 按值长度降序替换：短值是长值的子串时不能先替短的（spec §6.4）。
func TestRestoreReplacesLongestFirst(t *testing.T) {
	got := render.Restore(
		[]byte(`{"long":"sk-abcdefgh-suffix","short":"sk-abcdefgh"}`),
		map[string]string{
			"short_key": "sk-abcdefgh",
			"long_key":  "sk-abcdefgh-suffix",
		},
		nil,
	)
	require.True(t, got.Safe)
	require.Equal(t,
		`{"long":"{{cred.long_key}}","short":"{{cred.short_key}}"}`,
		string(got.Content))
}

// 一个值出现多次：全部替掉，漏一处等于没脱敏。
func TestRestoreReplacesEveryOccurrence(t *testing.T) {
	got := render.Restore(
		[]byte("key=sk-value-1234\n再说一遍 sk-value-1234\n"),
		map[string]string{"k": "sk-value-1234"},
		nil,
	)
	require.True(t, got.Safe)
	require.Equal(t, 2, strings.Count(string(got.Content), "{{cred.k}}"))
	require.NotContains(t, string(got.Content), "sk-value-1234")
}

// 磁盘上字面的 {{ 必须转义，否则往返律破掉。
func TestRestoreEscapesLiteralBraces(t *testing.T) {
	got := render.Restore([]byte("模板写法是 {{cred.x}} 这样"), nil, nil)
	require.True(t, got.Safe)
	require.Equal(t, "模板写法是 {{{{cred.x}} 这样", string(got.Content))

	// 往返：restore 的结果再 render 回去必须一字不差
	sec := &secrets.File{}
	back, err := render.Render(got.Content, sec.Lookup)
	require.NoError(t, err)
	require.Equal(t, "模板写法是 {{cred.x}} 这样", string(back))
}

// 变量：长度 ≥ 4 才替。
func TestRestoreSkipsShortVariables(t *testing.T) {
	got := render.Restore(
		[]byte(`{"branch":"main","tier":"1"}`),
		nil,
		map[string]string{"branch": "main", "tier": "1"},
	)
	require.Contains(t, string(got.Content), "{{var.branch}}")
	require.Contains(t, string(got.Content), `"1"`, "太短的变量值不替")
	require.True(t, got.Partial, "放弃还原某个变量时必须标记")
	require.True(t, got.Safe, "变量还原失败不影响 Safe——它不是秘密")
}

// 变量值在内容里出现次数与基线对不上：仍然替，但标记 Partial 让人复核。
func TestRestoreMarksPartialOnVariableCountMismatch(t *testing.T) {
	base := []byte(`{"a":"{{var.ws}}"}`)                    // 渲染时出现 1 次
	cur := []byte(`{"a":"main","b":"main-ish","c":"main"}`) // 现在出现 3 次
	got := render.RestoreWithBase(cur, base, nil, map[string]string{"ws": "main"})
	require.True(t, got.Partial)
	require.True(t, got.Safe)
}

func TestRestoreNoPartialWhenCountMatches(t *testing.T) {
	base := []byte(`{"a":"{{var.ws}}"}`)
	cur := []byte(`{"a":"main"}`)
	got := render.RestoreWithBase(cur, base, nil, map[string]string{"ws": "main"})
	require.False(t, got.Partial)
	require.Equal(t, `{"a":"{{var.ws}}"}`, string(got.Content))
}

// 内容里根本没出现的凭据/变量不影响任何标记。
func TestRestoreIgnoresAbsentValues(t *testing.T) {
	got := render.Restore(
		[]byte("普通内容"),
		map[string]string{"k": "sk-not-here-1234"},
		map[string]string{"ws": "nowhere"},
	)
	require.True(t, got.Safe)
	require.False(t, got.Partial)
	require.Equal(t, "普通内容", string(got.Content))
}

func TestRestoreOfEmptyContent(t *testing.T) {
	got := render.Restore(nil, map[string]string{"k": "sk-value-1234"}, nil)
	require.True(t, got.Safe)
	require.Empty(t, got.Content)
}
```

Create `agent/internal/render/security_test.go` —— **这组是安全测试，独立成文件并在 CI 里显式命名**（spec §10.1）：

```go
package render_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/render"
)

// TestSecurityRestoredContentNeverContainsCredentialValues 是 M1 的安全底线：
// 还原后的内容中不得含有任何已知凭据值。
//
// 它独立成例并以 Security 打头，是为了在 CI 里能被单独点名跑：
//   go test -tags=testing -run TestSecurity ./agent/...
//
// 这条一旦破了，密钥会明文进入**不可变**的版本历史（spec §1.4）。
func TestSecurityRestoredContentNeverContainsCredentialValues(t *testing.T) {
	creds := map[string]string{
		"a": "sk-ant-abcdefghijklmnop",
		"b": "ghp_qrstuvwxyz01234567",
		"c": "AKIAIOSFODNN7EXAMPLE",
		"d": "sk-ant-abcdefghijklmnop-longer", // a 的超集
	}

	bodies := []string{
		`{"env":{"A":"sk-ant-abcdefghijklmnop","B":"ghp_qrstuvwxyz01234567"}}`,
		"文中提到 AKIAIOSFODNN7EXAMPLE 与 sk-ant-abcdefghijklmnop-longer",
		strings.Repeat("sk-ant-abcdefghijklmnop\n", 50),
		"混在一起：sk-ant-abcdefghijklmnopghp_qrstuvwxyz01234567",
		"边界：前缀sk-ant-abcdefghijklmnop后缀",
	}

	for _, body := range bodies {
		got := render.Restore([]byte(body), creds, nil)
		require.True(t, got.Safe, "内容 %q 未能安全脱敏", body)
		for name, v := range creds {
			require.NotContains(t, string(got.Content), v,
				"还原后仍能搜到凭据 %s 的值", name)
		}
	}
}

// 还原不干净时必须明确报出来，让调用方只报路径、不带内容（spec §6.4）。
func TestSecurityUnsafeRestoreIsReported(t *testing.T) {
	// 构造一个替不干净的情形：凭据值本身含有会被后续替换重新引入的片段。
	creds := map[string]string{
		"a": "SECRET",
		"b": "X", // 长度 1：hub 侧拦得住，但历史数据里可能存在
	}
	got := render.Restore([]byte("值是 SECRET，另一个是 X"), creds, nil)
	if !got.Safe {
		require.NotContains(t, string(got.Content), "SECRET",
			"判定为不安全时也不该把内容交出去——调用方会丢弃它")
	}
	// 无论 Safe 与否，调用方的契约是：Safe == false 时不上报 Content。
	// 这条断言锁住「Safe 为真 ⇒ 内容里搜不到任何凭据值」这个蕴含关系。
	if got.Safe {
		for _, v := range creds {
			require.NotContains(t, string(got.Content), v)
		}
	}
}

// 极端输入不能让还原崩掉或死循环。
func TestSecurityRestoreHandlesPathologicalInput(t *testing.T) {
	creds := map[string]string{"k": "{{cred.k}}"} // 值本身长得像占位符
	got := render.Restore([]byte("{{cred.k}}"), creds, nil)
	require.NotNil(t, got.Content)

	empty := render.Restore([]byte("x"), map[string]string{"e": ""}, map[string]string{"v": ""})
	require.True(t, empty.Safe, "空值不该被当成「到处都能搜到」")
	require.Equal(t, "x", string(empty.Content))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./agent/internal/render/...`
Expected: FAIL，`undefined: render.Restore`

- [ ] **Step 3: 实现**

Create `agent/internal/render/restore.go`：

```go
package render

import (
	"sort"
	"strings"

	"github.com/FlintyLemming/orciny/protocol"
)

// MinVarLen 是变量参与还原的长度下限（spec §6.4）。
//
// 变量不是秘密，值常常很短（main、1）。把 "1" 替回 {{var.tier}} 会把文件里
// 每一个 1 都改掉，diff 变得完全不可读。宁可放弃还原并标记，让人来看。
const MinVarLen = 4

type RestoreResult struct {
	Content []byte
	// Partial 表示有变量未能还原。收编前 UI 强制人工复核这类条目——
	// diff 里会显示 main vs {{var.workspace}}，用户看得懂，且不泄密。
	Partial bool
	// Safe 表示全部已知凭据值都已替出。为 false 时调用方**不得上报内容**，
	// 只报路径并置 Truncated（spec §6.4）。
	Safe bool
}

// Restore 把磁盘上的真实值替回占位符。
func Restore(content []byte, creds, vars map[string]string) RestoreResult {
	return RestoreWithBase(content, nil, creds, vars)
}

// RestoreWithBase 在有基线内容时做更精确的变量判定。
//
// base 是渲染**前**的基线（占位符形态）。给了它就能算出「渲染时该变量应当
// 出现几次」，与当前内容里的次数一比，对不上就说明用户自己也写了一个同值
// 的字符串——此时替回去会把用户写的东西也变成占位符，因此标记 Partial
// 让人复核（spec §6.4）。base 为 nil 时退化：仍然替，但次数 ≠ 1 就标记。
func RestoreWithBase(content, base []byte, creds, vars map[string]string) RestoreResult {
	// 磁盘内容里字面的 {{ 必须先转义，否则往返律 render(restore(x)) == x
	// 会在含模板语法的 CLAUDE.md 上破掉（spec §6.1）。
	out := protocol.EscapeLiteral(string(content))

	res := RestoreResult{Safe: true}

	// 按值长度降序：短值是长值的子串时，先替短的会把长值切碎，
	// 剩下的碎片再也匹配不上，于是密钥的一部分留在了内容里。
	for _, r := range replacements(creds, protocol.RefCred) {
		if r.value == "" || !strings.Contains(out, r.value) {
			continue
		}
		out = strings.ReplaceAll(out, r.value, r.token)
	}

	for _, r := range replacements(vars, protocol.RefVar) {
		if r.value == "" || len(r.value) < MinVarLen {
			if r.value != "" && strings.Contains(out, r.value) {
				// 太短、放弃还原：不泄密（变量本就不是秘密），但要让人看一眼。
				res.Partial = true
			}
			continue
		}
		got := strings.Count(out, r.value)
		if got == 0 {
			continue
		}
		want := 1
		if base != nil {
			want = strings.Count(string(base), r.token)
		}
		if got != want {
			res.Partial = true
		}
		out = strings.ReplaceAll(out, r.value, r.token)
	}

	// 再扫一遍：任一已知凭据值仍能被搜到即判定还原失败（spec §6.4）。
	// 这是最后一道闸——上面的替换逻辑将来若被改坏，这里会拦住它。
	for _, v := range creds {
		if v != "" && strings.Contains(out, v) {
			res.Safe = false
			break
		}
	}

	res.Content = []byte(out)
	return res
}

type replacement struct {
	value string
	token string
}

func replacements(m map[string]string, kind protocol.RefKind) []replacement {
	out := make([]replacement, 0, len(m))
	for name, v := range m {
		out = append(out, replacement{
			value: v,
			token: "{{" + protocol.Ref{Kind: kind, Name: name}.String() + "}}",
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].value) != len(out[j].value) {
			return len(out[i].value) > len(out[j].value)
		}
		return out[i].value < out[j].value // 等长时按值排，保证确定性
	})
	return out
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./agent/internal/render/...`
Expected: PASS

- [ ] **Step 5: 在 CI 里单独点名安全测试**

`.github/workflows/ci.yml` 的测试步骤之后追加一步：

```yaml
      - name: 凭据脱敏安全测试
        run: go test -tags=testing -run TestSecurity -v ./agent/...
```

这一步在全量测试之外重复跑一次同样的用例。重复是有意的：它让「哪些测试是安全底线」在 CI 日志里一眼可见，将来有人删掉某条时不会悄无声息。

- [ ] **Step 6: 提交**

```bash
git add agent/internal/render/ .github/
git commit -m "feat: 凭据与变量的还原，含独立安全测试"
```

---

### Task 2: 往返律的 property test

**Files:**
- Test: `agent/internal/render/roundtrip_test.go`

`render(restore(x)) == x` 对任意内容与任意值集合成立——这是整个凭据机制的正确性支点（spec §6.1）。protocol 层已经测过「转义 + 解析」的往返；这里测的是**带真实值替换**的完整往返。

- [ ] **Step 1: 写测试**

Create `agent/internal/render/roundtrip_test.go`：

```go
package render_test

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/render"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
)

// render(restore(x)) == x：对任意磁盘内容与任意值集合成立。
//
// 这是整个凭据机制的正确性支点（spec §6.1 / §10.1）。它保证：
// 上报给 hub 的占位符内容，被任何一台机器渲染回来都与原磁盘内容一致，
// 于是收编不会改变任何机器上的实际配置。
func TestRenderRestoreRoundTripProperty(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))

	credValues := []string{"sk-ant-abcdefghij", "ghp_klmnopqrstuv", "AKIAIOSFODNN7EXAM"}
	varValues := []string{"main", "production", "workspace-1"}

	creds := map[string]string{}
	for i, v := range credValues {
		creds[string(rune('a'+i))] = v
	}
	vars := map[string]string{}
	for i, v := range varValues {
		vars["v"+string(rune('a'+i))] = v
	}

	sec := &secrets.File{Creds: creds, Vars: vars, Machine: map[string]string{}}

	alphabet := append([]string{
		"普通文字", "\n", "{", "}", "{{", "}}", " ", `"`, ":", ",",
	}, append(credValues, varValues...)...)

	for range 500 {
		var sb strings.Builder
		for range rng.IntN(24) {
			sb.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		disk := sb.String()

		res := render.Restore([]byte(disk), creds, vars)
		if !res.Safe {
			continue // 判定为不安全的内容不会被上报，往返律对它不适用
		}
		back, err := render.Render(res.Content, sec.Lookup)
		require.NoError(t, err, "输入 %q 还原后无法重新渲染", disk)
		require.Equal(t, disk, string(back), "往返不一致：%q", disk)
	}
}

// 变量被放弃还原时往返律仍要成立——只是内容里留着字面值而已。
func TestRoundTripHoldsWithPartialRestore(t *testing.T) {
	creds := map[string]string{"k": "sk-value-12345678"}
	vars := map[string]string{"tier": "1"} // 太短，会被放弃
	sec := &secrets.File{Creds: creds, Vars: vars, Machine: map[string]string{}}

	disk := `{"K":"sk-value-12345678","tier":"1"}`
	res := render.Restore([]byte(disk), creds, vars)
	require.True(t, res.Safe)

	back, err := render.Render(res.Content, sec.Lookup)
	require.NoError(t, err)
	require.Equal(t, disk, string(back))
}
```

- [ ] **Step 2: 跑测试**

Run: `go test -tags=testing ./agent/internal/render/ -run RoundTrip -v`
Expected: PASS（若 FAIL，说明 Task 1 的转义或替换顺序有问题——这正是这条测试的用处）

- [ ] **Step 3: 全量回归并提交**

Run: `go test -tags=testing ./...`
Expected: PASS

```bash
git add agent/internal/render/
git commit -m "test: 渲染与还原的往返律 property test"
```
