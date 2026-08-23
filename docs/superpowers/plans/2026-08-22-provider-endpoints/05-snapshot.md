# 子计划 05 · configsync 快照九键

**前置**：03（`providers` 双端点）、04（`Refs` 去掉 `Creds`）
**读这份之前先读** [00-overview.md](00-overview.md) 的 Global Constraints 与全局接口契约。

**交付物**：`protocol.ConfigSnapshot.Credentials` 字段删除（CBOR 键 `6` 退休）；
`configsync.providerValues` 的取值表从六项变九项；`ErrEndpointMissing` 纵深防御；
`Deps.Creds` 删除、`NotifyCredential` 删除。

**裁剪口径不变**（spec §3.4）：只发 `refs.provider_keys` 里出现过的键，
少一个键少一处泄露面，两个端点的 key 尤其。

**`openai.model` 取的是 provider 的 `default_model`，不是 binding**——
binding 本期恒指 claude 端点（spec §1.3）。这是一处**刻意的不对称**，
接 Codex 时会连同「配置集怎么绑 openai 端点」一起重新设计。实现时不要
「顺手」给 binding 加个 openai 槽。

---

### Task 1: `ConfigSnapshot` 去掉 `Credentials`

**Files:**
- Modify: `protocol/messages_m1.go:73-90`
- Test: `protocol/messages_m1_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `ConfigSnapshot` 不再有 `Credentials`；CBOR 键 `6` 退休

- [ ] **Step 1: 写失败的测试**

追加到 `protocol/messages_m1_test.go`：

```go
// 键 6 退休：ConfigSnapshot 不再有 Credentials 字段，
// 而这个编号也不许被任何新字段占用（M1.6 spec §3.5 + 计划 Global Constraints）。
func TestConfigSnapshotHasNoCredentialsField(t *testing.T) {
	typ := reflect.TypeOf(protocol.ConfigSnapshot{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		require.NotEqual(t, "Credentials", f.Name)
		require.NotContains(t, f.Tag.Get("cbor"), "6,keyasint",
			"CBOR 键 6 已退休，字段 %s 不许占用它", f.Name)
	}
}

// 老 agent 发来的、带键 6 的快照要能被静默忽略（wire 层向后兼容）。
func TestConfigSnapshotDecodeIgnoresRetiredKey(t *testing.T) {
	// 手工编一份带键 6 的 map，模拟老版本写下的字节。
	raw := map[int]any{
		0: "set1", 1: "rev1", 2: 1,
		3: []byte("{}"), 5: "checksum",
		6:  map[string]string{"zhipu": "sk-old"},
		10: map[string]string{"claude.base_url": "https://x.example"},
	}
	b, err := cbor.Marshal(raw)
	require.NoError(t, err)

	var snap protocol.ConfigSnapshot
	require.NoError(t, cbor.Unmarshal(b, &snap))
	require.Equal(t, "set1", snap.ConfigSetID)
	require.Equal(t, "https://x.example", snap.Provider["claude.base_url"])
}
```

同时把 `messages_m1_test.go`（以及 `protocol` 其余测试）里所有给
`Credentials:` 赋值的地方删掉。

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./protocol/ -run ConfigSnapshot -v
```

Expected: FAIL —— `Credentials` 字段还在。

- [ ] **Step 3: 实现**

Modify `protocol/messages_m1.go`：

```go
type ConfigSnapshot struct {
	ConfigSetID string      `cbor:"0,keyasint"`
	RevisionID  string      `cbor:"1,keyasint"`
	Seq         uint32      `cbor:"2,keyasint"`
	Manifest    []byte      `cbor:"3,keyasint"` // 原样 JSON，与发布时冻结的一致
	Files       []FileEntry `cbor:"4,keyasint"`
	Checksum    string      `cbor:"5,keyasint"`
	// 键 6 是原 Credentials，随 {{cred.*}} 一起废止（M1.6 spec §3.5）。
	// **退休不复用**：老 agent 写下的字节里键 6 是一张凭据表，
	// 让新语义顶着旧编号是最难查的一类 bug。
	Variables   map[string]string `cbor:"7,keyasint,omitempty"`
	IgnorePaths []string          `cbor:"8,keyasint,omitempty"`
	Mode        uint8             `cbor:"9,keyasint,omitempty"`
	// Provider 是服务绑定注入的九个端点限定名 → 真实值（M1.6 spec §3.1）。
	// 只含本 Revision 实际引用到的键（refs.provider_keys 裁剪，spec §3.4）——
	// 少一个键少一处泄露面，两个端点的 key 尤其。
	Provider map[string]string `cbor:"10,keyasint,omitempty"`
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./protocol/ -v
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add protocol/
git commit -m "feat(protocol): 快照去掉凭据表，CBOR 键 6 退休"
```

---

### Task 2: `providerValues` 的九键取值表与 `ErrEndpointMissing`

**Files:**
- Modify: `hub/internal/configsync/provider.go`
- Test: `hub/internal/configsync/provider_test.go`

**Interfaces:**
- Consumes: `providers.ClaudeOf` / `OpenAIOf` / `Store.Key`、`protocol.EndpointOf`
- Produces: `configsync.ErrEndpointMissing`；`isBindingFault` 认它

- [ ] **Step 1: 写失败的测试**

改写 `hub/internal/configsync/provider_test.go` 里既有的六键断言，并追加：

```go
func TestProviderValuesMapsAllNineKeys(t *testing.T) {
	h := newSyncFixture(t)
	provID := h.seedProvider(t, "智谱 GLM",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdef123456",
		"https://open.bigmodel.cn/api/paas/v4", "glm-5.2")

	got := h.providerValues(t, provID, providers.ModelSlots{
		Main: "glm-5.2[1m]", Opus: "glm-5.2[1m]",
		Sonnet: "glm-5.2[1m]", Haiku: "glm-4.7",
	}, protocol.ProviderKeys) // 引用全部九个

	require.Equal(t, map[string]string{
		"claude.base_url":     "https://open.bigmodel.cn/api/anthropic",
		"claude.auth_token":   "sk-zhipu-abcdef123456",
		"claude.model":        "glm-5.2[1m]",
		"claude.model_opus":   "glm-5.2[1m]",
		"claude.model_sonnet": "glm-5.2[1m]",
		"claude.model_haiku":  "glm-4.7",
		"openai.base_url":     "https://open.bigmodel.cn/api/paas/v4",
		"openai.api_key":      "sk-zhipu-abcdef123456",
		"openai.model":        "glm-5.2",
	}, got)
}

// 裁剪：只发 refs.provider_keys 里出现过的键（spec §3.4）。
func TestProviderValuesTrimsToReferencedKeys(t *testing.T) {
	h := newSyncFixture(t)
	provID := h.seedProvider(t, "智谱 GLM",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdef123456", "", "")

	got := h.providerValues(t, provID, providers.ModelSlots{},
		[]string{"claude.base_url"})

	require.Equal(t, map[string]string{
		"claude.base_url": "https://open.bigmodel.cn/api/anthropic",
	}, got)
	require.NotContains(t, got, "claude.auth_token", "没引用就不下发，key 尤其")
}

// openai.model 取的是 provider 的 default_model，不是 binding（spec §3.4）。
func TestOpenAIModelComesFromProviderDefault(t *testing.T) {
	h := newSyncFixture(t)
	provID := h.seedProvider(t, "智谱 GLM",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdef123456",
		"https://open.bigmodel.cn/api/paas/v4", "glm-4.7")

	got := h.providerValues(t, provID, providers.ModelSlots{
		Main: "glm-5.2", Opus: "glm-5.2", Sonnet: "glm-5.2", Haiku: "glm-5.2",
	}, []string{"openai.model"})

	require.Equal(t, "glm-4.7", got["openai.model"],
		"binding 的四槽只管 claude 端点")
}

// 端点级 key 覆盖平台级，快照里两个端点各拿各的。
func TestProviderValuesUsesPerEndpointKeys(t *testing.T) {
	h := newSyncFixture(t)
	provID := h.seedProviderWithTwoKeys(t, "两把 key 的中转",
		"sk-platform-000000", "sk-claude-side-111111")

	got := h.providerValues(t, provID, providers.ModelSlots{},
		[]string{"claude.auth_token", "openai.api_key"})

	require.Equal(t, "sk-claude-side-111111", got["claude.auth_token"])
	require.Equal(t, "sk-platform-000000", got["openai.api_key"])
}

// 纵深防御：本应被发布校验挡住，走到这里说明有路径绕过了它（spec §4.3）。
func TestProviderValuesRejectsUnconfiguredEndpoint(t *testing.T) {
	h := newSyncFixture(t)
	provID := h.seedProvider(t, "只有 claude",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdef123456", "", "")

	_, err := h.providerValuesErr(t, provID, providers.ModelSlots{},
		[]string{"openai.base_url"})
	require.ErrorIs(t, err, configsync.ErrEndpointMissing)
	require.Contains(t, err.Error(), "openai")
}

// 引用了模型槽但绑定里该槽是空的，仍然报 ErrEmptyModelSlot（M1.5 行为不变）。
func TestEmptyModelSlotStillRejected(t *testing.T) {
	h := newSyncFixture(t)
	provID := h.seedProvider(t, "智谱 GLM",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdef123456", "", "")

	_, err := h.providerValuesErr(t, provID, providers.ModelSlots{},
		[]string{"claude.model"})
	require.ErrorIs(t, err, configsync.ErrEmptyModelSlot)
}
```

`newSyncFixture` 按 `provider_test.go` 里既有的构造方式改写：`configsync.Deps`
里 `Providers: providers.NewStore(app, key, ev)`，`Creds` 字段本任务先留着
（Task 4 才删）。`providerValues` / `providerValuesErr` 两个薄封装照既有测试
调用 `Service` 的方式包一层——前者 `require.NoError` 后返回 map，
后者原样把 `(map, error)` 交出来。

两个 seed helper 走 `providers.Store.Create`（这里的 provider 都是合法的，
不需要绕过校验）：

```go
// seedProvider 建一条 provider。openaiURL 为空即不配 openai 端点。
func (h *syncFixture) seedProvider(
	t *testing.T, name, claudeURL, key, openaiURL, openaiModel string,
) string {
	t.Helper()
	in := providers.Input{
		Name: name,
		Key:  &key,
		Claude: providers.EndpointInput{
			BaseURL:   claudeURL,
			AuthField: providers.AuthToken,
			Models:    []string{"glm-5.2[1m]", "glm-5.2", "glm-4.7"},
		},
	}
	if openaiURL != "" {
		in.OpenAI = providers.EndpointInput{
			BaseURL:      openaiURL,
			AuthField:    providers.DefaultOpenAIAuthField,
			Models:       []string{openaiModel},
			DefaultModel: openaiModel,
		}
	}
	r, err := h.provs.Create(in)
	require.NoError(t, err)
	return r.Id
}

// seedProviderWithTwoKeys 建一条平台级与 claude 端点级各有一把 key 的 provider。
func (h *syncFixture) seedProviderWithTwoKeys(
	t *testing.T, name, platformKey, claudeKey string,
) string {
	t.Helper()
	r, err := h.provs.Create(providers.Input{
		Name: name,
		Key:  &platformKey,
		Claude: providers.EndpointInput{
			BaseURL:   "https://relay.example/anthropic",
			AuthField: providers.AuthToken,
			Models:    []string{"relay-max"},
			Key:       &claudeKey,
		},
		OpenAI: providers.EndpointInput{
			BaseURL:      "https://relay.example/v1",
			AuthField:    providers.DefaultOpenAIAuthField,
			Models:       []string{"relay-max"},
			DefaultModel: "relay-max",
		},
	})
	require.NoError(t, err)
	return r.Id
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/configsync/ -v
```

Expected: FAIL —— `ErrEndpointMissing` 未定义；取值表还是六项无前缀的键。

- [ ] **Step 3: 实现**

Modify `hub/internal/configsync/provider.go`：

```go
var (
	// ErrBindingMissing：Revision 引用了 {{provider.*}} 但没有绑定。
	// 这种状态本应被发布校验挡住（M1.5 spec §7），此处是纵深防御。
	ErrBindingMissing = errors.New("configsync: 引用了 {{provider.*}} 但没有服务绑定")

	// ErrEmptyModelSlot：引用了某个模型槽，但绑定里该槽是空的（透传模式）。
	// 发一个空串下去会让 Claude Code 去打一个空模型名，错误现场离原因很远。
	ErrEmptyModelSlot = errors.New("configsync: 引用了模型槽但绑定里该槽为空")

	// ErrEndpointMissing：引用了某个端点，但绑定的 provider 没配它。
	// 与 ErrBindingMissing 同一档的纵深防御（M1.6 spec §4.3）。
	ErrEndpointMissing = errors.New("configsync: 引用了某端点但绑定的服务配置没配它")
)
```

`providerValues` 的取值段：

```go
	prov, err := s.d.Providers.Get(b.Provider)
	if err != nil {
		return nil, err
	}

	// 先把「引用到的端点都配了吗」判掉（spec §4.3 的纵深防御）。
	// 这一步必须在解密之前：没配的端点连 key 都不该去解。
	claude := providers.ClaudeOf(prov)
	openai := providers.OpenAIOf(prov)
	for _, k := range refs.ProviderKeys {
		ep := protocol.EndpointOf(k)
		configured := false
		switch ep {
		case providers.EndpointClaude:
			configured = claude.Configured()
		case providers.EndpointOpenAI:
			configured = openai.Configured()
		default:
			continue // 非内置名，protocol 的词法早就拦过
		}
		if !configured {
			return nil, fmt.Errorf("%w：版本 %s 引用了 {{provider.%s}}，"+
				"但服务配置 %q 没有配置 %s 端点",
				ErrEndpointMissing, head.Id, k, prov.GetString("name"), ep)
		}
	}

	all := map[string]string{
		"claude.base_url":     claude.BaseURL,
		"claude.model":        b.Models.Main,
		"claude.model_opus":   b.Models.Opus,
		"claude.model_sonnet": b.Models.Sonnet,
		"claude.model_haiku":  b.Models.Haiku,
		"openai.base_url":     openai.BaseURL,
		// openai.model 取 provider 的 default_model，不是 binding：
		// binding 本期恒指 claude 端点（spec §1.3 / §3.4）。这是一处刻意的
		// 不对称，接 Codex 时会连同「配置集怎么绑 openai 端点」重新设计。
		"openai.model": openai.DefaultModel,
	}
	// 两把 key 按需解密：没被引用就不解，也就不会有明文进内存。
	for _, pair := range []struct{ key, endpoint string }{
		{"claude.auth_token", providers.EndpointClaude},
		{"openai.api_key", providers.EndpointOpenAI},
	} {
		if !contains(refs.ProviderKeys, pair.key) {
			continue
		}
		v, err := s.d.Providers.Key(prov, pair.endpoint)
		if err != nil {
			return nil, fmt.Errorf("configsync: 取服务配置 %s 的 %s 端点 key: %w",
				b.Provider, pair.endpoint, err)
		}
		all[pair.key] = v
	}

	out := make(map[string]string, len(refs.ProviderKeys))
	for _, k := range refs.ProviderKeys {
		v, ok := all[k]
		if !ok {
			continue // 非内置名，protocol 的词法早就拦过，这里只是稳一手
		}
		if v == "" {
			return nil, fmt.Errorf("%w：{{provider.%s}} 被引用，但绑定里该值是空的",
				ErrEmptyModelSlot, k)
		}
		out[k] = v
	}
	return out, nil
```

加一个包内 helper：

```go
func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
```

`isBindingFault` 加一支：

```go
// isBindingFault 判定一个快照组装失败是否属于「绑定相关、需要归因」的那一类。
//
// ErrEndpointMissing 属于这一类：指派置 failed 并写明原因，
// **不置 degraded**——机器上什么都没被改过（M1.6 spec §4.3）。
func isBindingFault(err error) bool {
	return errors.Is(err, ErrAgentTooOld) ||
		errors.Is(err, ErrBindingMissing) ||
		errors.Is(err, ErrEmptyModelSlot) ||
		errors.Is(err, ErrEndpointMissing)
}
```

函数头的注释更新：

```go
// providerValues 组装快照里的 provider 九个键（M1.6 spec §3.4）。
//
// 裁剪口径不变：只发 refs.provider_keys 里出现过的键。这是最小权限，
// 也是一条实际的防线——少一个键少一处泄露面，两个端点的 key 尤其。
// 判定全程**不读 blob 内容**，这正是 M1 立 refs 字段的初衷。
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/internal/configsync/ -v
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/configsync/provider.go hub/internal/configsync/provider_test.go
git commit -m "feat(hub): 快照九键与端点缺失的纵深防御"
```

---

### Task 3: 端点缺失时指派置 `failed`、不置 `degraded`

**Files:**
- Test: `hub/internal/configsync/service_test.go`（追加）

**Interfaces:**
- Consumes: Task 2 的 `ErrEndpointMissing` / `isBindingFault`
- Produces: 无新接口，锁住既有归因路径覆盖新错误

`failAssignment` 的代码**一行不改**——Task 2 把 `ErrEndpointMissing` 加进
`isBindingFault` 之后它就自动覆盖了。这个任务只是给那条自动覆盖上一道锁：
DoD 第 8 条要求它，而「靠别人顺带做对」的行为最容易在下一次重构里悄悄丢掉。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/configsync/service_test.go`：

```go
func TestPullMarksAssignmentFailedOnEndpointMissing(t *testing.T) {
	h := newSyncFixture(t)
	provID := h.seedProvider(t, "只有 claude",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdef123456", "", "")
	setID, machineID := h.seedBoundSet(t, provID, map[string]string{
		".codex/config.toml": "base_url = \"{{provider.openai.base_url}}\"\n",
	})

	_, err := h.svc.Snapshot(machineID)
	require.ErrorIs(t, err, configsync.ErrEndpointMissing)

	assign, err := h.sets.Assignment(machineID)
	require.NoError(t, err)
	require.Equal(t, configsets.StateFailed, assign.GetString("state"))
	require.NotEqual(t, configsets.StateDegraded, assign.GetString("state"),
		"机器上什么都没被改过，不需要人工解除")
	require.Contains(t, assign.GetString("last_error"), "openai")
	_ = setID
}
```

> `h.svc.Snapshot` / `h.seedBoundSet` 按 `service_test.go` 里既有的
> 「拉快照 → 断言 assignment」用例照做（M1.5 的
> `TestPullMarksAssignmentFailedOnOldAgent` 就是同一形状）。`seedBoundSet` 要
> 绕过发布校验直接发布——校验会先把 `endpoint_missing` 挡住，而这条测试
> 要验的正是「有路径绕过了它」之后的兜底。用 `revisions.PublishFiles` 直发。

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/configsync/ -run EndpointMissing -v
```

Expected: 若 Task 2 的 `isBindingFault` 漏加了那一支，这里 FAIL
（`state` 停在原值）；加了就直接 PASS——那是好的，说明覆盖成立。
无论哪种情况，这条断言都要留在库里。

- [ ] **Step 3: 实现**

若 Step 2 已经 PASS，本步无事可做，直接进 Step 4。
若 FAIL，回到 Task 2 Step 3 的 `isBindingFault` 补上那一支。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/internal/configsync/ -v
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/configsync/service_test.go
git commit -m "test(hub): 锁住端点缺失的指派归因"
```

---

### Task 4: 删掉 `Deps.Creds` 与 `NotifyCredential`

**Files:**
- Modify: `hub/internal/configsync/service.go:17,44,113-118,152`
- Modify: `hub/internal/configsync/notify.go:78-107`
- Modify: `hub/hub.go`（`configsync.Deps` 里去掉 `Creds`）
- Modify: `hub/api.go:24-30`（`RotateCredential` 里那次 `NotifyCredential`）
- Test: `hub/internal/configsync/service_test.go`

**Interfaces:**
- Consumes: 01 建的 `variables.Store`
- Produces: `configsync.Deps` 不再有 `Creds`；`NotifyCredential` 删除

轮换 key 现在走 `UpdateProvider` → `NotifyProvider`，**不需要**第二条通知路径：
key 就在 provider 上，改它就是改 provider。`NotifyProvider` 与
`config_sets.head_provider` 索引一个字都不改。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/configsync/service_test.go`：

```go
// 轮换 provider 的 key → 重注入全机队、不产生新 Revision。
// 这是 M1.5「改 Provider 不产生新 Revision」不变量在 key 内联之后的延续。
func TestRotatingProviderKeyReinjectsWithoutNewRevision(t *testing.T) {
	h := newSyncFixture(t)
	provID := h.seedProvider(t, "智谱 GLM",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-old-000000", "", "")
	setID, machineID := h.seedBoundSet(t, provID, map[string]string{
		configsets.SettingsPath: `{"env":{
			"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}",
			"ANTHROPIC_AUTH_TOKEN":"{{provider.claude.auth_token}}"
		}}`,
	})

	before := h.revisionCount(t, setID)
	h.rotateKey(t, provID, "sk-zhipu-new-999999")
	require.Equal(t, before, h.revisionCount(t, setID),
		"轮换 key 绝不产生新 Revision")

	snap, err := h.svc.Snapshot(machineID)
	require.NoError(t, err)
	require.Equal(t, "sk-zhipu-new-999999", snap.Provider["claude.auth_token"])

	// 收到的是不带 RevisionID 的 ConfigNotify（= 仅 secrets 变更）。
	n := h.lastNotify(t, machineID)
	require.Empty(t, n.RevisionID)
	require.Equal(t, protocol.ReasonRotated, n.Reason)
}
```

> `h.rotateKey` 调 `providers.Store.Update` 只改 `Key`（其余字段用
> `ProviderInputOf` 那种「读回来原样带上」的方式），再调
> `svc.NotifyProvider(provID)`。`h.lastNotify` / `h.revisionCount` 按
> `service_test.go` 现有的 fake Sender 与 revision 查询照做。

同时**删掉**既有的 `TestNotifyCredential*` 整组用例——它们测的是被废止的路径。

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/configsync/ -run RotatingProviderKey -v
```

Expected: FAIL（helper 未定义 / 断言不成立）。

- [ ] **Step 3: 实现**

`hub/internal/configsync/service.go`：

- `Deps` 去掉 `Creds *credentials.Store`，去掉那行 import
- 组装快照时删掉这一段：

```go
	creds, err := s.d.Creds.Values(refs.Creds)
	if err != nil {
		return snap, err
	}
```

- `protocol.ConfigSnapshot{...}` 里去掉 `Credentials: creds,`
- 注释「只发这个 Revision 实际引用到的凭据」那一段整体删掉——它描述的
  东西没有了；裁剪的说明留在 `providerValues` 里

`hub/internal/configsync/notify.go`：`NotifyCredential` 整个方法删除。

`hub/hub.go`：`configsync.NewService(configsync.Deps{...})` 里去掉 `Creds: h.creds,`。

`hub/api.go`：`RotateCredential` 里的 `return h.sync.NotifyCredential(name)`
暂时改成 `return nil`——整个方法在 08 才删掉，这里只是让它编译过。加一行注释
指向 08：

```go
// RotateCredential 轮换凭据。
//
// **已废弃**：凭据实体在 M1.6 里被废止，key 内联到 provider 上，
// 轮换走 UpdateProvider → NotifyProvider。本方法与它的路由在子计划 08 删除。
func (h *Hub) RotateCredential(name, value string) error {
	return h.creds.Rotate(name, value)
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/internal/configsync/ ./hub/ -v
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/configsync hub/hub.go hub/api.go
git commit -m "refactor(hub): 快照与通知去掉凭据这一路"
```
