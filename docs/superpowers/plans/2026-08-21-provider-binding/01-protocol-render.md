# 子计划 01 · 协议与渲染

**前置**：无（与 02 可并行）
**读这份之前先读** [00-overview.md](00-overview.md) 的 Global Constraints 与全局接口契约。

**交付物**：`protocol` 的 `RefProvider` / `ProviderKeys` / `ConfigSnapshot.Provider`；agent 侧 `secrets.File.Provider`、`render.Values` 与两档分派、`watcher` / `syncer` 接线。

**为什么先做这个**：spec §4.1 说得很清楚——`render.Render` 的 lookup 已经是通用的，**`render.go` 本身一行不动**。本子计划的全部工作是把「值从哪来」这条线接通，外加把还原的两档分派做对。此时还没有任何 UI 与数据库改动，全部用测试驱动。

---

### Task 1: `provider` 前缀进占位符词法

**Files:**
- Modify: `protocol/placeholder.go`
- Test: `protocol/placeholder_test.go`（追加，不改已有用例）

**Interfaces:**
- Consumes: 无
- Produces: `protocol.RefProvider`（`RefKind = 4`）、`protocol.ProviderKeys`、`parseRef` 的 `provider` 分支

- [ ] **Step 1: 写失败的测试**

追加到 `protocol/placeholder_test.go`：

```go
func TestParseProviderRefs(t *testing.T) {
	segs, err := protocol.Parse([]byte(
		`{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}",` +
			`"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}",` +
			`"ANTHROPIC_MODEL":"{{provider.model}}"}}`))
	require.NoError(t, err)

	var got []string
	for _, s := range segs {
		if s.Ref != nil {
			require.Equal(t, protocol.RefProvider, s.Ref.Kind)
			got = append(got, s.Ref.String())
		}
	}
	require.Equal(t, []string{
		"provider.base_url", "provider.auth_token", "provider.model",
	}, got)
}

// 与 machine.* 的既有测试对称：白名单外的名字必须被拒。
func TestParseRejectsUnknownProviderKey(t *testing.T) {
	_, err := protocol.Parse([]byte(`{{provider.temperature}}`))
	require.ErrorIs(t, err, protocol.ErrBadPlaceholder)
	require.Contains(t, err.Error(), "provider.temperature")
}

func TestProviderKeysAreExactlySix(t *testing.T) {
	require.Equal(t, []string{
		"base_url", "auth_token", "model", "model_opus", "model_sonnet", "model_haiku",
	}, protocol.ProviderKeys)
}

func TestRefKindStringCoversProvider(t *testing.T) {
	require.Equal(t, "provider", protocol.RefProvider.String())
}

