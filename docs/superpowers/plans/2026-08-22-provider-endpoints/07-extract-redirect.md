# 子计划 07 · 导入向导与漂移的抽取重定向

**前置**：03（`providers` 双端点）、04（词法跟进）
**读这份之前先读** [00-overview.md](00-overview.md) 的 Global Constraints 与全局接口契约。

**交付物**：`importer.Service.Extract` 与 `drift.Service.ExtractKey` 合并成同一条
「抽成 provider 的 key」路径；`importer.Service.MatchProviderIn` 反查预选；
`drift.Rebind` / `CreateProviderFromDrift` 跟进双端点；对应的 HTTP 端点。

**顺序上它排在 08 之前**（spec §8 把它放第 10 步）：`importer` 与 `drift` 是
`credentials.Store` 的最后两个使用者，不先把它们改掉，08 删不掉那个包。

**第 4 步的「每一处」是要紧的**（spec §5.5）：同一个 key 常常同时出现在 env 与
某段说明文字里，漏一处就等于没脱敏，而 Revision 不可变，写进去就洗不掉。

**非 AI 类的 key 没有抽取去处了**——这是废止 `{{cred.*}}` 的直接代价（spec §9）。
扫描仍然报警告，但用户只能手动处理（改成机器变量、或接受明文）。
UI 文案要说清楚，不要让「抽取」按钮对着一个没有去处的发现亮着。

---

### Task 1: `importer.Service.Extract` 重定向

**Files:**
- Modify: `hub/internal/importer/service.go:14,31,44-51,158-203`
- Test: `hub/internal/importer/service_test.go`

**Interfaces:**
- Consumes: `providers.Store`（`Get` / `Update` / `Key`）、`secretbox.MinValueLen`
- Produces: `importer.NewService(app, blobs, sets, provs, ev, sender)`（`creds` 换成 `provs`）；
  `(s *Service) Extract(setID, path, location, providerID, endpoint string) error`

- [ ] **Step 1: 写失败的测试**

改写 `hub/internal/importer/service_test.go` 里 `Extract` 的用例：

```go
func TestExtractWritesKeyIntoProviderEndpoint(t *testing.T) {
	f := newImporterFixture(t)
	provID := f.seedProvider(t, "智谱 GLM") // 只配 claude，平台级 key 为空
	setID := f.seedDraft(t, map[string]string{
		".claude/settings.json": `{"env":{
			"ANTHROPIC_BASE_URL":"https://open.bigmodel.cn/api/anthropic",
			"ANTHROPIC_AUTH_TOKEN":"sk-zhipu-abcdef123456"
		}}`,
	})

	require.NoError(t, f.svc.Extract(setID, ".claude/settings.json",
		"env.ANTHROPIC_AUTH_TOKEN", provID, providers.EndpointClaude))

	// key 进了 provider 的 claude 端点
	prov, err := f.provs.Get(provID)
	require.NoError(t, err)
	got, err := f.provs.Key(prov, providers.EndpointClaude)
	require.NoError(t, err)
	require.Equal(t, "sk-zhipu-abcdef123456", got)
	require.Equal(t, "3456", providers.ClaudeOf(prov).KeyLast4)

	// 草稿里换成了端点限定占位符
	content := f.draftContent(t, setID, ".claude/settings.json")
	require.Contains(t, string(content), "{{provider.claude.auth_token}}")
	require.NotContains(t, string(content), "sk-zhipu-abcdef123456")
}

// openai 侧写的是另一个 key 名（spec §5.5 第 4 步）。
func TestExtractIntoOpenAIEndpointUsesApiKeyToken(t *testing.T) {
	f := newImporterFixture(t)
	provID := f.seedProvider(t, "智谱 GLM")
	setID := f.seedDraft(t, map[string]string{
		".codex/auth.json": `{"OPENAI_API_KEY":"sk-openai-zyxwvu654321"}`,
	})

	require.NoError(t, f.svc.Extract(setID, ".codex/auth.json",
		"OPENAI_API_KEY", provID, providers.EndpointOpenAI))

	content := f.draftContent(t, setID, ".codex/auth.json")
	require.Contains(t, string(content), "{{provider.openai.api_key}}")
}

// 「每一处」：同一个值出现在 env 与说明文字里，两处都要替（spec §5.5）。
func TestExtractReplacesEveryOccurrence(t *testing.T) {
	f := newImporterFixture(t)
	provID := f.seedProvider(t, "智谱 GLM")
	setID := f.seedDraft(t, map[string]string{
		".claude/settings.json": `{
			"env":{"ANTHROPIC_AUTH_TOKEN":"sk-zhipu-abcdef123456"},
			"note":"备用：sk-zhipu-abcdef123456"
		}`,
	})

	require.NoError(t, f.svc.Extract(setID, ".claude/settings.json",
		"env.ANTHROPIC_AUTH_TOKEN", provID, providers.EndpointClaude))

	content := f.draftContent(t, setID, ".claude/settings.json")
	require.NotContains(t, string(content), "sk-zhipu-abcdef123456",
		"漏一处就等于没脱敏，而 Revision 写进去就洗不掉")
	require.Equal(t, 2, strings.Count(string(content), "{{provider.claude.auth_token}}"))
}

// 长度下限不变：太短的值抽出来会在还原时到处误匹配。
func TestExtractRejectsShortValue(t *testing.T) {
	f := newImporterFixture(t)
	provID := f.seedProvider(t, "智谱 GLM")
	setID := f.seedDraft(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_AUTH_TOKEN":"short"}}`,
	})

	err := f.svc.Extract(setID, ".claude/settings.json",
		"env.ANTHROPIC_AUTH_TOKEN", provID, providers.EndpointClaude)
	require.Error(t, err)
	require.Contains(t, err.Error(), "短于")
}

