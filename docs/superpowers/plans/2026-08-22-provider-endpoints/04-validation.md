# 子计划 04 · configsets 发布校验与字面 token 跟进

**前置**：02（`protocol` 词法）、03（`providers` 双端点）
**读这份之前先读** [00-overview.md](00-overview.md) 的 Global Constraints 与全局接口契约。

**交付物**：`Refs` 去掉 `Creds` 字段；`collectRefs` / `refsOf` 不再收集 cred 引用；
`Validate` 去掉 `known` 参数、自己查机器变量；新增 `endpoint_missing` 阻断级校验；
`auth_field_mismatch` 与 `FixAuthField` 改用端点限定占位符；散在 `drift` 与
`importer` 里的两处字面 token 跟进。

**本子计划的核心是 `endpoint_missing`（Task 3）**。spec §3.2 说「显式前缀下这是
一行判断」——就是这一行：从 `refs.provider_keys` 里取出被引用的端点集合，
逐个查绑定 provider 的对应 `Endpoint.Configured()`。它是**阻断级**，不是警告：
引用了一个没配的端点，发布出去必然渲染失败。

---

### Task 1: `Refs` 去掉 `Creds`，`Validate` 去掉 `known`

**Files:**
- Modify: `hub/internal/configsets/service.go:38-44,60,178-210`
- Modify: `hub/internal/configsets/validate.go`
- Modify: `hub/internal/revisions/service.go:221-250`
- Modify: `hub/internal/routes/config.go:157-172,497-515`
- Test: `hub/internal/configsets/validate_test.go`
- Test: `hub/internal/configsets/service_test.go`

**Interfaces:**
- Consumes: `protocol.RefVar` / `RefProvider`
- Produces: `Refs{Vars, ProviderKeys}`；`(s *Service) Validate(setID string) ([]Problem, error)`

**存量 JSON 里的 `creds` 键不清洗**（spec §6.2）：结构体去掉字段后
`encoding/json` 反序列化自然忽略多出来的键。不要写迁移去洗它。

- [ ] **Step 1: 写失败的测试**

既有测试用的 helper 是 `newService(t) (*tests.TestApp, *configsets.Service)`
与直接调 `s.SetDraftFile(...)`，下面沿用同一套，不引入新基建。

先把 `validate_test.go` 里三处**引用凭据的既有用例**改掉：
`TestValidateAcceptsCleanDraft` 与 `TestValidateReportsUndefinedRef` 里的
`{{cred.k}}` / `{{cred.gone}}` 换成 `{{var.k}}` / `{{var.gone}}`，
`Validate(set.Id, map[string]bool{...})` 一律换成 `Validate(set.Id)`，
并按下面的 `seedVariable` 预先定义需要「已定义」的那些变量。

然后追加：

```go
// known 参数删掉之后，机器变量的「已定义」由 Service 自己查 variables 表。
// 未定义的 {{var.*}} 仍然要报——ProblemUndefinedRef 保留，只是现在只服务于它。
func TestValidateStillReportsUndefinedVar(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, ".claude/CLAUDE.md",
		[]byte("工作区：{{var.workspace}}\n"), 0o644, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	require.Equal(t, configsets.ProblemUndefinedRef, problems[0].Kind)
	require.Contains(t, problems[0].Detail, "var.workspace")

	// 给某台机器定义了这个变量之后就不再报。
	seedVariable(t, app, "workspace", "main")
	problems, err = s.Validate(set.Id)
	require.NoError(t, err)
	require.Empty(t, problems)
}

// {{cred.*}} 现在是语法错误，由 protocol 的词法在 Refs 阶段就拒掉。
func TestValidateReportsCredAsSyntaxError(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	// 直接塞一条清单项：SetDraftFile 会先跑 collectRefs，语法错在那里就炸了。
	// 用 blobs 落内容 + SetDraft 绕过，与 TestValidateReportsOversizeEntry 同法。
	b := blobs.New(app)
	hash, _, err := b.Put([]byte(`{"env":{"K":"{{cred.zhipu}}"}}`))
	require.NoError(t, err)
	require.NoError(t, s.SetDraft(set.Id, []protocol.FileEntry{{
		Path: ".claude/settings.json", Hash: hash, Size: 30, Mode: 0o600,
	}}))

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	require.Equal(t, configsets.ProblemUndefinedRef, problems[0].Kind)
	require.Contains(t, problems[0].Detail, "未知前缀")
}

func TestRefsFieldHasNoCreds(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	seedVariable(t, app, "workspace", "main")
	_, err = s.SetDraftFile(set.Id, ".claude/settings.json", []byte(`{"env":{
		"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}",
		"X":"{{var.workspace}}"
	}}`), 0o600, nil)
	require.NoError(t, err)

	refs, err := s.DraftRefs(set.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"workspace"}, refs.Vars)
	require.Equal(t, []string{"claude.base_url"}, refs.ProviderKeys)
}
```