// Refs 提取要把 provider 一并去重排序吐出来——发布期靠它填 provider_keys。
func TestRefsIncludesProvider(t *testing.T) {
	refs, err := protocol.Refs([]byte(
		`{{provider.model}}{{cred.k}}{{provider.model}}{{provider.base_url}}`))
	require.NoError(t, err)
	var got []string
	for _, r := range refs {
		got = append(got, r.String())
	}
	require.Equal(t, []string{"cred.k", "provider.base_url", "provider.model"}, got)
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./protocol/ -run 'Provider|RefKindString|RefsIncludes' -v
```

Expected: 编译失败，`undefined: protocol.RefProvider`。

- [ ] **Step 3: 实现**

在 `protocol/placeholder.go` 的 `RefKind` 常量块追加：

```go
const (
	RefCred    RefKind = 1
	RefVar     RefKind = 2
	RefMachine RefKind = 3
	RefProvider RefKind = 4
)
```

`String()` 追加分支：

```go
	case RefProvider:
		return "provider"
```

在 `MachineKeys` 下方追加：

```go
// ProviderKeys 是 {{provider.*}} 允许的全部名字（M1.5 spec §3.1）。
//
// Ref.Name 是**字段名，不是 Provider 名**：绑定在配置集里唯一，不需要指名。
// 与 MachineKeys 同理，它属于「词法词汇表」——两侧必须认同同一组内置名。
// 顺序即 UI 展示顺序，不要重排。
var ProviderKeys = []string{
	"base_url", "auth_token", "model", "model_opus", "model_sonnet", "model_haiku",
}
```

`parseRef` 的 switch 里，`machine` 分支之后追加：

```go
	case "provider":
		for _, k := range ProviderKeys {
			if k == name {
				return Ref{Kind: RefProvider, Name: name}, nil
			}
		}
		return Ref{}, fmt.Errorf("%w: provider.%s 不是内置名（只有 %v）",
			ErrBadPlaceholder, name, ProviderKeys)
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./protocol/ -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add protocol/placeholder.go protocol/placeholder_test.go && git commit -m "feat(protocol): 占位符加 provider 前缀与六个内置名"
```

---

### Task 2: `ConfigSnapshot.Provider` 与 wire 兼容

**Files:**
- Modify: `protocol/messages_m1.go`
- Test: `protocol/messages_m1_test.go`（追加）

**Interfaces:**
- Consumes: Task 1 的 `ProviderKeys`
- Produces: `ConfigSnapshot.Provider map[string]string`（CBOR keyasint = 10）

- [ ] **Step 1: 写失败的测试**

追加到 `protocol/messages_m1_test.go`：

```go
func TestConfigSnapshotCarriesProvider(t *testing.T) {
	in := protocol.ConfigSnapshot{
		ConfigSetID: "cs1", RevisionID: "r1", Seq: 3, Checksum: "abc",
		Provider: map[string]string{
			"base_url":   "https://open.bigmodel.cn/api/anthropic",
			"auth_token": "sk-zhipu-1234567890",
			"model":      "glm-5.1",
		},
	}
	b, err := protocol.Marshal(in)
	require.NoError(t, err)

	var out protocol.ConfigSnapshot
	require.NoError(t, protocol.Unmarshal(b, &out))
	require.Equal(t, in.Provider, out.Provider)
}

// 旧 agent 的 ConfigSnapshot 没有字段 10。wire 层必须兼容：
// 收到新字段静默忽略，而不是解码报错（spec §3.4）。
func TestOldSnapshotShapeIgnoresProviderField(t *testing.T) {
	// legacySnapshot 是 M1 的形状，逐字符照抄 0..9，故意不含 10。
	type legacySnapshot struct {
		ConfigSetID string                 `cbor:"0,keyasint"`
		RevisionID  string                 `cbor:"1,keyasint"`
		Seq         uint32                 `cbor:"2,keyasint"`
		Manifest    []byte                 `cbor:"3,keyasint"`
		Files       []protocol.FileEntry   `cbor:"4,keyasint"`
		Checksum    string                 `cbor:"5,keyasint"`
		Credentials map[string]string      `cbor:"6,keyasint,omitempty"`
		Variables   map[string]string      `cbor:"7,keyasint,omitempty"`
		IgnorePaths []string               `cbor:"8,keyasint,omitempty"`
		Mode        uint8                  `cbor:"9,keyasint,omitempty"`
	}

	b, err := protocol.Marshal(protocol.ConfigSnapshot{
		ConfigSetID: "cs1", RevisionID: "r1", Checksum: "abc",
		Credentials: map[string]string{"k": "v"},
		Provider:    map[string]string{"base_url": "https://example.test"},
	})
	require.NoError(t, err)

	var old legacySnapshot
	require.NoError(t, protocol.Unmarshal(b, &old), "旧形状必须能解出来")
	require.Equal(t, "cs1", old.ConfigSetID)
	require.Equal(t, map[string]string{"k": "v"}, old.Credentials)
}

// 反向：新 agent 收到旧 hub 的快照，Provider 为 nil 而不是报错。
func TestNewSnapshotShapeAcceptsMissingProvider(t *testing.T) {
	type legacySnapshot struct {
		ConfigSetID string `cbor:"0,keyasint"`
		RevisionID  string `cbor:"1,keyasint"`
		Checksum    string `cbor:"5,keyasint"`
	}
	b, err := protocol.Marshal(legacySnapshot{
		ConfigSetID: "cs1", RevisionID: "r1", Checksum: "abc",
	})
	require.NoError(t, err)

	var snap protocol.ConfigSnapshot
	require.NoError(t, protocol.Unmarshal(b, &snap))
	require.Nil(t, snap.Provider)
}
```

> 若 `protocol.Marshal` / `protocol.Unmarshal` 不是该包对外的编解码函数名，用 `protocol/codec.go` 里实际导出的那两个（见 `codec_test.go` 的用法），其余不变。

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./protocol/ -run 'Snapshot.*Provider|ProviderField' -v
```

Expected: FAIL，`unknown field Provider`。

- [ ] **Step 3: 实现**

`protocol/messages_m1.go` 的 `ConfigSnapshot` 末尾追加：

```go
	// Provider 是服务绑定注入的六个内置名 → 真实值（M1.5 spec §3.4）。
	// 只含本 Revision 实际引用到的键（refs.provider_keys 裁剪，spec §5.1）——
	// 少一个键少一处泄露面，auth_token 尤其。
	//
	// omitempty + 新 keyasint 键：旧 agent 解码时静默忽略，wire 层兼容。
	Provider map[string]string `cbor:"10,keyasint,omitempty"`
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./protocol/ -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add protocol/messages_m1.go protocol/messages_m1_test.go && git commit -m "feat(protocol): ConfigSnapshot 追加 Provider 字段"
```

---

### Task 3: agent 的凭据缓存认得 provider

**Files:**
- Modify: `agent/internal/secrets/secrets.go`
- Test: `agent/internal/secrets/secrets_test.go`（追加）

**Interfaces:**
- Consumes: `protocol.RefProvider`
- Produces: `secrets.File.Provider map[string]string`，`Lookup` 的 provider 分支

- [ ] **Step 1: 写失败的测试**

追加到 `agent/internal/secrets/secrets_test.go`：

```go
func TestLookupProvider(t *testing.T) {
	f := &secrets.File{
		Provider: map[string]string{
			"base_url": "https://open.bigmodel.cn/api/anthropic",
			"model":    "glm-5.1",
		},
	}
	v, ok := f.Lookup(protocol.Ref{Kind: protocol.RefProvider, Name: "base_url"})
	require.True(t, ok)
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic", v)

	// 没下发的键必须返回 false——渲染要因此整体失败，
	// 而不是把空串写进 settings.json（M1 spec §6.1）。
	_, ok = f.Lookup(protocol.Ref{Kind: protocol.RefProvider, Name: "model_opus"})
	require.False(t, ok)
}

func TestSaveLoadKeepsProvider(t *testing.T) {
	dir := t.TempDir()
	want := &secrets.File{
		Creds:    map[string]string{"k": "sk-value-1234"},
		Vars:     map[string]string{"ws": "main"},
		Machine:  map[string]string{"hostname": "mac"},
		Provider: map[string]string{"base_url": "https://example.test", "model": "glm-5.1"},
	}
	require.NoError(t, secrets.Save(dir, want))
	got, err := secrets.Load(dir)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestLoadMissingReturnsEmptyProvider(t *testing.T) {
	f, err := secrets.Load(t.TempDir())
	require.NoError(t, err)
	require.Empty(t, f.Provider)
	require.NotNil(t, f.Provider)
}

func TestEqualComparesProvider(t *testing.T) {
	a := &secrets.File{Provider: map[string]string{"model": "glm-5.1"}}
	b := &secrets.File{Provider: map[string]string{"model": "glm-4.7"}}
	require.False(t, a.Equal(b))
	require.True(t, a.Equal(&secrets.File{Provider: map[string]string{"model": "glm-5.1"}}))
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./agent/internal/secrets/ -v
```

Expected: 编译失败，`unknown field Provider`。

- [ ] **Step 3: 实现**

`agent/internal/secrets/secrets.go`：

```go
type File struct {
	Creds   map[string]string `json:"creds"`
	Vars    map[string]string `json:"vars"`
	Machine map[string]string `json:"machine"`
	// Provider 是服务绑定注入的六个内置名（M1.5 spec §3.1）。
	// auth_token 是秘密，因此本文件仍然一律 0600。
	Provider map[string]string `json:"provider"`
}
```

`Load` 的三处 nil 兜底后追加：

```go
	if f.Provider == nil {
		f.Provider = map[string]string{}
	}
```

`Load` 里 `os.IsNotExist` 的早返回也要带上：

```go
		return &File{
			Creds: map[string]string{}, Vars: map[string]string{},
			Machine: map[string]string{}, Provider: map[string]string{},
		}, nil
```

`Lookup` 追加分支：

```go
	case protocol.RefProvider:
		v, ok := f.Provider[r.Name]
		return v, ok
```

`Equal` 追加：

```go
	return sameMap(f.Creds, o.Creds) && sameMap(f.Vars, o.Vars) &&
		sameMap(f.Machine, o.Machine) && sameMap(f.Provider, o.Provider)
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./agent/internal/secrets/ -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add agent/internal/secrets/ && git commit -m "feat(agent): secrets 缓存承载 provider 六个值"
```

---

### Task 4: 还原的两档分派

**Files:**
- Modify: `agent/internal/render/restore.go`
- Modify: `agent/internal/render/restore_test.go`（已有用例要改签名）
- Modify: `agent/internal/render/roundtrip_test.go`（已有用例要改签名，并扩展 property test）
- Modify: `agent/internal/render/security_test.go`（已有用例要改签名）

**Interfaces:**
- Consumes: `protocol.RefProvider`
- Produces: `render.Values`；`Restore(content []byte, v Values) RestoreResult`；`RestoreWithBase(content, base []byte, v Values) RestoreResult`

**分档口径（spec §4.2，实现时逐条对照）**

| 值 | 档位 | 理由 |
|---|---|---|
| `auth_token` | 凭据档（必须还原，失败即 `Safe=false`） | 是秘密，泄露不可逆 |
| `base_url` | 变量档（尽力而为） | 不是秘密；长度远超 `MinVarLen` |
| `model` / `model_opus` / `model_sonnet` / `model_haiku` | 变量档 | 可能很短，`MinVarLen` 会让短 id 自动降级为 `RestorePartial` |

**四槽同值的处理（spec §4.2 末句）**：按值去重，保留排序后的第一条。去重之后 `got != want` 的既有计数逻辑会自动把它标成 `Partial`，让人复核——不需要额外代码。**不要**试图用基线做「按出现顺序分别还原」：那会在 agent 最要紧的正确性函数里加第二条隐晦的排序规则，只为换一张已经禁用收编的卡片上的 diff 好看。

- [ ] **Step 1: 写失败的测试**

新增到 `agent/internal/render/restore_test.go`：

```go
func TestRestoreAuthTokenGoesToCredentialTier(t *testing.T) {
	v := render.Values{
		Provider: map[string]string{"auth_token": "sk-zhipu-abcdefghij"},
	}
	res := render.Restore([]byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-zhipu-abcdefghij"}}`), v)
	require.True(t, res.Safe)
	require.Equal(t, `{"env":{"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}"}}`,
		string(res.Content))
	require.NotContains(t, string(res.Content), "sk-zhipu")
}