func TestExtractRejectsUnknownProvider(t *testing.T) {
	f := newImporterFixture(t)
	setID := f.seedDraft(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-zhipu-abcdef123456"}}`,
	})
	err := f.svc.Extract(setID, ".claude/settings.json",
		"env.ANTHROPIC_AUTH_TOKEN", "nonexistent", providers.EndpointClaude)
	require.ErrorIs(t, err, providers.ErrNotFound)
}

// 反查预选：文件里的 base_url 命中已有 provider 就把它报出来（spec §5.5 第 2 步）。
func TestMatchProviderInFindsProviderByBaseURL(t *testing.T) {
	f := newImporterFixture(t)
	provID := f.seedProvider(t, "智谱 GLM")
	setID := f.seedDraft(t, map[string]string{
		".claude/settings.json": `{"env":{
			"ANTHROPIC_BASE_URL":"https://open.bigmodel.cn/api/anthropic",
			"ANTHROPIC_AUTH_TOKEN":"sk-zhipu-abcdef123456"
		}}`,
	})

	m, err := f.svc.MatchProviderIn(setID, ".claude/settings.json")
	require.NoError(t, err)
	require.Equal(t, providers.MatchProvider, m.Kind)
	require.True(t, m.Exact)
	require.Equal(t, provID, m.ProviderID)
}

func TestMatchProviderInReturnsNoneWithoutBaseURL(t *testing.T) {
	f := newImporterFixture(t)
	setID := f.seedDraft(t, map[string]string{
		".claude/CLAUDE.md": "没有 base_url\n",
	})
	m, err := f.svc.MatchProviderIn(setID, ".claude/CLAUDE.md")
	require.NoError(t, err)
	require.Equal(t, providers.MatchNone, m.Kind)
}
```

`newImporterFixture` 按 `service_test.go` 现有的构造方式改写：把
`credentials.NewStore` 换成 `providers.NewStore(app, key, ev)`，并暴露
`svc` / `provs` / `seedProvider` / `seedDraft` / `draftContent` 五个成员。

`seedProvider` 建一条**平台级 key 为空、只配 claude 端点**的 provider——
这正是「刚认出平台、还没填 key」的状态，也是 Extract 的典型入口。
`providers.Store.Create` 会拒绝「配了 base_url 却没 key」，所以它直接写记录：

```go
// seedProvider 建一条只配了 claude 端点、还没有任何 key 的 provider。
// 直接写记录：Store.Create 会拒绝这种状态，而它正是抽取向导要面对的入口。
func (f *importerFixture) seedProvider(t *testing.T, name string) string {
	t.Helper()
	c, err := f.app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("name", name)
	r.Set("claude", providers.ClaudeEndpoint{
		Endpoint: providers.Endpoint{
			BaseURL:   "https://open.bigmodel.cn/api/anthropic",
			AuthField: providers.AuthToken,
			Models:    []string{"glm-5.2"},
		},
	})
	r.Set("openai", providers.OpenAIEndpoint{
		Endpoint: providers.Endpoint{
			AuthField: providers.DefaultOpenAIAuthField,
			Models:    []string{},
		},
	})
	require.NoError(t, f.app.Save(r))
	return r.Id
}
```

`seedDraft` 与 `draftContent` 沿用 `service_test.go` 里既有的写法
（建配置集 → 逐个 `SetDraftFile` → 读回 blob）。

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/importer/ -v
```