> `blobs.Put` 的确切签名照 `TestValidateReportsOversizeEntry` 里既有的用法
> 写，那条用例已经在做同一件事（绕过 `SetDraftFile` 的前置检查）。

`seedVariable` helper（加到 `service_test.go`，`newService` 旁边）：

```go
func seedVariable(t *testing.T, app core.App, key, value string) {
	t.Helper()
	mc, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	m := core.NewRecord(mc)
	m.Set("fingerprint", "fp-"+key)
	m.Set("pub_key", "pk")
	m.Set("status", "offline")
	require.NoError(t, app.Save(m))

	vc, err := app.FindCollectionByNameOrId("variables")
	require.NoError(t, err)
	v := core.NewRecord(vc)
	v.Set("machine", m.Id)
	v.Set("key", key)
	v.Set("value", value)
	require.NoError(t, app.Save(v))
}
```

`DraftRefs` 若尚未存在，加一个三行的读取方法（`Refs` 目前只在内部用）：

```go
// DraftRefs 读回草稿的引用集合。
func (s *Service) DraftRefs(setID string) (Refs, error) {
	r, err := s.record(setID)
	if err != nil {
		return Refs{}, err
	}
	var refs Refs
	_ = r.UnmarshalJSONField("draft_refs", &refs)
	return refs, nil
}
```

同时把既有测试里所有 `s.Validate(setID, known)` 的调用改成 `s.Validate(setID)`，
并删掉构造 `known` 的那几行。

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/configsets/ -v
```

Expected: FAIL —— `Validate` 参数个数不对、`protocol.RefCred` 未定义（编译错）。

- [ ] **Step 3: 实现**

`hub/internal/configsets/service.go`：

```go
// Refs 是 revisions.refs 与 config_sets.draft_refs 的形状。
//
// Creds 字段随 {{cred.*}} 一起删除（M1.6 spec §3.5）。存量 JSON 里多出的
// creds 键反序列化时自然忽略，**不清洗**（spec §6.2）。
type Refs struct {
	Vars         []string `json:"vars"`
	ProviderKeys []string `json:"provider_keys"`
}
```

初始化那行（`service.go:60`）：

```go
	r.Set("draft_refs", Refs{Vars: []string{}, ProviderKeys: []string{}})