// auth_token 是秘密：替不回去就必须判定不安全，调用方只报路径（spec §4.2）。
func TestRestoreUnsafeWhenAuthTokenSurvives(t *testing.T) {
	v := render.Values{
		Provider: map[string]string{"auth_token": "sk-zhipu-abcdefghij"},
	}
	// 值被人为切碎成两段，替换替不干净，最后一道闸要拦住它。
	disk := `{"a":"sk-zhipu-abcdefghij","b":"sk-zhipu-abcdefghij{{"}`
	res := render.Restore([]byte(disk), v)
	if res.Safe {
		require.NotContains(t, string(res.Content), "sk-zhipu-abcdefghij")
	}
}

func TestRestoreBaseURLGoesToVarTier(t *testing.T) {
	v := render.Values{
		Provider: map[string]string{"base_url": "https://open.bigmodel.cn/api/anthropic"},
	}
	res := render.Restore(
		[]byte(`{"env":{"ANTHROPIC_BASE_URL":"https://open.bigmodel.cn/api/anthropic"}}`), v)
	require.True(t, res.Safe)
	require.Contains(t, string(res.Content), "{{provider.base_url}}")
}

// 短模型 id 触发 RestorePartial 让人复核——这正是 MinVarLen 想要的行为。
func TestRestoreShortModelIDIsPartial(t *testing.T) {
	v := render.Values{Provider: map[string]string{"model": "k2"}}
	res := render.Restore([]byte(`{"env":{"ANTHROPIC_MODEL":"k2"}}`), v)
	require.True(t, res.Partial)
	require.Contains(t, string(res.Content), `"k2"`, "太短，放弃还原但不阻断")
}