Expected: FAIL —— `Extract` 参数个数不对、`MatchProviderIn` 未定义。

- [ ] **Step 3: 实现**

Modify `hub/internal/importer/service.go`：

依赖从 `creds` 换成 `provs`：

```go
type Service struct {
	app    core.App
	blobs  *blobs.Store
	sets   *configsets.Service
	provs  *providers.Store
	ev     *events.Writer
	sender Sender

	pending map[string]session
}

func NewService(app core.App, b *blobs.Store, sets *configsets.Service,
	provs *providers.Store, ev *events.Writer, sender Sender) *Service {
	return &Service{
		app: app, blobs: b, sets: sets, provs: provs, ev: ev, sender: sender,
		pending: map[string]session{},
	}
}
```

`Extract` 改写：

```go
// Extract 把某处的值抽成 provider 某个端点的 key：写进 provider →
// 把该文件里**每一处**该值替成该端点的 key 占位符 → 重写草稿。
//
// 「每一处」是要紧的（M1 spec §1.4）：同一个 key 常常同时出现在 env 与
// 某段说明文字里，漏一处就等于没脱敏，而 Revision 是不可变的，
// 写进去就洗不掉。
//
// 与 M1 的区别只在去处：值从「建一条凭据」改成「写进 provider 的端点 key」
// （M1.6 spec §5.5）。凭据实体提供的加密落库、末四位回显 provider 自己完全
// 能承担；而「起个名再回来选它」这一步的心智负担被去掉了。
func (s *Service) Extract(setID, path, location, providerID, endpoint string) error {
	token, err := keyTokenOf(endpoint)
	if err != nil {
		return err
	}
	prov, err := s.provs.Get(providerID)
	if err != nil {
		return err
	}

	draft, err := s.sets.Draft(setID)
	if err != nil {
		return err
	}
	var entry *protocol.FileEntry
	for i := range draft {
		if draft[i].Path == path {
			entry = &draft[i]
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("importer: 草稿里没有 %s", path)
	}
	content, err := s.blobs.Get(entry.Hash)
	if err != nil {
		return err
	}

	value := gjson.GetBytes(content, location).String()
	if value == "" {
		return fmt.Errorf("importer: %s 的 %s 取不到值", path, location)
	}
	if len(value) < secretbox.MinValueLen {
		return fmt.Errorf("importer: %s 的值只有 %d 个字符，短于 %d，"+
			"抽出来之后还原时会到处误匹配；请保留明文或改用机器变量",
			location, len(value), secretbox.MinValueLen)
	}

	if err := s.provs.SetEndpointKey(prov, endpoint, value); err != nil {
		return err
	}

	replaced := strings.ReplaceAll(string(content), value, token)
	if _, err := s.sets.SetDraftFile(setID, path, []byte(replaced), entry.Mode, entry.Keys); err != nil {
		return err
	}
	return nil
}

// keyTokenOf 给出某端点承载 key 的占位符字面形态。
// 两侧的 key 名不同（claude.auth_token vs openai.api_key，spec §3.1），
// 不能拿一个常量套两边。
func keyTokenOf(endpoint string) (string, error) {
	switch endpoint {
	case providers.EndpointClaude:
		return "{{provider.claude.auth_token}}", nil
	case providers.EndpointOpenAI:
		return "{{provider.openai.api_key}}", nil
	default:
		return "", fmt.Errorf("importer: 未知端点 %q", endpoint)
	}
}

// MatchProviderIn 对文件里的 base_url 做一次反查，供抽取向导预选 provider
// （M1.6 spec §5.5 第 2 步）。找不到 base_url 或反查不中都返回 MatchNone，
// 不是错误——那只是「让用户自己选」。
func (s *Service) MatchProviderIn(setID, path string) (providers.Match, error) {
	draft, err := s.sets.Draft(setID)
	if err != nil {
		return providers.Match{}, err
	}
	for _, f := range draft {
		if f.Path != path {
			continue
		}
		content, err := s.blobs.Get(f.Hash)
		if err != nil {
			return providers.Match{}, err
		}
		url := gjson.GetBytes(content, "env.ANTHROPIC_BASE_URL").String()
		if url == "" {
			return providers.Match{Kind: providers.MatchNone}, nil
		}
		return s.provs.MatchBaseURL(url)
	}
	return providers.Match{Kind: providers.MatchNone}, nil
}
```

`providers.Store` 加一个 `SetEndpointKey`（Extract 与 drift 共用同一条写入路径）：