```

`collectRefs`：删掉 `creds` map、`case protocol.RefCred` 分支与返回结构里的
`Creds` 字段。注释里「引用集合是凭据删除保护的依据」改成「引用集合是快照裁剪
与发布校验的依据」。

`hub/internal/revisions/service.go` 的 `refsOf` 同样处理。

`hub/internal/configsets/validate.go` 的 `Validate`：

```go
// Validate 检查草稿能不能发布。
//
// 机器变量的「已定义」由本方法自己查 variables 表——原来的 known 参数装的是
// 「已定义的凭据名」，凭据实体废止后它没有内容可装了（M1.6 spec §3.5）。
//
// 未定义引用必须在这里拒掉：让它进 Revision，agent 渲染时只能拒绝 apply
// 该文件，而那时用户已经点过发布了（M1 spec §6.1）。
func (s *Service) Validate(setID string) ([]Problem, error) {
	files, err := s.Draft(setID)
	if err != nil {
		return nil, err
	}
	known, err := s.knownVars()
	if err != nil {
		return nil, err
	}
	// …（主循环不变，只把 known[ref.String()] 换成 known[ref.Name]）
}

// knownVars 汇总全库已定义的机器变量名。
//
// 不按机器分：发布是全配置集一次的动作，而同一个配置集可能指派给多台机器。
// 「至少有一台机器定义了它」是发布期能给出的最宽松的判定，剩下的由 agent
// 渲染时的未定义引用兜底（M1 spec §6.1）。
func (s *Service) knownVars() (map[string]bool, error) {
	recs, err := s.app.FindAllRecords("variables")
	if err != nil {
		return nil, fmt.Errorf("configsets: 读取机器变量: %w", err)
	}
	out := make(map[string]bool, len(recs))
	for _, r := range recs {
		out[r.GetString("key")] = true
	}
	return out, nil
}
```

主循环里那段判断改成：

```go
		for _, ref := range refs {
			// machine.* 是内置值；provider.* 的「已定义」由绑定与端点校验
			// 判定（M1.6 spec §4），不走这条路——否则每个绑了服务的配置集
			// 都会报一堆假的未定义引用。
			if ref.Kind != protocol.RefVar {
				continue
			}
			if !known[ref.Name] {
				problems = append(problems, Problem{Path: f.Path, Kind: ProblemUndefinedRef,
					Detail: "未定义的引用 " + ref.String()})
			}
		}
```

`hub/internal/routes/config.go`：

- `validateConfigSet` 去掉 `d.knownRefs` 调用与 `d.Creds == nil` 判断：

```go
func (d Deps) validateConfigSet(e *core.RequestEvent) error {
	if d.Sets == nil {
		return e.InternalServerError("配置集服务未就绪", nil)
	}
	problems, err := d.Sets.Validate(e.Request.PathValue("id"))
	if err != nil {
		return mapErr(e, err)
	}
	if problems == nil {
		problems = []configsets.Problem{}
	}
	return e.JSON(http.StatusOK, problems)
}
```

- `knownRefs` 这个 helper **整个删掉**。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/internal/configsets/ ./hub/internal/revisions/ -v
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/configsets hub/internal/revisions hub/internal/routes/config.go
git commit -m "feat(hub): 引用集合与发布校验去掉凭据这一层"
```

---

### Task 2: `auth_field_mismatch` 改写为只管 claude 端点

**Files:**
- Modify: `hub/internal/configsets/binding.go`
- Test: `hub/internal/configsets/binding_test.go`（`seedProvider` 改造）
- Test: `hub/internal/configsets/validate_test.go`（`auth_field` 的三条既有用例 + 一条新的）

**Interfaces:**
- Consumes: `providers.ClaudeOf` / `ClaudeEndpoint` / `OpenAIEndpoint`
- Produces: `tokenAuthToken = "{{provider.claude.auth_token}}"`；
  `seedProvider` / `seedProviderWith` 测试 helper；`FixAuthField` 行为语义不变

**openai 端点不做这条校验**（spec §4.2）：它的 `auth_field` 是自由文本，
没有「二选一选错了」这种可判定的错误形态；而 `.codex/config.toml` 的结构
也不是 `settings.json` 的 `env` 对象。接 Codex 时再立。

- [ ] **Step 1: 写失败的测试**

先改 `binding_test.go` 里的 `seedProvider`——它现在写的是 003 的老字段，
必须换成端点 JSON（顺带不再建凭据）：