// 四槽同值：按值去重，只留一个 token；计数对不上因此标记 Partial。
func TestRestoreDedupesIdenticalModelSlots(t *testing.T) {
	v := render.Values{Provider: map[string]string{
		"model": "glm-5.1", "model_opus": "glm-5.1",
		"model_sonnet": "glm-5.1", "model_haiku": "glm-5.1",
	}}
	disk := `{"env":{"ANTHROPIC_MODEL":"glm-5.1","ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-5.1"}}`
	res := render.Restore([]byte(disk), v)
	require.True(t, res.Safe)
	require.Equal(t, 2, strings.Count(string(res.Content), "{{provider.model}}"))
	require.NotContains(t, string(res.Content), "{{provider.model_opus}}")
	require.True(t, res.Partial, "出现次数与基线对不上，必须让人复核")
}
```

在 `agent/internal/render/roundtrip_test.go` 追加：

```go
// 往返律扩展到含 provider 值的情形（spec §11）。
func TestRoundTripWithProviderValues(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 17))

	provider := map[string]string{
		"base_url":     "https://open.bigmodel.cn/api/anthropic",
		"auth_token":   "sk-zhipu-abcdefghijklmn",
		"model":        "glm-5.1",
		"model_opus":   "glm-4.7",
		"model_sonnet": "glm-4.6-air",
		"model_haiku":  "glm-4.5-flash",
	}
	vals := render.Values{
		Creds:    map[string]string{"anthropic": "sk-ant-abcdefghij"},
		Vars:     map[string]string{"ws": "production"},
		Provider: provider,
	}
	sec := &secrets.File{
		Creds: vals.Creds, Vars: vals.Vars,
		Machine: map[string]string{}, Provider: provider,
	}

	alphabet := []string{"普通文字", "\n", "{", "}", "{{", "}}", " ", `"`, ":", ","}
	for _, v := range provider {
		alphabet = append(alphabet, v)
	}
	alphabet = append(alphabet, "sk-ant-abcdefghij", "production")

	for range 500 {
		var sb strings.Builder
		for range rng.IntN(24) {
			sb.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		disk := sb.String()

		res := render.Restore([]byte(disk), vals)
		if !res.Safe {
			continue // 判定为不安全的内容不会被上报，往返律对它不适用
		}
		back, err := render.Render(res.Content, sec.Lookup)
		require.NoError(t, err, "输入 %q 还原后无法重新渲染", disk)
		require.Equal(t, disk, string(back), "往返不一致：%q", disk)
	}
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./agent/internal/render/ -v
```

Expected: 编译失败（`render.Values` 未定义、`Restore` 参数个数不符）。

- [ ] **Step 3: 实现**

改写 `agent/internal/render/restore.go` 的签名与桶构造：

```go
// Values 是还原时可用的全部值。
//
// provider 的六个值在这里被分派进已有的两档，不新增第三档（spec §4.2）：
// auth_token 是秘密，进凭据档（必须还原，失败即 Safe=false）；
// base_url 与四个模型槽不是秘密，进变量档（尽力而为，受 MinVarLen 约束）。
type Values struct {
	Creds    map[string]string
	Vars     map[string]string
	Provider map[string]string
}

// authTokenKey 是 provider 里唯一走凭据档的键。
const authTokenKey = "auth_token"

// Restore 把磁盘上的真实值替回占位符。
func Restore(content []byte, v Values) RestoreResult {
	return RestoreWithBase(content, nil, v)
}

func RestoreWithBase(content, base []byte, v Values) RestoreResult {
	out := protocol.EscapeLiteral(string(content))
	res := RestoreResult{Safe: true}

	for _, r := range v.secretBucket() {
		if r.value == "" || !strings.Contains(out, r.value) {
			continue
		}
		out = strings.ReplaceAll(out, r.value, r.token)
	}

	for _, r := range v.varBucket() {
		if r.value == "" || len(r.value) < MinVarLen {
			if r.value != "" && strings.Contains(out, r.value) {
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

	for _, val := range v.secretValues() {
		if val != "" && strings.Contains(out, val) {
			res.Safe = false
			break
		}
	}

	res.Content = []byte(out)

	if res.Safe {
		back, err := Render(res.Content, restoreLookup(v))
		if err != nil || !bytes.Equal(back, content) {
			res.Safe = false
		}
	}
	return res
}

// secretBucket 是必须还原的值：全部凭据，加上 provider.auth_token。
//
// auth_token 顶着 provider. 前缀，但它是秘密，不能因为前缀就当成普通值
// （spec §3.2）。
func (v Values) secretBucket() []replacement {
	out := replacements(v.Creds, protocol.RefCred)
	if tok := v.Provider[authTokenKey]; tok != "" {
		out = append(out, replacement{
			value: tok,
			token: tokenOf(protocol.RefProvider, authTokenKey),
		})
	}
	return dedupeByValue(sortByValueLenDesc(out))
}

// varBucket 是尽力而为的值：全部变量，加上 provider 除 auth_token 外的五个。
func (v Values) varBucket() []replacement {
	rest := make(map[string]string, len(v.Provider))
	for k, val := range v.Provider {
		if k != authTokenKey {
			rest[k] = val
		}
	}
	out := append(replacements(v.Vars, protocol.RefVar),
		replacements(rest, protocol.RefProvider)...)
	return dedupeByValue(sortByValueLenDesc(out))
}

// secretValues 是「还原后不允许再被搜到」的值集合（spec §6.4 的最后一道闸）。
func (v Values) secretValues() []string {
	out := make([]string, 0, len(v.Creds)+1)
	for _, val := range v.Creds {
		out = append(out, val)
	}
	if tok := v.Provider[authTokenKey]; tok != "" {
		out = append(out, tok)
	}
	return out
}

func restoreLookup(v Values) func(protocol.Ref) (string, bool) {
	return func(r protocol.Ref) (string, bool) {
		switch r.Kind {
		case protocol.RefCred:
			s, ok := v.Creds[r.Name]
			return s, ok
		case protocol.RefVar:
			s, ok := v.Vars[r.Name]
			return s, ok
		case protocol.RefProvider:
			s, ok := v.Provider[r.Name]
			return s, ok
		default:
			return "", false
		}
	}
}
```

把 `replacements` 拆成构造 + 排序，并加去重：

```go
func tokenOf(kind protocol.RefKind, name string) string {
	return "{{" + protocol.Ref{Kind: kind, Name: name}.String() + "}}"
}

func replacements(m map[string]string, kind protocol.RefKind) []replacement {
	out := make([]replacement, 0, len(m))
	for name, v := range m {
		out = append(out, replacement{value: v, token: tokenOf(kind, name)})
	}
	return out
}

// sortByValueLenDesc 按值长度降序。
//
// 短值是长值的子串时，先替短的会把长值切碎，剩下的碎片再也匹配不上，
// 于是密钥的一部分留在了内容里。等长时按值、再按 token 排——四个模型槽
// 常常是同一个值，只按值排会让它们的相对次序不确定，渲染就不再是纯函数。
func sortByValueLenDesc(out []replacement) []replacement {
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].value) != len(out[j].value) {
			return len(out[i].value) > len(out[j].value)
		}
		if out[i].value != out[j].value {
			return out[i].value < out[j].value
		}
		return out[i].token < out[j].token
	})
	return out
}