```go
// SetEndpointKey 把一把明文 key 写进某端点，并回填末四位。
//
// 与 Update 走同一套加密与长度校验，但**不碰其余字段**：抽取的语义是
// 「给这个端点补上 key」，不该顺手把用户在 UI 上没动过的 base_url 改掉。
func (s *Store) SetEndpointKey(r *core.Record, endpoint, value string) error {
	if len(value) < secretbox.MinValueLen {
		return fmt.Errorf("%w: 至少 %d 个字符", secretbox.ErrShortValue, secretbox.MinValueLen)
	}
	var field string
	switch endpoint {
	case EndpointClaude:
		field = "claude_key_cipher"
	case EndpointOpenAI:
		field = "openai_key_cipher"
	default:
		return fmt.Errorf("providers: 未知端点 %q", endpoint)
	}
	enc, err := secretbox.Encrypt(s.key, value)
	if err != nil {
		return err
	}
	r.Set(field, enc)
	if err := setEndpointLast4(r, endpoint, secretbox.Last4(value)); err != nil {
		return err
	}
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("providers: 写入 %s 端点的 key: %w", endpoint, err)
	}
	s.write(events.KindProviderUpdated, r)
	return nil
}
```

`hub/hub.go` 里 `importer.NewService(...)` 的第四个实参从 `h.creds` 换成
`h.provs`——注意 `h.provs` 的构造要排在 `h.importer` 之前（03 Task 6 已经
把它挪到主密钥之后了，顺序满足）。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/internal/importer/ ./hub/internal/providers/ -v
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/importer hub/internal/providers hub/hub.go
git commit -m "feat(hub): 导入向导的抽取写进 provider 端点 key"
```

---

### Task 2: `drift.ExtractKey` 并入同一条路径

**Files:**
- Modify: `hub/internal/drift/service.go:29-32`（`Deps.Creds` → `Deps.Providers` 已有，删 `Creds`）
- Modify: `hub/internal/drift/binding.go:120-160`（`ExtractKey`）、`:200-215`（`Rebind` 读 defaults）
- Modify: `hub/providers.go:82-95`（`CreateProviderFromDrift`）
- Test: `hub/internal/drift/binding_test.go`

**Interfaces:**
- Consumes: Task 1 的 `providers.Store.SetEndpointKey`
- Produces: `(s *Service) ExtractKey(eventID, location, providerID, endpoint string) error`；
  `Hub.CreateProviderFromDrift(eventID, location, endpoint string, in providers.Input) (string, error)`

- [ ] **Step 1: 写失败的测试**

改写 `hub/internal/drift/binding_test.go` 里 `ExtractKey` 的用例：

```go
func TestExtractKeyWritesIntoProviderEndpoint(t *testing.T) {
	f := newDriftFixture(t)
	provID := f.seedProvider(t, "新中转", "https://relay.example/anthropic")
	eventID := f.seedBindingDrift(t, `{"env":{
		"ANTHROPIC_BASE_URL":"https://relay.example/anthropic",
		"ANTHROPIC_AUTH_TOKEN":"sk-relay-abcdef123456"
	}}`)

	require.NoError(t, f.svc.ExtractKey(eventID,
		"env.ANTHROPIC_AUTH_TOKEN", provID, providers.EndpointClaude))

	prov, err := f.provs.Get(provID)
	require.NoError(t, err)
	got, err := f.provs.Key(prov, providers.EndpointClaude)
	require.NoError(t, err)
	require.Equal(t, "sk-relay-abcdef123456", got)
}

// 源是漂移 blob 而不是草稿——这把 key 是用户在机器上手写的，中台从没见过它。
func TestExtractKeyReadsFromDriftBlobNotDraft(t *testing.T) {
	f := newDriftFixture(t)
	provID := f.seedProvider(t, "新中转", "https://relay.example/anthropic")
	eventID := f.seedBindingDrift(t,
		`{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-only-in-drift-9988"}}`)

	require.NoError(t, f.svc.ExtractKey(eventID,
		"env.ANTHROPIC_AUTH_TOKEN", provID, providers.EndpointClaude))

	prov, err := f.provs.Get(provID)
	require.NoError(t, err)
	got, err := f.provs.Key(prov, providers.EndpointClaude)
	require.NoError(t, err)
	require.Equal(t, "sk-only-in-drift-9988", got)
}