```go
// seedProvider 建一条只配了 claude 端点的 Provider，返回记录 id。
// 直接写记录而不走 providers.Store：configsets 的测试不该被 Store 的校验绑住。
func seedProvider(t *testing.T, app core.App, name string) string {
	t.Helper()
	return seedProviderWith(t, app, name, "")
}

// seedProviderWith 可以额外配上 openai 端点。openaiBaseURL 为空即不配。
func seedProviderWith(t *testing.T, app core.App, name, openaiBaseURL string) string {
	t.Helper()
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	p := core.NewRecord(c)
	p.Set("name", name)
	p.Set("key_cipher", "x")
	p.Set("key_last4", "1234")
	p.Set("claude", providers.ClaudeEndpoint{
		Endpoint: providers.Endpoint{
			BaseURL:   "https://open.bigmodel.cn/api/anthropic",
			AuthField: providers.AuthToken,
			KeyLast4:  "1234",
			Models:    []string{"glm-5.2"},
		},
	})
	p.Set("openai", providers.OpenAIEndpoint{
		Endpoint: providers.Endpoint{
			BaseURL:   openaiBaseURL,
			AuthField: providers.DefaultOpenAIAuthField,
			Models:    []string{},
		},
	})
	require.NoError(t, app.Save(p))
	return p.Id
}
```

`validate_test.go` 里 `auth_field` 那三条既有用例（`TestValidateAuthFieldMismatchGivesFix`、
`TestValidateAuthFieldMatchIsQuiet`、`TestFixAuthFieldRewritesDraft`）把
`{{provider.auth_token}}` / `{{provider.base_url}}` 全部换成端点限定写法，
`Validate(set.Id, …)` 换成 `Validate(set.Id)`。然后追加：

```go
// 旧的字面量不再被认作「承载 key 的那一行」——它现在压根过不了词法。
func TestAuthFieldMismatchIgnoresOldUnqualifiedToken(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	provID := seedProvider(t, app, "智谱 GLM · 个人")

	b := blobs.New(app)
	body := []byte(`{"env":{"ANTHROPIC_API_KEY":"{{provider.auth_token}}"}}`)
	hash, _, err := b.Put(body)
	require.NoError(t, err)
	require.NoError(t, s.SetDraft(set.Id, []protocol.FileEntry{{
		Path: configsets.SettingsPath, Hash: hash,
		Size: uint32(len(body)), Mode: 0o600,
	}}))
	require.NoError(t, s.SetDraftBinding(set.Id, &providers.Binding{Provider: provID}))

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	// 报的是语法错误，不是 auth_field_mismatch
	for _, p := range problems {
		require.NotEqual(t, configsets.ProblemAuthFieldMismatch, p.Kind)
	}
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/configsets/ -run AuthField -v
```

Expected: FAIL —— 实现里的常量还是 `{{provider.auth_token}}`。

- [ ] **Step 3: 实现**

Modify `hub/internal/configsets/binding.go`：

```go
// tokenAuthToken 是承载 claude 端点 API key 的那个占位符的字面形态。
//
// **只管 claude 端点**（M1.6 spec §4.2）：这条校验找的是 settings.json 的 env
// 里键名对不对，而 openai 端点的 auth_field 是自由文本，没有「二选一选错了」
// 这种可判定的错误形态；.codex/config.toml 的结构也不是 env 对象。
const tokenAuthToken = "{{provider.claude.auth_token}}"
```

`validateBinding` 第 3 条与 `FixAuthField` 里的

```go
	want := prov.GetString("auth_field")
```

都换成

```go
	want := providers.ClaudeOf(prov).AuthField
```

`validateBinding` 第 1、2 条的文案更新为端点限定写法（spec §4.1）：

