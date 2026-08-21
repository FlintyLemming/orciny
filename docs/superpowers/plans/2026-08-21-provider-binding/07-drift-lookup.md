# 子计划 07 · 绑定漂移的反查前两档

**前置**：05、06
**读这份之前先读** [00-overview.md](00-overview.md) 的 Global Constraints 与全局接口契约。

**交付物**：`MatchBinding` 反查、`Rebind` 改绑、`ExtractKey` 把机器上手写的 key 抽成凭据；两个 HTTP 端点与 `hub` 公开入口；卡片上的两档动作。

**纯增量**：子计划 06 的第三档骨架一行不改，前两档命中时把兜底文案换成可操作的按钮。

**反查三档**（spec §6.3）：

| 命中 | 卡片提供的动作 |
|---|---|
| **已有 Provider** | 「这台机器改用了『Kimi 官方』。把配置集的绑定改成它？」→ 改的是**绑定**，产生新 Revision，绑定活着，全机队跟着走 |
| **内置预设** | 「识别为 Kimi。新建服务配置？」→ 打开新建向导，预填平台与 base_url；把机器上手写的那个 key 抽成凭据 → 建成后回到上一行的动作 |
| **都不命中** | 只给「恢复」「忽略」，卡片写明"无法识别这个 base_url 属于哪个平台"（子计划 06 已完成） |

**这个交互的价值**（照抄 spec §6.3 末段，写进包注释）：它匹配了真实用法——「我在某台机器上试了个新中转，好用」。没有它，这个发现要手工搬回 Web 重做一遍；有了它，一键变成全机队的决定。

---

### Task 1: `MatchBinding` 与 `ExtractKey`

**Files:**
- Modify: `hub/internal/drift/binding.go`
- Modify: `hub/internal/drift/service.go`（`Deps` 加 `Providers` / `Creds`）
- Modify: `hub/hub.go`（装配）
- Test: `hub/internal/drift/binding_test.go`（追加）