// 改绑时模型槽取新 Provider 的 claude 端点 defaults：换了家供应商，
// 旧供应商的模型 id 在新 endpoint 上没有意义（M1.5 spec §6.3 第一档）。
func TestRebindTakesDefaultsFromClaudeEndpoint(t *testing.T) {
	f := newDriftFixture(t)
	provID := f.seedProviderWithDefaults(t, "新中转", "https://relay.example/anthropic",
		providers.ModelSlots{
			Main: "relay-max", Opus: "relay-max",
			Sonnet: "relay-max", Haiku: "relay-mini",
		})
	eventID := f.seedBindingDriftInSet(t, `{"env":{
		"ANTHROPIC_BASE_URL":"https://relay.example/anthropic"
	}}`)

	rev, err := f.svc.Rebind(eventID, provID)
	require.NoError(t, err)

	var b providers.Binding
	require.NoError(t, rev.UnmarshalJSONField("binding", &b))
	require.Equal(t, provID, b.Provider)
	require.Equal(t, "relay-mini", b.Models.Haiku)
}
```

`newDriftFixture`、`seedBindingDrift`、`seedBindingDriftInSet` 三个 helper 按
`binding_test.go` / `rig_test.go` 现有的构造方式改写，只把 `drift.Deps` 里的
`Creds` 去掉、补上 `Providers: providers.NewStore(app, key, ev)`。
两个 `seedProvider*` 直接写记录：

```go
// seedProvider 建一条只配 claude 端点、带平台级密文的 provider。
func (f *driftFixture) seedProvider(t *testing.T, name, baseURL string) string {
	t.Helper()
	return f.seedProviderWithDefaults(t, name, baseURL, providers.ModelSlots{})
}