// dedupeByValue 同值只留排序后的第一条。
//
// 四个模型槽常常填同一个值（spec §2.3）：此时「把值替成 token」是有歧义的，
// 替完第一个之后后面三个已经找不到东西可替。留一条、让既有的「出现次数与
// 基线对不上就标 Partial」逻辑去提醒人复核，比在这里发明第二套定位规则安全。
func dedupeByValue(in []replacement) []replacement {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, r := range in {
		if r.value != "" && seen[r.value] {
			continue
		}
		seen[r.value] = true
		out = append(out, r)
	}
	return out
}
```

- [ ] **Step 4: 改掉全部既有调用点，运行测试确认通过**

`agent/internal/render` 包内的 `restore_test.go`、`roundtrip_test.go`、`security_test.go` 里所有 `render.Restore(x, creds, vars)` 改成 `render.Restore(x, render.Values{Creds: creds, Vars: vars})`，`RestoreWithBase` 同理。

```bash
go test ./agent/internal/render/ -v
```

Expected: 全部 PASS，含新增的 `TestRoundTripWithProviderValues`。

- [ ] **Step 5: 提交**

```bash
git add agent/internal/render/ && git commit -m "feat(agent): 还原把 provider 六个值分派进凭据档与变量档"
```

---

### Task 5: agent 接线 —— 快照落盘与对账取值

**Files:**
- Modify: `agent/internal/syncer/syncer.go`（`saveSecrets`）
- Modify: `agent/internal/watcher/scan.go`（`mapsOf` → `valuesOf`，`RestoreWithBase` 调用点）
- Test: `agent/internal/syncer/syncer_test.go`（追加）
- Test: `agent/internal/watcher/scan_test.go`（追加）

**Interfaces:**
- Consumes: `secrets.File.Provider`、`render.Values`、`protocol.ConfigSnapshot.Provider`
- Produces: 无新导出符号；`render.Render` 与 `applier.BuildPlan` 的签名**不变**——lookup 已经是通用的（spec §4.1）

- [ ] **Step 1: 写失败的测试**

追加到 `agent/internal/watcher/scan_test.go`：

```go
// 对账把 provider 值一并喂进还原：否则磁盘上的真实 base_url 会原样上报，
// 而基线里是占位符，每次扫描都报一条假漂移。
func TestScanRestoresProviderValues(t *testing.T) {
	// 用该文件既有的建 watcher / 写基线的方式起一个 rig，基线内容为：
	base := `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}",` +
		`"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}","X":"1"}}`
	// 磁盘内容是渲染后的真实值，外加一处用户手改：
	disk := `{"env":{"ANTHROPIC_BASE_URL":"https://open.bigmodel.cn/api/anthropic",` +
		`"ANTHROPIC_AUTH_TOKEN":"sk-zhipu-abcdefghij","X":"2"}}`
	sec := &secrets.File{
		Creds: map[string]string{}, Vars: map[string]string{}, Machine: map[string]string{},
		Provider: map[string]string{
			"base_url":   "https://open.bigmodel.cn/api/anthropic",
			"auth_token": "sk-zhipu-abcdefghij",
		},
	}
	// 期望：上报内容里两处都是占位符，且不含 sk-zhipu。
	_ = base
	_ = disk
	_ = sec
}
```

> 这段用例的 rig（建临时 managed home、写 `state.json` 基线、`watcher.New` + `Reload` + `Scan`）照抄 `scan_test.go` 里既有的同类用例，只替换内容与断言。断言三条：
> 1. `items` 长度为 1，`Path` 是那个 settings 文件；
> 2. `string(it.Content)` 同时含 `{{provider.base_url}}` 与 `{{provider.auth_token}}`；
> 3. `require.NotContains(t, string(it.Content), "sk-zhipu")` 且 `it.Truncated == false`。

追加到 `agent/internal/syncer/syncer_test.go`：

```go
// 快照带来的 provider 值必须落进 secrets.json，否则渲染取不到值、
// 整份 apply 失败（M1 spec §6.1）。
func TestSnapshotProviderLandsInSecrets(t *testing.T) {
	// 照抄本文件既有用例构造 syncer 与快照，只改两处：
	//   1. blob 内容 = `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}",
	//                          "ANTHROPIC_MODEL":"{{provider.model}}"}}`
	//   2. snap.Provider = map[string]string{
	//          "base_url": "https://open.bigmodel.cn/api/anthropic",
	//          "model":    "glm-5.1",
	//      }
}
```

这条用例的四条断言：

1. `ack.OK` 为真；
2. `secrets.Load(dir)` 之后 `.Provider` 与 `snap.Provider` 逐键相等；
3. 落盘的 `.claude/settings.json` 含 `https://open.bigmodel.cn/api/anthropic` 与 `glm-5.1`；
4. 落盘内容**不含** `{{provider.`——渲染绝不能把字面占位符落给 Claude Code。

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./agent/internal/watcher/ ./agent/internal/syncer/ -run 'Provider' -v
```

Expected: FAIL——上报内容里 base_url 还是字面值 / `secrets.json` 里没有 provider。

- [ ] **Step 3: 实现**

`agent/internal/syncer/syncer.go` 的 `saveSecrets`：

```go
	f := &secrets.File{
		Creds:    snap.Credentials,
		Vars:     snap.Variables,
		Provider: snap.Provider,
		Machine: map[string]string{
			"name":     name,
			"hostname": hostname,
			"os":       runtime.GOOS,
			"arch":     runtime.GOARCH,
		},
	}
	if f.Creds == nil {
		f.Creds = map[string]string{}
	}
	if f.Vars == nil {
		f.Vars = map[string]string{}
	}
	if f.Provider == nil {
		f.Provider = map[string]string{}
	}