```go
	if usedIn != "" && binding == nil {
		return append(out, Problem{
			Path: usedIn, Kind: ProblemBindingMissing,
			Detail: "文件里用了 {{provider.claude.*}} 或 {{provider.openai.*}}，" +
				"但这个配置集还没有服务绑定。到「服务绑定」区选一个 AI 服务配置。",
		}), nil
	}
```

```go
	if usedIn == "" {
		out = append(out, Problem{
			Kind: ProblemBindingUnused, Warning: true,
			Detail: "已绑定服务配置，但没有任何文件用到 {{provider.claude.*}}。" +
				"用「插入 env 片段」把它写进 settings.json。",
		})
		return out, nil
	}
```

判断依据本身**不变**：仍然是「文件里有没有任一 `RefProvider` 引用」——
词法变了，逻辑没变（spec §4.1）。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/internal/configsets/ -v
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/configsets/
git commit -m "feat(hub): auth_field 校验改用端点限定占位符"
```

---

### Task 3: 新增 `endpoint_missing`

**Files:**
- Modify: `hub/internal/configsets/validate.go`（常量）
- Modify: `hub/internal/configsets/binding.go`（判定）
- Test: `hub/internal/configsets/validate_test.go`

**Interfaces:**
- Consumes: `protocol.EndpointOf`、`providers.ClaudeOf` / `OpenAIOf` / `Configured()`
- Produces: `configsets.ProblemEndpointMissing = "endpoint_missing"`

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/configsets/validate_test.go`（用 Task 2 定义的
`seedProvider` / `seedProviderWith`）：

```go
// 引用了 openai 端点，但绑定的 provider 只配了 claude → 阻断级错误。
func TestValidateEndpointMissingIsBlocking(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	provID := seedProvider(t, app, "智谱 GLM · 个人") // 只配 claude

	_, err = s.SetDraftFile(set.Id, configsets.SettingsPath, []byte(`{"env":{
		"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}",
		"ANTHROPIC_AUTH_TOKEN":"{{provider.claude.auth_token}}"
	}}`), 0o600, nil)
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, ".codex/config.toml",
		[]byte("base_url = \"{{provider.openai.base_url}}\"\n"), 0o644, nil)
	require.NoError(t, err)
	require.NoError(t, s.SetDraftBinding(set.Id, &providers.Binding{Provider: provID}))

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)

	var p *configsets.Problem
	for i := range problems {
		if problems[i].Kind == configsets.ProblemEndpointMissing {
			p = &problems[i]
		}
	}
	require.NotNil(t, p, "必须报 endpoint_missing")
	require.False(t, p.Warning, "阻断级，不是警告")
	require.Equal(t, ".codex/config.toml", p.Path)
	require.Contains(t, p.Detail, "OpenAI 端点")
}

// 两个端点都配了就不报。
func TestValidateNoEndpointMissingWhenBothConfigured(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	provID := seedProviderWith(t, app, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/paas/v4")

	_, err = s.SetDraftFile(set.Id, ".codex/config.toml",
		[]byte("base_url = \"{{provider.openai.base_url}}\"\n"), 0o644, nil)
	require.NoError(t, err)
	require.NoError(t, s.SetDraftBinding(set.Id, &providers.Binding{Provider: provID}))

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	for _, p := range problems {
		require.NotEqual(t, configsets.ProblemEndpointMissing, p.Kind)
	}
}

// 每个缺失的端点只报一条，路径取字典序第一个引用它的文件——
// 一个配置集里同一个端点被十个文件引用时，报十条只是噪音。
func TestEndpointMissingIsReportedOncePerEndpoint(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	provID := seedProvider(t, app, "智谱 GLM · 个人")

	_, err = s.SetDraftFile(set.Id, ".codex/a.toml",
		[]byte("u = \"{{provider.openai.base_url}}\"\n"), 0o644, nil)
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, ".codex/b.toml",
		[]byte("k = \"{{provider.openai.api_key}}\"\n"), 0o644, nil)
	require.NoError(t, err)
	require.NoError(t, s.SetDraftBinding(set.Id, &providers.Binding{Provider: provID}))

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	n := 0
	for _, p := range problems {
		if p.Kind == configsets.ProblemEndpointMissing {
			n++
			require.Equal(t, ".codex/a.toml", p.Path)
		}
	}
	require.Equal(t, 1, n)
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/configsets/ -run Endpoint -v
```