func (f *driftFixture) seedProviderWithDefaults(
	t *testing.T, name, baseURL string, defaults providers.ModelSlots,
) string {
	t.Helper()
	c, err := f.app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	cipher, err := secretbox.Encrypt(f.key, "sk-seed-abcdef123456")
	require.NoError(t, err)

	r := core.NewRecord(c)
	r.Set("name", name)
	r.Set("key_cipher", cipher)
	r.Set("key_last4", "3456")
	r.Set("claude", providers.ClaudeEndpoint{
		Endpoint: providers.Endpoint{
			BaseURL:   baseURL,
			AuthField: providers.AuthToken,
			KeyLast4:  "3456",
			Models:    []string{"relay-max", "relay-mini"},
		},
		Defaults: defaults,
	})
	r.Set("openai", providers.OpenAIEndpoint{
		Endpoint: providers.Endpoint{
			AuthField: providers.DefaultOpenAIAuthField,
			Models:    []string{},
		},
	})
	require.NoError(t, f.app.Save(r))
	return r.Id
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/drift/ -v
```

Expected: FAIL —— `ExtractKey` 签名不对、`Rebind` 还在读顶层 `defaults`。

- [ ] **Step 3: 实现**

`hub/internal/drift/service.go`：`Deps` 去掉 `Creds *credentials.Store` 与 import。

`hub/internal/drift/binding.go`：

```go
// ExtractKey 把漂移内容里 location 处的值写进某个 provider 的端点 key。
//
// 源是漂移 blob 而不是草稿（与 importer.Extract 的区别就在这里）：
// 这把 key 是用户在**机器上**手写的，中台从没见过它。除了取值的来源，
// 两者是同一条代码路径——都落到 providers.Store.SetEndpointKey
// （M1.6 spec §5.5）。
func (s *Service) ExtractKey(eventID, location, providerID, endpoint string) error {
	rec, err := s.d.App.FindRecordById("drift_events", eventID)
	if err != nil {
		return fmt.Errorf("drift: 漂移 %s 不存在: %w", eventID, err)
	}
	content, err := s.driftContent(rec)
	if err != nil {
		return err
	}
	value := gjson.GetBytes(content, location).String()
	if value == "" {
		return fmt.Errorf("drift: %s 的 %s 取不到值", rec.GetString("path"), location)
	}
	prov, err := s.d.Providers.Get(providerID)
	if err != nil {
		return err
	}
	// 长度下限由 SetEndpointKey 把关（M1 spec §6.4）：
	// 太短的值抽出来会在还原时到处误匹配。
	return s.d.Providers.SetEndpointKey(prov, endpoint, value)
}
```

`Rebind` 里读 defaults 的两行：

```go
	// 模型槽取新 Provider 的 claude 端点 defaults：换了家供应商，
	// 旧供应商的模型 id 在新 endpoint 上没有意义。
	// 绑定本期恒指 claude 端点（M1.6 spec §1.3）。
	binding := &providers.Binding{
		Provider: providerID,
		Models:   providers.ClaudeOf(prov).Defaults,
	}
```

`hub/providers.go` 的 `CreateProviderFromDrift` 改成**一次成型**：先把 key 从
漂移里取出来内联进 `Input`，再建。

不是「先建 provider 再补 key」，因为 `providers.Store` 会校验「配了 base_url
的端点必须能解出一把 key」——先建会被它挡住，而放宽那条校验去迁就一个向导，
是拿数据面的正确性换交互的便利。顺带也少了 M1.5 里「两步之间失败留下一条
孤儿凭据」的中间态。

```go
// CreateProviderFromDrift 把漂移内容里 location 处的值取出来内联进
// providers.Input，然后一次建成（M1.5 spec §6.3 第二档 + M1.6 spec §5.5）。
//
// 一次成型而不是「先建再补」：providers.Store 会校验「配了 base_url 的端点
// 必须能解出一把 key」，先建会被它挡住；而放宽那条校验去迁就一个向导，
// 是拿数据面的正确性换交互的便利。
func (h *Hub) CreateProviderFromDrift(
	eventID, location, endpoint string, in providers.Input,
) (string, error) {
	if location != "" {
		value, err := h.drift.DriftValue(eventID, location)
		if err != nil {
			return "", err
		}
		switch endpoint {
		case providers.EndpointClaude:
			in.Claude.Key = &value
		case providers.EndpointOpenAI:
			in.OpenAI.Key = &value
		default:
			return "", fmt.Errorf("未知端点 %q", endpoint)
		}
	}
	return h.CreateProvider(in)
}
```

于是 `drift` 侧要多导出一个取值方法（`ExtractKey` 复用它）：

```go
// DriftValue 读漂移内容里 location 处的明文值。
// 建 provider 的向导用它把机器上手写的 key 内联进 Input。
func (s *Service) DriftValue(eventID, location string) (string, error) {
	rec, err := s.d.App.FindRecordById("drift_events", eventID)
	if err != nil {
		return "", fmt.Errorf("drift: 漂移 %s 不存在: %w", eventID, err)
	}
	content, err := s.driftContent(rec)
	if err != nil {
		return "", err
	}
	value := gjson.GetBytes(content, location).String()
	if value == "" {
		return "", fmt.Errorf("drift: %s 的 %s 取不到值", rec.GetString("path"), location)
	}
	return value, nil
}
```

`ExtractKey` 的前半段改成调 `DriftValue`：

```go
func (s *Service) ExtractKey(eventID, location, providerID, endpoint string) error {
	value, err := s.DriftValue(eventID, location)
	if err != nil {
		return err
	}
	prov, err := s.d.Providers.Get(providerID)
	if err != nil {
		return err
	}
	return s.d.Providers.SetEndpointKey(prov, endpoint, value)
}
```

`findAuthKey` 保持只认 `env.ANTHROPIC_AUTH_TOKEN` / `env.ANTHROPIC_API_KEY`
两个位置——它服务的是绑定漂移，而漂移源只有 `.claude/**`（spec §1.3）。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/internal/drift/ ./hub/ -v
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/drift hub/providers.go
git commit -m "feat(hub): 漂移的抽取与导入向导合并成同一条路径"
```

---

### Task 3: HTTP 端点跟进

**Files:**
- Modify: `hub/internal/routes/config.go`（`extractCredential` → `extractKey`，加 `providerMatch`）
- Modify: `hub/internal/routes/providers.go`（`providerBody` 双端点、`from_drift` 带 endpoint）
- Modify: `hub/internal/routes/routes.go`（注册 `provider-match`）
- Modify: `hub/api.go:108-111`（`ExtractCredential` → `ExtractKeyToProvider`，加 `MatchProviderIn`）
- Test: `hub/internal/routes/config_test.go`、`hub/internal/routes/providers_test.go`

**Interfaces:**
- Consumes: Task 1 / Task 2
- Produces: 见 00-overview 的「HTTP 端点」表与 `providerBody` 定义

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/routes/providers_test.go`：

```go
func TestCreateProviderWithBothEndpoints(t *testing.T) {
	rig := newRoutesRig(t)
	body := `{
		"name":"智谱 GLM",
		"preset":"zhipu",
		"key":"sk-zhipu-abcdef123456",
		"claude":{
			"base_url":"https://open.bigmodel.cn/api/anthropic",
			"auth_field":"ANTHROPIC_AUTH_TOKEN",
			"models":["glm-5.2"],
			"defaults":{"main":"glm-5.2","opus":"glm-5.2","sonnet":"glm-5.2","haiku":"glm-5.2"}
		},
		"openai":{
			"base_url":"https://open.bigmodel.cn/api/paas/v4",
			"models":["glm-5.2"],
			"default_model":"glm-5.2"
		}
	}`
	res := rig.POST(t, "/api/orciny/providers", body)
	require.Equal(t, 200, res.Code)

	prov := rig.LastProvider(t)
	require.True(t, providers.ClaudeOf(prov).Configured())
	require.True(t, providers.OpenAIOf(prov).Configured())
	require.Equal(t, providers.DefaultOpenAIAuthField,
		providers.OpenAIOf(prov).AuthField, "没给就用默认值")
}

// key 字段缺席 = 不修改；不能被当成「清空」。
func TestUpdateProviderWithoutKeyFieldKeepsKey(t *testing.T) {
	rig := newRoutesRig(t)
	id := rig.SeedProvider(t, "智谱 GLM", "sk-zhipu-abcdef123456")

	res := rig.PUT(t, "/api/orciny/providers/"+id, `{
		"name":"智谱 GLM",
		"claude":{
			"base_url":"https://open.bigmodel.cn/api/anthropic",
			"auth_field":"ANTHROPIC_AUTH_TOKEN",
			"models":["glm-5.2"]
		},
		"openai":{"base_url":"","models":[]}
	}`)
	require.Equal(t, 204, res.Code)

	prov := rig.Provider(t, id)
	got, err := rig.Providers.Key(prov, providers.EndpointClaude)
	require.NoError(t, err)
	require.Equal(t, "sk-zhipu-abcdef123456", got)
}

// 端点级 key 显式传空串 = 清空，回落平台级。
func TestUpdateProviderWithEmptyEndpointKeyClearsIt(t *testing.T) {
	rig := newRoutesRig(t)
	id := rig.SeedProviderWithEndpointKey(t, "两把 key",
		"sk-platform-000000", "sk-claude-side-111111")

	res := rig.PUT(t, "/api/orciny/providers/"+id, `{
		"name":"两把 key",
		"claude":{
			"base_url":"https://open.bigmodel.cn/api/anthropic",
			"auth_field":"ANTHROPIC_AUTH_TOKEN",
			"models":["glm-5.2"],
			"key":""
		},
		"openai":{"base_url":"","models":[]}
	}`)
	require.Equal(t, 204, res.Code)

	prov := rig.Provider(t, id)
	got, err := rig.Providers.Key(prov, providers.EndpointClaude)
	require.NoError(t, err)
	require.Equal(t, "sk-platform-000000", got)
}

func TestCreateProviderFromDriftInlinesKey(t *testing.T) {
	rig := newRoutesRig(t)
	eventID := rig.SeedBindingDrift(t,
		`{"env":{"ANTHROPIC_BASE_URL":"https://relay.example/anthropic",
		  "ANTHROPIC_AUTH_TOKEN":"sk-relay-abcdef123456"}}`)

	res := rig.POST(t, "/api/orciny/providers", `{
		"name":"新中转",
		"claude":{
			"base_url":"https://relay.example/anthropic",
			"auth_field":"ANTHROPIC_AUTH_TOKEN",
			"models":[]
		},
		"openai":{"base_url":"","models":[]},
		"from_drift":{"event":"`+eventID+`",
		  "location":"env.ANTHROPIC_AUTH_TOKEN","endpoint":"claude"}
	}`)
	require.Equal(t, 200, res.Code)

	prov := rig.LastProvider(t)
	got, err := rig.Providers.Key(prov, providers.EndpointClaude)
	require.NoError(t, err)
	require.Equal(t, "sk-relay-abcdef123456", got)
}
```

追加到 `hub/internal/routes/config_test.go`：

```go
func TestExtractEndpointWritesKeyIntoProvider(t *testing.T) {
	rig := newRoutesRig(t)
	provID := rig.SeedProviderNoKey(t, "智谱 GLM")
	setID := rig.SeedDraft(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-zhipu-abcdef123456"}}`,
	})

	res := rig.POST(t, "/api/orciny/config-sets/"+setID+"/extract", `{
		"path":".claude/settings.json",
		"location":"env.ANTHROPIC_AUTH_TOKEN",
		"provider":"`+provID+`",
		"endpoint":"claude"
	}`)
	require.Equal(t, 204, res.Code)
}

func TestExtractRejectsMissingProvider(t *testing.T) {
	rig := newRoutesRig(t)
	setID := rig.SeedDraft(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-zhipu-abcdef123456"}}`,
	})
	res := rig.POST(t, "/api/orciny/config-sets/"+setID+"/extract", `{
		"path":".claude/settings.json","location":"env.ANTHROPIC_AUTH_TOKEN"
	}`)
	require.Equal(t, 400, res.Code)
}

func TestProviderMatchEndpoint(t *testing.T) {
	rig := newRoutesRig(t)
	provID := rig.SeedProvider(t, "智谱 GLM", "sk-zhipu-abcdef123456")
	setID := rig.SeedDraft(t, map[string]string{
		".claude/settings.json": `{"env":{
			"ANTHROPIC_BASE_URL":"https://open.bigmodel.cn/api/anthropic"
		}}`,
	})

	res := rig.GET(t, "/api/orciny/config-sets/"+setID+
		"/provider-match?path=.claude/settings.json")
	require.Equal(t, 200, res.Code)
	require.Contains(t, res.Body.String(), provID)
}
```

> `newRoutesRig` 与它的 helper 按 `routes_test.go` / `providers_test.go`
> 现有的测试装置改写（那里已有一套 superuser 请求的封装）。

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/routes/ -v
```

Expected: FAIL。

- [ ] **Step 3: 实现**

`hub/internal/routes/providers.go`：`providerBody` / `endpointBody` 按
00-overview 的定义写，并加转换：

```go
func (b endpointBody) input() providers.EndpointInput {
	return providers.EndpointInput{
		BaseURL: b.BaseURL, AuthField: b.AuthField, Models: b.Models,
		Key: b.Key, Defaults: b.Defaults, DefaultModel: b.DefaultModel,
	}
}

func (b providerBody) input() providers.Input {
	return providers.Input{
		Name: b.Name, Preset: b.Preset, Note: b.Note, Key: b.Key,
		Claude: b.Claude.input(), OpenAI: b.OpenAI.input(),
	}
}
```

`createProvider` 的 `FromDrift` 分支改成传 endpoint：

```go
	if req.FromDrift != nil {
		id, err := d.Admin.CreateProviderFromDrift(
			req.FromDrift.Event, req.FromDrift.Location, req.FromDrift.Endpoint,
			req.input())
		...
	}
```

`hub/internal/routes/config.go`：`extractCredential` 改名 `extractKey` 并改请求体：

```go
func (d Deps) extractKey(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req struct {
		Path     string `json:"path"`
		Location string `json:"location"`
		Provider string `json:"provider"`
		Endpoint string `json:"endpoint"`
	}
	if err := e.BindBody(&req); err != nil ||
		req.Path == "" || req.Location == "" || req.Provider == "" {
		return e.BadRequestError("需要 path、location、provider", nil)
	}
	if req.Endpoint == "" {
		req.Endpoint = providers.EndpointClaude
	}
	if err := d.Admin.ExtractKeyToProvider(e.Request.PathValue("id"),
		req.Path, req.Location, req.Provider, req.Endpoint); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

func (d Deps) providerMatch(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	path := e.Request.URL.Query().Get("path")
	if path == "" {
		return e.BadRequestError("需要 path", nil)
	}
	m, err := d.Admin.MatchProviderIn(e.Request.PathValue("id"), path)
	if err != nil {
		return mapErr(e, err)
	}
	return e.JSON(http.StatusOK, m)
}
```

`Admin` 接口：`ExtractCredential` 换成

```go
	ExtractKeyToProvider(setID, path, location, providerID, endpoint string) error
	MatchProviderIn(setID, path string) (providers.Match, error)
	CreateProviderFromDrift(eventID, location, endpoint string, in providers.Input) (string, error)
```

`hub/api.go`：

```go
// ExtractKeyToProvider 把草稿里某处的值抽成某个 provider 端点的 key。
func (h *Hub) ExtractKeyToProvider(setID, path, location, providerID, endpoint string) error {
	if err := h.importer.Extract(setID, path, location, providerID, endpoint); err != nil {
		return err
	}
	// key 变了就要重注入：草稿还没发布，但这条 provider 可能已经被别的
	// 配置集绑着（M1.5 spec §5.2 的重注入语义）。
	return h.sync.NotifyProvider(providerID)
}

// MatchProviderIn 对草稿里某个文件的 base_url 做一次反查，供抽取向导预选。
func (h *Hub) MatchProviderIn(setID, path string) (providers.Match, error) {
	return h.importer.MatchProviderIn(setID, path)
}
```

`hub/internal/routes/routes.go`：

```go
	g.POST("/config-sets/{id}/extract", d.extractKey).Bind(su)
	g.GET("/config-sets/{id}/provider-match", d.providerMatch).Bind(su)
```

`mapErr` 里加一支（`secretbox.ErrShortValue` → 400）：

```go
	case errors.Is(err, secretbox.ErrShortValue), errors.Is(err, providers.ErrNoKey):
		return e.BadRequestError(err.Error(), nil)
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/... -v
```

Expected: PASS，除了 `credentials` 包自身与仍指着它的 `hub/api.go` 里那三个
待删方法（08 处理）。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/routes hub/api.go
git commit -m "feat(hub): 抽取与反查的 HTTP 端点跟进双端点"
```