```

`agent/internal/watcher/scan.go`：把 `mapsOf` 换成 `valuesOf`：

```go
// valuesOf 把凭据缓存整理成还原用的值集合。
// provider 的分档由 render.Values 内部完成（spec §4.2），这里不做判断。
func valuesOf(sec *secrets.File) render.Values {
	if sec == nil {
		return render.Values{}
	}
	return render.Values{Creds: sec.Creds, Vars: sec.Vars, Provider: sec.Provider}
}
```

调用点：

```go
		res := render.RestoreWithBase(content, base, valuesOf(sec))
```

`cloneSecrets` 追加 `Provider: copyMap(sec.Provider)`。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./agent/... ./protocol/ -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 全量回归并提交**

```bash
go test -tags=testing ./...
```

Expected: 全绿（hub 侧还没动，不该受影响）。

```bash
git add agent/ && git commit -m "feat(agent): 快照的 provider 值落盘并参与对账还原"
```

---

## 本子计划完成后的状态

- `{{provider.*}}` 在两侧都是合法词法；hub 还没有任何东西会产出它。
- agent 收到带 `Provider` 的快照能正确渲染、落盘、对账、还原。
- 旧 agent 收到新快照仍然只是静默忽略该字段（wire 兼容已测）；功能门槛留给子计划 04。
- **没有**任何数据库改动，`go test -tags=testing ./...` 应当全绿。