Expected: FAIL —— `ProblemEndpointMissing` 未定义。

- [ ] **Step 3: 实现**

`hub/internal/configsets/validate.go` 的常量块加一行：

```go
	// M1.6：双端点（spec §4.3）
	ProblemEndpointMissing = "endpoint_missing"
```

`hub/internal/configsets/binding.go` 的 `validateBinding`：第一遍扫描时把
「哪个端点被哪个文件先引用到」一起收下来，第 3 条之后加第 4 条。

第一遍的循环改成：

```go
	// 第一遍：谁引用了 provider.*、哪些端点被引用到，以及 settings.json 的内容。
	usedIn := ""
	// endpointUsedIn: 端点名 → 第一个引用它的文件路径。
	// files 已按 Path 升序（saveDraft 排过），因此「第一个」是确定的。
	endpointUsedIn := map[string]string{}
	var settings []byte
	for _, f := range files {
		content, err := s.blobs.Get(f.Hash)
		if err != nil {
			continue
		}
		if f.Path == SettingsPath {
			settings = content
		}
		refs, err := protocol.Refs(content)
		if err != nil {
			continue // 语法错误由 Validate 的主循环报，这里不重复
		}
		for _, r := range refs {
			if r.Kind != protocol.RefProvider {
				continue
			}
			if usedIn == "" {
				usedIn = f.Path
			}
			if ep := protocol.EndpointOf(r.Name); ep != "" {
				if _, seen := endpointUsedIn[ep]; !seen {
					endpointUsedIn[ep] = f.Path
				}
			}
		}
	}
```

第 3 条之后追加：

```go
	// 4. 引用的端点没配（spec §4.3）。阻断级：发布出去必然渲染失败。
	//
	// spec §3.2 说的「显式前缀下这是一行判断」就是这里——端点段直接写在
	// 占位符里，hub 不需要任何「这个路径属于哪个工具」的推断。
	for _, ep := range []string{providers.EndpointClaude, providers.EndpointOpenAI} {
		path, used := endpointUsedIn[ep]
		if !used {
			continue
		}
		configured := false
		label := ""
		switch ep {
		case providers.EndpointClaude:
			configured, label = providers.ClaudeOf(prov).Configured(), "Claude"
		case providers.EndpointOpenAI:
			configured, label = providers.OpenAIOf(prov).Configured(), "OpenAI"
		}
		if configured {
			continue
		}
		out = append(out, Problem{
			Path: path, Kind: ProblemEndpointMissing,
			Detail: fmt.Sprintf(
				"文件 %s 引用了 {{provider.%s.*}}，但绑定的服务配置「%s」没有配置 %s 端点。"+
					"到「AI 服务」页给它补上 base_url，或改引用另一侧端点。",
				path, ep, prov.GetString("name"), label),
		})
	}
```

（`prov` 在第 3 条那里已经取过了；把 `prov, err := s.provs.Get(...)` 那两行
提到第 3 条之前，让第 3、4 条共用。）

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/internal/configsets/ -v
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/configsets/
git commit -m "feat(hub): 发布校验新增 endpoint_missing"
```

---

### Task 4: 散在别处的两处字面 token 跟进

**Files:**
- Modify: `hub/internal/drift/binding.go:18`（`baseURLToken`）
- Modify: `hub/internal/importer/scan.go:50-52`（`placeholderValue` 正则）
- Test: `hub/internal/drift/binding_test.go`
- Test: `hub/internal/importer/scan_test.go`

**Interfaces:**
- Consumes: 无
- Produces: 行为不变，只是认的字面量变了

这两处不属于 `configsets`，但它们与词法是同一件事：一个认「基线里的
base_url 占位符」，一个认「已经被抽成占位符的值」。放在这里是为了让
**hub 侧对新词法的跟进在一次提交里做完**，不留半新半旧的状态。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/drift/binding_test.go`：