**Interfaces:**
- Consumes: `providers.Store.MatchBaseURL`、`importer.Scan`、`credentials.Store.Create`
- Produces: `BindingMatch`、`(*Service).MatchBinding(eventID)`、`(*Service).ExtractKey(eventID, location, credName) (string, error)`

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/drift/binding_test.go`（用 `rig_test.go` 的 rig；rig 要补上建 Provider 的辅助）：

```go
// 第一档：字面 URL 精确命中已有 Provider。
func TestMatchBindingHitsExistingProvider(t *testing.T) {
	r := newRig(t)
	kimi := r.seedProvider(t, "Kimi 官方", "https://api.moonshot.cn/anthropic", "sk-kimi-abcdefghij")
	eventID := r.seedBindingDrift(t, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic", "")

	m, err := r.svc.MatchBinding(eventID)
	require.NoError(t, err)
	require.Equal(t, "https://api.moonshot.cn/anthropic", m.URL)
	require.Equal(t, providers.MatchProvider, m.Match.Kind)
	require.True(t, m.Match.Exact)
	require.Equal(t, kimi, m.Match.ProviderID)
}

// 第二档：库里没有，内置预设里有。
func TestMatchBindingHitsPreset(t *testing.T) {
	r := newRig(t)
	eventID := r.seedBindingDrift(t, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic", "")

	m, err := r.svc.MatchBinding(eventID)
	require.NoError(t, err)
	require.Equal(t, providers.MatchPreset, m.Match.Kind)
	require.Equal(t, "kimi", m.Match.PresetID)
}

// 第三档：都不命中。
func TestMatchBindingNoHit(t *testing.T) {
	r := newRig(t)
	eventID := r.seedBindingDrift(t, ".claude/settings.json",
		"https://某个没人听说过的中转.test/v1", "")

	m, err := r.svc.MatchBinding(eventID)
	require.NoError(t, err)
	require.Equal(t, providers.MatchNone, m.Match.Kind)
}

// 第二档还要顺手找出机器上手写的那把 key，供新建向导抽成凭据。
func TestMatchBindingFindsHandWrittenKey(t *testing.T) {
	r := newRig(t)
	eventID := r.seedBindingDrift(t, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic", "sk-kimi-QWERTYUIOPasdfghjkl1234")

	m, err := r.svc.MatchBinding(eventID)
	require.NoError(t, err)
	require.Equal(t, "env.ANTHROPIC_AUTH_TOKEN", m.KeyLocation)
	require.Contains(t, m.KeyMasked, "…")
	require.NotContains(t, m.KeyMasked, "QWERTYUIOPasdfghjkl",
		"掩码里不许出现完整的 key")
}

func TestMatchBindingRejectsNonBindingDrift(t *testing.T) {
	r := newRig(t)
	eventID := r.seedNormalDrift(t, "CLAUDE.md")
	_, err := r.svc.MatchBinding(eventID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "不是绑定漂移")
}

func TestExtractKeyCreatesCredential(t *testing.T) {
	r := newRig(t)
	eventID := r.seedBindingDrift(t, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic", "sk-kimi-QWERTYUIOPasdfghjkl1234")

	credID, err := r.svc.ExtractKey(eventID, "env.ANTHROPIC_AUTH_TOKEN", "kimi_key")
	require.NoError(t, err)

	v, err := r.creds.ValueByID(credID)
	require.NoError(t, err)
	require.Equal(t, "sk-kimi-QWERTYUIOPasdfghjkl1234", v)
}

// 太短的值抽成凭据会在还原时到处误匹配（M1 spec §6.4），一律拒绝。
func TestExtractKeyRejectsShortValue(t *testing.T) {
	r := newRig(t)
	eventID := r.seedBindingDrift(t, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic", "abc")
	_, err := r.svc.ExtractKey(eventID, "env.ANTHROPIC_AUTH_TOKEN", "kimi_key")
	require.Error(t, err)
}
```

> `seedBindingDrift(t, path, url, key)` 是 rig 的新辅助：造一条 `binding_drift = true`、`binding_url = url` 的 open 漂移，`current_blob` 的内容是
> `{"env":{"ANTHROPIC_BASE_URL":"<url>","ANTHROPIC_AUTH_TOKEN":"<key 或占位符>"}}`。
> `seedProvider` 照子计划 02 的写法。

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/drift/ -run 'MatchBinding|ExtractKey' -v
```

Expected: FAIL，未定义。

- [ ] **Step 3: 实现**

`hub/internal/drift/service.go` 的 `Deps` 追加：

```go
	// Providers 供绑定漂移的反查（spec §6.3）。
	Providers *providers.Store
	// Creds 供把机器上手写的 key 抽成凭据（spec §6.3 第二档）。
	Creds *credentials.Store
```

`hub/hub.go` 装配处补上 `Providers: h.provs, Creds: h.creds`。

`hub/internal/drift/binding.go` 追加：

```go
// BindingMatch 是一次绑定漂移的反查结果（spec §6.3）。
type BindingMatch struct {
	// URL 是机器上那段字面 base_url。
	URL string `json:"url"`
	// Match 是三档判定的结果。Exact 为 false 时 UI 措辞降级为「可能是」。
	Match providers.Match `json:"match"`
	// KeyLocation / KeyMasked 是在漂移内容里找到的、机器上手写的那把 key。
	// 第二档的新建向导用它把 key 抽成凭据，用户不必再去平台后台复制一遍。
	KeyLocation string `json:"key_location,omitempty"`
	KeyMasked   string `json:"key_masked,omitempty"`
}

// MatchBinding 对一条绑定漂移做反查（spec §6.3）。
//
// 这个交互的价值在于它匹配了真实用法：「我在某台机器上试了个新中转，好用」
// ——没有它，这个发现要手工搬回 Web 重做一遍；有了它，一键变成全机队的决定。
func (s *Service) MatchBinding(eventID string) (BindingMatch, error) {
	var out BindingMatch
	rec, err := s.d.App.FindRecordById("drift_events", eventID)
	if err != nil {
		return out, fmt.Errorf("drift: 漂移 %s 不存在: %w", eventID, err)
	}
	if !rec.GetBool("binding_drift") {
		return out, fmt.Errorf("drift: %s 不是绑定漂移", rec.GetString("path"))
	}
	out.URL = rec.GetString("binding_url")

	m, err := s.d.Providers.MatchBaseURL(out.URL)
	if err != nil {
		return out, err
	}
	out.Match = m

	if content, err := s.driftContent(rec); err == nil {
		out.KeyLocation, out.KeyMasked = findAuthKey(rec.GetString("path"), content)
	}
	return out, nil
}

// findAuthKey 在漂移内容里找那把机器上手写的 API key。
//
// 复用 importer 的敏感项检测（产品 §4.2 的既有能力），但只认 env 里两个
// 鉴权字段的位置——全文扫出来的其他候选与「换了家供应商」无关。
func findAuthKey(path string, content []byte) (location, masked string) {
	for _, f := range importer.Scan(path, content) {
		switch f.Location {
		case "env." + providers.AuthToken, "env." + providers.AuthAPIKey:
			return f.Location, f.Masked
		}
	}
	return "", ""
}

// ExtractKey 把漂移内容里 location 处的值抽成凭据，返回凭据记录 id。
//
// 源是漂移 blob 而不是草稿（与 importer.Extract 的区别就在这里）：
// 这把 key 是用户在**机器上**手写的，中台从没见过它。
func (s *Service) ExtractKey(eventID, location, credName string) (string, error) {
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
	cred, err := s.d.Creds.Create(credName, value,
		"从机器 "+rec.GetString("machine")+" 的漂移里抽取")
	if err != nil {
		return "", err
	}
	return cred.Id, nil
}

// driftContent 读一条漂移的现状内容。
func (s *Service) driftContent(rec *core.Record) ([]byte, error) {
	blobID := rec.GetString("current_blob")
	if blobID == "" {
		return nil, fmt.Errorf("drift: %s 没有现状内容", rec.GetString("path"))
	}
	b, err := s.d.App.FindRecordById("blobs", blobID)
	if err != nil {
		return nil, fmt.Errorf("drift: 读取现状内容: %w", err)
	}
	return s.d.Blobs.Get(b.GetString("hash"))
}
```

> `credentials.Store.Create` 已经有 `MinValueLen` 的长度下限校验，短值会被它拒掉——`TestExtractKeyRejectsShortValue` 靠的就是这条，不用在 drift 里重复实现。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/... -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/ && git commit -m "feat(hub): 绑定漂移的反查与手写 key 抽取"
```

---

### Task 2: `Rebind`

**Files:**
- Modify: `hub/internal/drift/binding.go`
- Test: `hub/internal/drift/binding_test.go`（追加）

**Interfaces:**
- Consumes: `revisions.Service.Head` / `Files` / `PublishFiles`、`configsets.Service.SetDraftBinding`、`configsync.Service.NotifyConfigSet`
- Produces: `(*Service).Rebind(eventID, providerID string) (*core.Record, error)`

**改的是绑定，产生新 Revision**（spec §2.2 / §6.3 第一档）：绑定活着，全机队跟着走。

**发布的是 head 的清单而不是草稿**：改绑定不该顺手把用户草稿里没做完的编辑一起推上去。草稿绑定也同步改掉，否则用户下一次发布会把绑定又切回去。

**不主动关掉这条漂移**：新 Revision 下发 → agent apply → `ApplyAck` → `resolveDriftOnApply` 会把它标成 `superseded`（M1 已有的路径，spec §7.7）。在这里抢先标记等于撒谎——那时机器上还没变。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/drift/binding_test.go`：

```go
func TestRebindProducesNewRevisionAndKeepsBindingAlive(t *testing.T) {
	r := newRig(t)
	zhipu := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	kimi := r.seedProvider(t, "Kimi 官方",
		"https://api.moonshot.cn/anthropic", "sk-kimi-abcdefghij")

	setID := r.seedBoundSet(t, zhipu, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	})
	before, err := r.revs.Head(setID)
	require.NoError(t, err)

	eventID := r.seedBindingDriftIn(t, setID, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic", "")

	rev, err := r.svc.Rebind(eventID, kimi)
	require.NoError(t, err)
	require.NotEqual(t, before.Id, rev.Id, "换绑定必须产生新 Revision")

	got, err := r.revs.BindingOf(rev.Id)
	require.NoError(t, err)
	require.Equal(t, kimi, got.Provider)

	set, err := r.app.FindRecordById("config_sets", setID)
	require.NoError(t, err)
	require.Equal(t, rev.Id, set.GetString("head"))
	require.Equal(t, kimi, set.GetString("head_provider"))

	// 草稿绑定也要跟着走，否则下一次发布会把绑定切回智谱。
	draft, err := r.sets.DraftBinding(setID)
	require.NoError(t, err)
	require.Equal(t, kimi, draft.Provider)

	// 文件内容不变——改的只是绑定。
	files, err := r.revs.Files(rev.Id)
	require.NoError(t, err)
	oldFiles, err := r.revs.Files(before.Id)
	require.NoError(t, err)
	require.Equal(t, oldFiles, files)
}

// 新绑定的模型槽取新 Provider 的 defaults——换了家供应商，
// 旧供应商的模型 id 在新 endpoint 上没有意义。
func TestRebindTakesNewProviderDefaults(t *testing.T) {
	r := newRig(t)
	zhipu := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	kimi := r.seedProviderWithDefaults(t, "Kimi 官方",
		"https://api.moonshot.cn/anthropic", "sk-kimi-abcdefghij",
		providers.ModelSlots{
			Main: "kimi-k2", Opus: "kimi-k2", Sonnet: "kimi-k2", Haiku: "kimi-k2",
		})

	setID := r.seedBoundSet(t, zhipu, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	})
	eventID := r.seedBindingDriftIn(t, setID, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic", "")

	rev, err := r.svc.Rebind(eventID, kimi)
	require.NoError(t, err)

	got, err := r.revs.BindingOf(rev.Id)
	require.NoError(t, err)
	require.Equal(t, providers.ModelSlots{
		Main: "kimi-k2", Opus: "kimi-k2", Sonnet: "kimi-k2", Haiku: "kimi-k2",
	}, got.Models)
}

// 漂移不在这里被关掉——ApplyAck 的既有路径会把它标成 superseded。
// 抢先标记等于撒谎：那时机器上还没变。
func TestRebindLeavesDriftOpen(t *testing.T) {
	r := newRig(t)
	zhipu := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	kimi := r.seedProvider(t, "Kimi 官方",
		"https://api.moonshot.cn/anthropic", "sk-kimi-abcdefghij")
	setID := r.seedBoundSet(t, zhipu, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	})
	eventID := r.seedBindingDriftIn(t, setID, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic", "")

	_, err := r.svc.Rebind(eventID, kimi)
	require.NoError(t, err)

	rec, err := r.app.FindRecordById("drift_events", eventID)
	require.NoError(t, err)
	require.Equal(t, "open", rec.GetString("state"))
}

func TestRebindRefusesNonBindingDrift(t *testing.T) {
	r := newRig(t)
	eventID := r.seedNormalDrift(t, "CLAUDE.md")
	_, err := r.svc.Rebind(eventID, "p1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "不是绑定漂移")
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/drift/ -run Rebind -v
```

Expected: FAIL，未定义。

- [ ] **Step 3: 实现**

`hub/internal/drift/binding.go` 追加：

```go
// Rebind 把漂移所属配置集的绑定改成指定 Provider，并发布一条新 Revision
// （spec §2.2 / §6.3 第一档）。
//
// 发布的是 **head 的清单**而不是草稿：改绑定不该顺手把用户草稿里没做完的
// 编辑一起推上去。草稿绑定同步改掉，否则下一次发布会把绑定又切回去。
//
// **不在这里关掉这条漂移**：新 Revision 下发 → agent apply → ApplyAck →
// resolveDriftOnApply 会把它标成 superseded（M1 spec §7.7 的既有路径）。
// 抢先标记等于撒谎——那时机器上还没变。
func (s *Service) Rebind(eventID, providerID string) (*core.Record, error) {
	rec, err := s.d.App.FindRecordById("drift_events", eventID)
	if err != nil {
		return nil, fmt.Errorf("drift: 漂移 %s 不存在: %w", eventID, err)
	}
	if !rec.GetBool("binding_drift") {
		return nil, fmt.Errorf("drift: %s 不是绑定漂移", rec.GetString("path"))
	}
	setID := rec.GetString("config_set")
	if setID == "" {
		return nil, fmt.Errorf("drift: %s 不属于任何配置集", rec.GetString("path"))
	}

	prov, err := s.d.Providers.Get(providerID)
	if err != nil {
		return nil, err
	}
	// 模型槽取新 Provider 的 defaults：换了家供应商，旧供应商的模型 id
	// 在新 endpoint 上没有意义。
	var defaults providers.ModelSlots
	_ = prov.UnmarshalJSONField("defaults", &defaults)
	binding := &providers.Binding{Provider: providerID, Models: defaults}

	head, err := s.d.Revs.Head(setID)
	if err != nil {
		return nil, err
	}
	files, err := s.d.Revs.Files(head.Id)
	if err != nil {
		return nil, err
	}
	if err := s.d.Sets.SetDraftBinding(setID, binding); err != nil {
		return nil, err
	}
	rev, err := s.d.Revs.PublishFiles(setID, files, binding,
		"从收件箱改绑到「"+prov.GetString("name")+"」", "publish")
	if err != nil {
		return nil, err
	}
	if err := s.d.Events.Write(events.KindBindingChanged, rec.GetString("machine"),
		map[string]any{
			"config_set": setID, "provider": providerID,
			"revision": rev.Id, "from_drift": eventID,
		}); err != nil {
		s.log.Warn("写 binding.changed 事件失败", "error", err)
	}
	if s.d.Sync != nil {
		if err := s.d.Sync.NotifyConfigSet(setID, rev.Id, protocol.ReasonPublished); err != nil {
			return nil, err
		}
	}
	return rev, nil
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/... -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/drift/ && git commit -m "feat(hub): 从收件箱一键改绑并发布新版本"
```

---

### Task 3: `hub` 公开入口与两个端点

**Files:**
- Modify: `hub/providers.go`
- Modify: `hub/internal/routes/config.go`（`Admin` 追加）
- Modify: `hub/internal/routes/providers.go`
- Modify: `hub/internal/routes/routes.go`
- Test: `hub/internal/routes/providers_test.go`（追加）

**Interfaces:**
- Consumes: `drift.MatchBinding` / `Rebind` / `ExtractKey`、`providers.Store.Create`
- Produces: `Hub.MatchBindingDrift` / `RebindFromDrift` / `CreateProviderFromDrift`；`GET /drift/{id}/binding-match`、`POST /drift/{id}/rebind`；`POST /providers` 支持 `from_drift`

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/routes/providers_test.go`：

```go
func TestBindingDriftRoutesRequireSuperuser(t *testing.T) {
	srv := newRouterServer(t, routes.Deps{Admin: &fakeAdmin{}})
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/orciny/drift/e1/binding-match", ""},
		{"POST", "/api/orciny/drift/e1/rebind", `{"provider":"p1"}`},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "%s %s", c.method, c.path)
	}
}

// 建服务配置时带 from_drift → 先抽 key 成凭据，再用它建。
func TestCreateProviderWithFromDrift(t *testing.T) {
	admin := &fakeAdmin{}
	srv := newRouterServer(t, routes.Deps{Admin: admin})
	body := `{"name":"Kimi 官方","preset":"kimi",` +
		`"base_url":"https://api.moonshot.cn/anthropic",` +
		`"auth_field":"ANTHROPIC_AUTH_TOKEN",` +
		`"from_drift":{"event":"e1","location":"env.ANTHROPIC_AUTH_TOKEN","name":"kimi_key"}}`

	rec := doSuperuser(t, srv, "POST", "/api/orciny/providers", body) // 该文件既有的带鉴权请求辅助
	require.Equal(t, http.StatusOK, rec.Code)

	require.Equal(t, "e1", admin.fromDriftEvent)
	require.Equal(t, "env.ANTHROPIC_AUTH_TOKEN", admin.fromDriftLocation)
	require.Equal(t, "kimi_key", admin.fromDriftName)
	require.Equal(t, "Kimi 官方", admin.fromDriftInput.Name)
	require.False(t, admin.plainCreateCalled, "带 from_drift 时不走普通的 CreateProvider")
}

func TestRebindRequiresProvider(t *testing.T) {
	srv := newRouterServer(t, routes.Deps{Admin: &fakeAdmin{}})
	rec := doSuperuser(t, srv, "POST", "/api/orciny/drift/e1/rebind", `{}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/routes/ -run 'BindingDrift|FromDrift|Rebind' -v
```

Expected: FAIL，404。

- [ ] **Step 3: 实现**

`hub/providers.go` 追加：

```go
// MatchBindingDrift 对一条绑定漂移做反查三档（spec §6.3）。
func (h *Hub) MatchBindingDrift(eventID string) (drift.BindingMatch, error) {
	return h.drift.MatchBinding(eventID)
}

// RebindFromDrift 把漂移所属配置集的绑定改成指定 Provider 并发布新版本，
// 返回新 revision id。
func (h *Hub) RebindFromDrift(eventID, providerID string) (string, error) {
	rev, err := h.drift.Rebind(eventID, providerID)
	if err != nil {
		return "", err
	}
	return rev.Id, nil
}

// CreateProviderFromDrift 先把漂移内容里 location 处的值抽成名为 credName
// 的凭据，再用它建服务配置（spec §6.3 第二档）。in.Credential 由本方法填。
//
// 两步之间失败时凭据会留下来——这是有意的：让用户在凭据页看得见它，
// 好过悄悄回滚掉一把他刚在机器上生成的 key。
func (h *Hub) CreateProviderFromDrift(
	eventID, location, credName string, in providers.Input,
) (string, error) {
	credID, err := h.drift.ExtractKey(eventID, location, credName)
	if err != nil {
		return "", err
	}
	in.Credential = credID
	return h.CreateProvider(in)
}
```

`routes.Admin` 追加：

```go
	MatchBindingDrift(eventID string) (drift.BindingMatch, error)
	RebindFromDrift(eventID, providerID string) (string, error)
	CreateProviderFromDrift(eventID, location, credName string, in providers.Input) (string, error)
```

`hub/internal/routes/providers.go` 的 `providerBody` 追加：

```go
	// FromDrift 非空时，先把漂移内容里的 key 抽成凭据再建（spec §6.3 第二档）。
	// 此时 Credential 字段被忽略。
	FromDrift *struct {
		Event    string `json:"event"`
		Location string `json:"location"`
		Name     string `json:"name"`
	} `json:"from_drift"`
```

`createProvider` 分派：

```go
	if req.FromDrift != nil {
		id, err := d.Admin.CreateProviderFromDrift(
			req.FromDrift.Event, req.FromDrift.Location, req.FromDrift.Name, req.input())
		if err != nil {
			return mapErr(e, err)
		}
		return e.JSON(http.StatusOK, map[string]any{"id": id})
	}
```

追加两个处理函数：

```go
func (d Deps) bindingMatch(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	m, err := d.Admin.MatchBindingDrift(e.Request.PathValue("id"))
	if err != nil {
		return mapErr(e, err)
	}
	return e.JSON(http.StatusOK, m)
}

func (d Deps) rebindDrift(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req struct {
		Provider string `json:"provider"`
	}
	if err := e.BindBody(&req); err != nil || req.Provider == "" {
		return e.BadRequestError("需要 provider", nil)
	}
	revID, err := d.Admin.RebindFromDrift(e.Request.PathValue("id"), req.Provider)
	if err != nil {
		return mapErr(e, err)
	}
	return e.JSON(http.StatusOK, map[string]any{"revision": revID})
}
```

`routes.go` 追加：

```go
	g.GET("/drift/{id}/binding-match", d.bindingMatch).Bind(su)
	g.POST("/drift/{id}/rebind", d.rebindDrift).Bind(su)
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/... -v
```

Expected: 全部 PASS。既有的 `fakeAdmin` 补三个方法。

- [ ] **Step 5: 提交**

```bash
git add hub/ && git commit -m "feat(hub): 绑定漂移反查与改绑的 HTTP 端点"
```

---

### Task 4: 前端 —— 卡片上的两档动作

**Files:**
- Create: `hub/internal/site/src/components/BindingDriftActions.tsx`
- Create: `hub/internal/site/src/components/BindingDriftActions.test.tsx`
- Modify: `hub/internal/site/src/components/DriftCard.tsx`
- Modify: `hub/internal/site/src/pages/Inbox.tsx`
- Modify: `hub/internal/site/src/lib/api.ts`
- Modify: `hub/internal/site/src/types/collections.ts`

**Interfaces:**
- Consumes: `GET /drift/{id}/binding-match`、`POST /drift/{id}/rebind`、`POST /providers`（带 `from_drift`）
- Produces: `BindingMatchResult` 类型；`matchBindingDrift` / `rebindDrift` / `createProviderFromDrift`

**措辞纪律**（spec §6.4 / §13）：`match.exact === false` 时措辞降级为「**可能是**」，且动作仍需用户确认，不自动执行。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/site/src/components/BindingDriftActions.test.tsx`：

```tsx
const base = { url: 'https://api.moonshot.cn/anthropic' }

it('第一档：命中已有 Provider，给「改成它」', async () => {
  const onRebind = vi.fn()
  render(
    <BindingDriftActions
      match={{ ...base, match: { kind: 'provider', exact: true,
        provider_id: 'p1', provider_name: 'Kimi 官方' } }}
      onRebind={onRebind}
      onCreateProvider={vi.fn()}
    />,
  )
  expect(screen.getByText(/Kimi 官方/)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /改成它|改绑/ }))
  expect(onRebind).toHaveBeenCalledWith('p1')
})

it('host 匹配时措辞降级为「可能是」', () => {
  render(
    <BindingDriftActions
      match={{ ...base, match: { kind: 'provider', exact: false,
        provider_id: 'p1', provider_name: 'Kimi 官方' } }}
      onRebind={vi.fn()}
      onCreateProvider={vi.fn()}
    />,
  )
  expect(screen.getByText(/可能是/)).toBeInTheDocument()
})

it('第二档：命中内置预设，给「新建服务配置」并带上 key 位置', async () => {
  const onCreate = vi.fn()
  render(
    <BindingDriftActions
      match={{ ...base, match: { kind: 'preset', exact: true,
        preset_id: 'kimi', preset_name: 'Kimi (Moonshot)' },
        key_location: 'env.ANTHROPIC_AUTH_TOKEN', key_masked: 'sk-k…1234' }}
      onRebind={vi.fn()}
      onCreateProvider={onCreate}
    />,
  )
  expect(screen.getByText(/Kimi \(Moonshot\)/)).toBeInTheDocument()
  expect(screen.getByText(/sk-k…1234/)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /新建服务配置/ }))
  expect(onCreate).toHaveBeenCalledWith('kimi', 'env.ANTHROPIC_AUTH_TOKEN')
})

it('第三档：都不命中，只写明无法识别，不给动作按钮', () => {
  render(
    <BindingDriftActions
      match={{ url: 'https://某中转.test/v1', match: { kind: 'none', exact: false } }}
      onRebind={vi.fn()}
      onCreateProvider={vi.fn()}
    />,
  )
  expect(screen.getByText(/无法识别/)).toBeInTheDocument()
  expect(screen.queryByRole('button')).toBeNull()
})

it('反查还没回来时不显示任何动作', () => {
  render(<BindingDriftActions match={undefined} onRebind={vi.fn()} onCreateProvider={vi.fn()} />)
  expect(screen.queryByRole('button')).toBeNull()
})
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
cd hub/internal/site && npx vitest run src/components/BindingDriftActions.test.tsx
```

Expected: FAIL，模块不存在。

- [ ] **Step 3: 实现**

`types/collections.ts` 追加：

```ts
export interface ProviderMatch {
  kind: 'provider' | 'preset' | 'none'
  /** false = 只有 host 对得上，措辞降级为「可能是」（M1.5 spec §6.4） */
  exact: boolean
  provider_id?: string
  provider_name?: string
  preset_id?: string
  preset_name?: string
}

export interface BindingMatchResult {
  url: string
  match: ProviderMatch
  /** 机器上手写的那把 key 在漂移内容里的位置 */
  key_location?: string
  key_masked?: string
}
```

`lib/api.ts` 追加：

```ts
export function matchBindingDrift(eventId: string) {
  return getJSON<BindingMatchResult>(`/api/orciny/drift/${eventId}/binding-match`)
}

export function rebindDrift(eventId: string, provider: string) {
  return postJSON<{ revision: string }>(`/api/orciny/drift/${eventId}/rebind`, { provider })
}

export function createProviderFromDrift(
  body: ProviderBody & { from_drift: { event: string; location: string; name: string } },
) {
  return postJSON<{ id: string }>('/api/orciny/providers', body)
}
```

Create `BindingDriftActions.tsx`：

```tsx
export function BindingDriftActions({
  match,
  onRebind,
  onCreateProvider,
}: {
  /** undefined = 反查还没回来 */
  match?: BindingMatchResult
  onRebind: (providerId: string) => void
  onCreateProvider: (presetId: string, keyLocation: string) => void
})
```

三档渲染：

- `match === undefined` → `null`
- `match.match.kind === 'provider'` → 一句话 +「改成它」按钮：
  - `exact` → `<Trans>这台机器改用了「{name}」。把配置集的绑定改成它？</Trans>`
  - `!exact` → `<Trans>这台机器用的地址**可能是**「{name}」。把配置集的绑定改成它？</Trans>`
  - 按钮 `onClick={() => onRebind(match.match.provider_id!)}`
- `match.match.kind === 'preset'` → `<Trans>识别为「{presetName}」。新建服务配置？</Trans>`；有 `key_masked` 时补一句 `<Trans>机器上那把 key（{key_masked}）会被抽成凭据。</Trans>`；按钮 `onClick={() => onCreateProvider(match.match.preset_id!, match.key_location ?? '')}`
- `kind === 'none'` → 子计划 06 已有的兜底文案（把它从 `DriftCard` 挪进来，保持一处）

`DriftCard.tsx`：新增可选 prop `bindingMatch?: BindingMatchResult` 与两个回调，把子计划 06 的兜底段落替换成 `<BindingDriftActions>`。保留「这台机器改用了别的 API 地址：<url>」那一行——三档都要显示它。

`Inbox.tsx`：

```tsx
  // 绑定漂移一般只有一两条，进页面时一次性把反查结果取回来。
  const [matches, setMatches] = useState<Record<string, BindingMatchResult>>({})
  useEffect(() => {
    const ids = drifts.filter((d) => d.binding_drift && d.state === 'open').map((d) => d.id)
    let cancelled = false
    void Promise.all(ids.map((id) => matchBindingDrift(id).then((m) => [id, m] as const)))
      .then((pairs) => {
        if (!cancelled) setMatches(Object.fromEntries(pairs))
      })
      .catch(() => { /* 反查失败就退回第三档文案 */ })
    return () => { cancelled = true }
  }, [drifts])
```

`onRebind` → `await rebindDrift(id, providerId)` → 刷新收件箱与配置集列表。
`onCreateProvider(presetId, keyLocation)` → 打开子计划 05 的 `<ProviderDialog>`，用 `presets.find(p => p.id === presetId)` 预填，并把 `{ event, location: keyLocation }` 传下去让它走 `createProviderFromDrift`；建成后自动调 `rebindDrift(eventId, newProviderId)`——**这就是 spec §6.3 说的「建成后回到上一行的动作」**。

- [ ] **Step 4: 运行测试确认通过**

```bash
cd hub/internal/site && npm test && npx tsc --noEmit
```

Expected: 全部 PASS。

- [ ] **Step 5: 抽取文案、复原构建脏文件、提交**

```bash
cd hub/internal/site && npm run extract && npm run compile && npm run build
```

补 `en.po` 新条目，然后：

```bash
git checkout -- hub/internal/site/dist/index.html
git add hub/internal/site/src/ && git commit -m "feat(web): 绑定漂移的反查两档动作"
```

---

### Task 5: 收尾 —— 全量回归与验收记录

**Files:**
- Create: `docs/superpowers/plans/2026-08-21-provider-binding/acceptance.md`
- Modify: `docs/PRODUCT-DESIGN.md`（回填，见下）

- [ ] **Step 1: 全量测试**

```bash
go test -tags=testing ./...
```

```bash
cd hub/internal/site && npm test
```

```bash
make lint
```

- [ ] **Step 2: 量体积**

```bash
make build && ls -lh dist/orciny dist/orciny-agent
```

Expected: agent < 30 MB、hub < 64 MB（与 M1 验收同口径）。

- [ ] **Step 3: 写验收记录**

照 [M1 的 acceptance.md](../2026-07-31-m1-config-loop/acceptance.md) 的格式，逐条核对 00-overview 的十五条 DoD，每条写「结论 + 依据」。需要多机真机才能验的条目标为**待真机验收**并写清对应的自动化覆盖，不要含糊过去。

- [ ] **Step 4: 回填产品设计文档**

本 spec 引入了产品设计文档 §4 未描述的实体（`providers`）与页面（AI 服务）。按 spec §14 回填：

- §4.2 增补「服务绑定」小节
- §4.8 信息架构表增加「AI 服务」页，里程碑标 M1.5
- §6 数据模型表增加 `providers` 行
- §4.7 增补一句：订阅采集实例与服务配置共享凭据、可选关联（M1.5 spec §9）

- [ ] **Step 5: 提交**

```bash
git add docs/ && git commit -m "docs: M1.5 验收记录与产品设计文档回填"
```

---

## 本子计划完成后的状态

- 反查三档全部就位：命中已有 Provider 一键改绑；命中预设一键新建 + 抽 key + 改绑；都不命中给兜底文案。
- 「我在某台机器上试了个新中转，好用」→ 一键变成全机队的决定。
- M1.5 全部交付完成，验收记录已写。