```go
func TestDetectBindingDriftUsesEndpointQualifiedToken(t *testing.T) {
	base := []byte(`{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}"}}`)
	cur := []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://relay.example/anthropic"}}`)

	url, ok := drift.DetectBindingDrift(base, cur)
	require.True(t, ok)
	require.Equal(t, "https://relay.example/anthropic", url)
}

// 旧字面量不再被认作绑定漂移的基线锚点：存量 blob 里的它下次发布时会被
// 词法拒掉，不该再顺着它去猜（spec §6.2）。
func TestDetectBindingDriftIgnoresOldToken(t *testing.T) {
	base := []byte(`{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`)
	cur := []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://relay.example/anthropic"}}`)
	_, ok := drift.DetectBindingDrift(base, cur)
	require.False(t, ok)
}
```

`binding_test.go` 里既有的五条用例，把基线里的 `{{provider.base_url}}`
全部替换成 `{{provider.claude.base_url}}`，断言不变。

追加到 `hub/internal/importer/scan_test.go`：

```go
// 已经是占位符的值不再报——否则 Extract 之后 Findings 会把同一条再吐回来。
func TestScanSkipsEndpointQualifiedPlaceholders(t *testing.T) {
	content := []byte(`{"env":{
		"ANTHROPIC_AUTH_TOKEN":"{{provider.claude.auth_token}}",
		"OPENAI_API_KEY":"{{provider.openai.api_key}}",
		"X_TOKEN":"{{var.some_token}}"
	}}`)
	require.Empty(t, importer.Scan(".claude/settings.json", content))
}

// {{cred.*}} 已废止，它现在只是一个普通字符串——但它也不像密钥，
// 不该因为「键名含 token」就被报成敏感项之外的东西。这条锁住不炸。
func TestScanHandlesLegacyCredPlaceholder(t *testing.T) {
	content := []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"{{cred.zhipu}}"}}`)
	require.NotPanics(t, func() { _ = importer.Scan(".claude/settings.json", content) })
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/drift/ ./hub/internal/importer/ -run 'BindingDrift|Placeholder|LegacyCred' -v
```

Expected: FAIL。

- [ ] **Step 3: 实现**

`hub/internal/drift/binding.go`：

```go
// baseURLToken 是基线里 claude 端点 base_url 那一处的字面形态。
//
// 只认 claude 端点（M1.6 spec §1.3）：漂移源只有 .claude/**，
// openai 端点本期不会产生漂移。
const baseURLToken = "{{provider.claude.base_url}}"
```

`hub/internal/importer/scan.go`：

```go
	// 已被抽成占位符的值不再报——否则 Extract 之后 Findings 会把同一条再吐回来。
	// provider.* 是**端点限定**的两段名（M1.6 spec §3.1）：绑了服务的配置集里
	// 它到处都是。cred 前缀已废止，不再列入。
	placeholderValue = regexp.MustCompile(
		`^\{\{(var|machine)\.[A-Za-z0-9_-]+\}\}$|` +
			`^\{\{provider\.(claude|openai)\.[A-Za-z0-9_-]+\}\}$`)
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/internal/drift/ ./hub/internal/importer/ -v
```

Expected: 这两个包里与本任务相关的用例 PASS。其余用例若因 07 还没做而红
（`Extract` 的签名、`Rebind` 读 `defaults`），留给 07。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/drift/binding.go hub/internal/drift/binding_test.go hub/internal/importer/scan.go hub/internal/importer/scan_test.go
git commit -m "fix(hub): 漂移锚点与敏感项检测跟进端点限定词法"
```
