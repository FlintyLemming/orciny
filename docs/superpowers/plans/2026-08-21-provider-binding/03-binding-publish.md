# 子计划 03 · 绑定、发布与校验

**前置**：01、02
**读这份之前先读** [00-overview.md](00-overview.md) 的 Global Constraints 与全局接口契约。

**交付物**：`refs.provider_keys` 的扫描与落库；`draft_binding` 的读写；发布时冻结 `binding` 并同步 `head_provider`；spec §7 的三条发布校验与 `auth_field` 一键修复；`importer` 正则认 `provider`。

**核心不变量（每个任务都要能指回它）**：**引用**进 Revision，**值**不进（spec §2.2）。换绑定产生新 Revision；改 Provider 内部不产生。

---

### Task 1: `refs.provider_keys`

**Files:**
- Modify: `hub/internal/configsets/service.go`（`Refs` 结构、`collectRefs`）
- Modify: `hub/internal/configsets/validate.go`（未定义引用检查跳过 provider）
- Modify: `hub/internal/revisions/service.go`（`refsOf`）
- Test: `hub/internal/configsets/service_test.go`（追加）
- Test: `hub/internal/revisions/service_test.go`（追加）

**Interfaces:**
- Consumes: `protocol.RefProvider`、`protocol.Refs`
- Produces: `configsets.Refs{Creds, Vars, ProviderKeys}`；`revisions.refs` 与 `config_sets.draft_refs` 里多一个 `provider_keys` 数组

**为什么 `provider_keys` 是必须的而不是可选优化**（spec §2.2）：§5.1 要「只下发被引用到的键」，hub 必须在**不读 blob 内容**的前提下知道 `{{provider.model_opus}}` 有没有被用到——这正是 M1 立 `refs` 字段的初衷。

注意它与 `head_provider` 是两件事：`provider_keys` 记的是**哪几个内置名被引用**，`head_provider` 记的是**绑定指向哪条 Provider**。前者服务于下发裁剪，后者服务于重注入反查。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/configsets/service_test.go`（用该文件既有的建 Service 方式）：

```go
func TestDraftRefsCollectsProviderKeys(t *testing.T) {
	s, _ := newService(t) // 该文件既有的构造函数
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)

	_, err = s.SetDraftFile(set.Id, ".claude/settings.json", []byte(
		`{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}",`+
			`"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}",`+
			`"ANTHROPIC_MODEL":"{{provider.model}}","X":"{{cred.k}}"}}`), 0o600, nil)
	require.NoError(t, err)

	var refs configsets.Refs
	rec, err := s.App().FindRecordById("config_sets", set.Id) // 或用该文件既有的取记录方式
	require.NoError(t, err)
	require.NoError(t, rec.UnmarshalJSONField("draft_refs", &refs))

	require.Equal(t, []string{"auth_token", "base_url", "model"}, refs.ProviderKeys,
		"去重后按名字升序")
	require.Equal(t, []string{"k"}, refs.Creds)
}

// 转义的 {{{{provider.x}}}} 是字面文本，不算引用（spec §11）。
func TestDraftRefsIgnoresEscapedProviderRefs(t *testing.T) {
	s, _ := newService(t)
	set, err := s.Create("文档配置", "")
	require.NoError(t, err)

	_, err = s.SetDraftFile(set.Id, "CLAUDE.md",
		[]byte("写法是 {{{{provider.base_url}}"), 0o644, nil)
	require.NoError(t, err)

	var refs configsets.Refs
	rec, err := s.App().FindRecordById("config_sets", set.Id)
	require.NoError(t, err)
	require.NoError(t, rec.UnmarshalJSONField("draft_refs", &refs))
	require.Empty(t, refs.ProviderKeys)
}

// provider.* 的「已定义」由三条绑定校验判定，不走未定义引用那条路——
// 否则每个绑定了服务的配置集都会报一堆假的未定义引用。
func TestValidateDoesNotReportProviderAsUndefinedRef(t *testing.T) {
	s, _ := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, ".claude/settings.json",
		[]byte(`{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`), 0o600, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id, map[string]bool{})
	require.NoError(t, err)
	for _, p := range problems {
		require.NotEqual(t, configsets.ProblemUndefinedRef, p.Kind,
			"provider.* 不该被当成未定义引用：%+v", p)
	}
}
```

追加到 `hub/internal/revisions/service_test.go`：

```go
func TestPublishFreezesProviderKeys(t *testing.T) {
	sets, revs, _ := newServices(t) // 该文件既有的构造函数
	set, err := sets.Create("主力配置", "")
	require.NoError(t, err)
	_, err = sets.SetDraftFile(set.Id, ".claude/settings.json", []byte(
		`{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}",`+
			`"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}",`+
			`"ANTHROPIC_MODEL":"{{provider.model}}"}}`), 0o600, nil)
	require.NoError(t, err)

	rev, err := revs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)

	var refs configsets.Refs
	require.NoError(t, rev.UnmarshalJSONField("refs", &refs))
	require.Equal(t, []string{"auth_token", "base_url", "model"}, refs.ProviderKeys)
	require.Empty(t, refs.Creds)
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/configsets/ ./hub/internal/revisions/ -run 'ProviderKeys|UndefinedRef' -v
```

Expected: 编译失败，`Refs` 没有 `ProviderKeys` 字段。

- [ ] **Step 3: 实现**

`hub/internal/configsets/service.go`：

```go
// Refs 是 config_sets.draft_refs 与 revisions.refs 的形状。
//
// ProviderKeys 记的是**哪几个 {{provider.*}} 内置名被引用**（形如
// ["auth_token","base_url"]），不是绑定指向哪条 Provider——后者在
// head_provider / binding 里。下发时靠它裁剪，不读 blob 内容（spec §2.2）。
type Refs struct {
	Creds        []string `json:"creds"`
	Vars         []string `json:"vars"`
	ProviderKeys []string `json:"provider_keys"`
}
```

`Create` 的初始值：

```go
	r.Set("draft_refs", Refs{Creds: []string{}, Vars: []string{}, ProviderKeys: []string{}})
```

`collectRefs`：

```go
	creds := map[string]bool{}
	vars := map[string]bool{}
	providerKeys := map[string]bool{}
	// …
		for _, ref := range rs {
			switch ref.Kind {
			case protocol.RefCred:
				creds[ref.Name] = true
			case protocol.RefVar:
				vars[ref.Name] = true
			case protocol.RefProvider:
				providerKeys[ref.Name] = true
			}
			// machine.* 是内置值，不进引用集合。
		}
	// …
	return Refs{
		Creds:        sortedKeys(creds),
		Vars:         sortedKeys(vars),
		ProviderKeys: sortedKeys(providerKeys),
	}, nil
```

`hub/internal/configsets/validate.go` 的未定义引用循环：

```go
		for _, ref := range refs {
			// machine.* 是内置值；provider.* 的「已定义」由三条绑定校验
			// 判定（spec §7），不走这条路——否则每个绑了服务的配置集都会
			// 报一堆假的未定义引用。
			if ref.Kind == protocol.RefMachine || ref.Kind == protocol.RefProvider {
				continue
			}
			if !known[ref.String()] {
				problems = append(problems, Problem{Path: f.Path, Kind: ProblemUndefinedRef,
					Detail: "未定义的引用 " + ref.String()})
			}
		}
```

> 注意：`Problem` 的字面量构造从位置式改成字段式——Task 4 会给它加两个字段，位置式会当场编译失败。本任务顺手全部改掉。

`hub/internal/revisions/service.go` 的 `refsOf` 做同样的三分支处理并返回三个数组。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/... -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/configsets/ hub/internal/revisions/ && git commit -m "feat(hub): refs 扫描并冻结 provider_keys"
```

---

### Task 2: 草稿绑定的读写

**Files:**
- Create: `hub/internal/configsets/binding.go`
- Create: `hub/internal/configsets/binding_test.go`
- Modify: `hub/internal/configsets/service.go`（`Service` 持一个 `*providers.Store`）

**Interfaces:**
- Consumes: `providers.Binding`、`providers.Store`
- Produces: `DraftBinding(setID) (*providers.Binding, error)`、`SetDraftBinding(setID string, b *providers.Binding) error`

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/configsets/binding_test.go`：

```go
package configsets_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

func TestDraftBindingRoundTrip(t *testing.T) {
	s, app := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)

	// 未绑定：返回 (nil, nil)，不是错误。
	b, err := s.DraftBinding(set.Id)
	require.NoError(t, err)
	require.Nil(t, b)

	provID := seedProvider(t, app, "智谱 GLM · 个人")
	want := &providers.Binding{
		Provider: provID,
		Models: providers.ModelSlots{
			Main: "glm-5.1", Opus: "glm-5.1", Sonnet: "glm-5.1", Haiku: "glm-5.1",
		},
	}
	require.NoError(t, s.SetDraftBinding(set.Id, want))

	got, err := s.DraftBinding(set.Id)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestSetDraftBindingNilClears(t *testing.T) {
	s, app := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	require.NoError(t, s.SetDraftBinding(set.Id,
		&providers.Binding{Provider: seedProvider(t, app, "智谱 GLM · 个人")}))

	require.NoError(t, s.SetDraftBinding(set.Id, nil))
	got, err := s.DraftBinding(set.Id)
	require.NoError(t, err)
	require.Nil(t, got)
}

// 绑一条不存在的 Provider 必须当场被拒：让它进草稿，用户会在发布时
// 才发现，而那时错误信息离操作现场已经很远。
func TestSetDraftBindingRejectsUnknownProvider(t *testing.T) {
	s, _ := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	err = s.SetDraftBinding(set.Id, &providers.Binding{Provider: "不存在"})
	require.ErrorIs(t, err, providers.ErrNotFound)
}

// 半填的四槽是配置错误（spec §2.3）：只钉主模型会让 Claude Code
// 拿 claude-haiku-* 去打人家的 endpoint。
func TestSetDraftBindingRejectsHalfFilledSlots(t *testing.T) {
	s, app := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	err = s.SetDraftBinding(set.Id, &providers.Binding{
		Provider: seedProvider(t, app, "智谱 GLM · 个人"),
		Models:   providers.ModelSlots{Main: "glm-5.1"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "全空")
}
```

`seedProvider` 建一条最小可用 Provider（含一条凭据），放在 `binding_test.go` 里：

```go
func seedProvider(t *testing.T, app core.App, name string) string {
	t.Helper()
	creds, err := app.FindCollectionByNameOrId("credentials")
	require.NoError(t, err)
	cred := core.NewRecord(creds)
	cred.Set("name", "zhipu_key")
	cred.Set("cipher_value", "x")
	cred.Set("last4", "1234")
	require.NoError(t, app.Save(cred))

	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	p := core.NewRecord(c)
	p.Set("name", name)
	p.Set("base_url", "https://open.bigmodel.cn/api/anthropic")
	p.Set("auth_field", providers.AuthToken)
	p.Set("credential", cred.Id)
	require.NoError(t, app.Save(p))
	return p.Id
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/configsets/ -run 'Binding' -v
```

Expected: FAIL，`DraftBinding` 未定义。

- [ ] **Step 3: 实现**

`hub/internal/configsets/service.go` 的 `Service` 与构造函数：

```go
type Service struct {
	app   core.App
	blobs *blobs.Store
	ev    *events.Writer
	provs *providers.Store
}

func NewService(app core.App, b *blobs.Store, ev *events.Writer) *Service {
	return &Service{app: app, blobs: b, ev: ev, provs: providers.NewStore(app, ev)}
}
```

> 构造函数签名不变，内部自建 `providers.Store`——与 `revisions.NewService` 内部自建 `configsets.Service` 是同一个手法，装配点不用跟着改。

Create `hub/internal/configsets/binding.go`：

```go
package configsets

import (
	"fmt"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

// DraftBinding 读草稿绑定。未绑定返回 (nil, nil)——「没绑」不是错误。
func (s *Service) DraftBinding(setID string) (*providers.Binding, error) {
	r, err := s.record(setID)
	if err != nil {
		return nil, err
	}
	return decodeBinding(r, "draft_binding")
}

// SetDraftBinding 设置或清空草稿绑定。b 为 nil 即解绑。
//
// 改绑定走正常的草稿/发布流程 → 新 Revision（spec §2.2 / §8.2）：
// 绑定决定了这个版本渲染出什么，历史版本必须能解释自己。
func (s *Service) SetDraftBinding(setID string, b *providers.Binding) error {
	r, err := s.record(setID)
	if err != nil {
		return err
	}
	if b == nil {
		r.Set("draft_binding", nil)
	} else {
		if _, err := s.provs.Get(b.Provider); err != nil {
			return err
		}
		if !b.Models.Empty() && !b.Models.Full() {
			return fmt.Errorf(
				"configsets: 四个模型槽必须要么全空（透传）要么全满，当前 %+v", b.Models)
		}
		r.Set("draft_binding", b)
	}
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("configsets: 保存草稿绑定: %w", err)
	}
	provider := ""
	if b != nil {
		provider = b.Provider
	}
	if err := s.ev.Write(events.KindBindingChanged, "", map[string]any{
		"config_set": setID, "provider": provider,
	}); err != nil {
		s.app.Logger().Warn("写 binding.changed 事件失败", "error", err)
	}
	return nil
}

// decodeBinding 从记录的 JSON 字段解一条绑定。空字段返回 (nil, nil)。
func decodeBinding(r interface {
	UnmarshalJSONField(string, any) error
}, field string) (*providers.Binding, error) {
	var b providers.Binding
	if err := r.UnmarshalJSONField(field, &b); err != nil {
		// 空 JSON 字段解不动是正常情形，按未绑定处理。
		return nil, nil
	}
	if b.Provider == "" {
		return nil, nil
	}
	return &b, nil
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/... -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/configsets/ && git commit -m "feat(hub): 配置集草稿绑定的读写"
```

---

### Task 3: 发布冻结绑定并同步 `head_provider`

**Files:**
- Modify: `hub/internal/revisions/service.go`（`PublishFiles` 签名、`Publish`、`Rollback`、新增 `BindingOf`）
- Modify: `hub/internal/drift/adopt.go`（`PublishFiles` 调用点）
- Test: `hub/internal/revisions/service_test.go`（追加）

**Interfaces:**
- Consumes: `providers.Binding`、`configsets.Service.DraftBinding`
- Produces: `PublishFiles(setID, files, binding, note, source)`、`BindingOf(revID) (*providers.Binding, error)`

**`head_provider` 的唯一写入点就在这里。** 若实现中出现第二个写入点，停下来重看设计（spec §13）。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/revisions/service_test.go`：

```go
func TestPublishFreezesBindingAndSyncsHeadProvider(t *testing.T) {
	// 用该文件既有的方式建 Service + 配置集；provID 用 seedProvider 造。
	require.NoError(t, sets.SetDraftBinding(setID, &providers.Binding{
		Provider: provID,
		Models: providers.ModelSlots{
			Main: "glm-5.1", Opus: "glm-5.1", Sonnet: "glm-5.1", Haiku: "glm-5.1",
		},
	}))

	rev, err := revs.Publish(setID, "v1", "publish")
	require.NoError(t, err)

	got, err := revs.BindingOf(rev.Id)
	require.NoError(t, err)
	require.Equal(t, provID, got.Provider)
	require.Equal(t, "glm-5.1", got.Models.Haiku)

	set, err := app.FindRecordById("config_sets", setID)
	require.NoError(t, err)
	require.Equal(t, provID, set.GetString("head_provider"),
		"head_provider 必须与 head 同写——重注入的反查链靠它")
}

func TestPublishWithoutBindingClearsHeadProvider(t *testing.T) {
	// 先绑定发布 v1，再解绑发布 v2。
	require.NoError(t, sets.SetDraftBinding(setID, nil))
	_, err := revs.Publish(setID, "v2", "publish")
	require.NoError(t, err)

	set, err := app.FindRecordById("config_sets", setID)
	require.NoError(t, err)
	require.Empty(t, set.GetString("head_provider"))
}

// 回滚回到旧版本时，绑定也要跟着回去——否则 v4 会用 v3 的绑定
// 渲染 v1 的文件，谁也解释不了这个版本。
func TestRollbackRestoresBinding(t *testing.T) {
	// v1 绑 A，v2 绑 B，回滚到 v1 生成 v3。
	rev3, err := revs.Rollback(setID, rev1.Id)
	require.NoError(t, err)

	got, err := revs.BindingOf(rev3.Id)
	require.NoError(t, err)
	require.Equal(t, provA, got.Provider)

	set, err := app.FindRecordById("config_sets", setID)
	require.NoError(t, err)
	require.Equal(t, provA, set.GetString("head_provider"))

	// 草稿也要拉回去，否则用户下一次发布会把绑定又切回 B。
	draft, err := sets.DraftBinding(setID)
	require.NoError(t, err)
	require.Equal(t, provA, draft.Provider)
}
```

追加到 `hub/internal/drift/adopt_test.go`：

```go
// 收编改的是文件，不是绑定：新 Revision 沿用 head 的绑定。
// 不沿用的话，收编一次就等于顺手解绑，且没有任何提示。
func TestAdoptKeepsHeadBinding(t *testing.T) {
	r := newRig(t)
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedBoundSet(t, provID, map[string]string{"CLAUDE.md": "原文"})

	before, err := r.revs.Head(setID)
	require.NoError(t, err)
	wantBinding, err := r.revs.BindingOf(before.Id)
	require.NoError(t, err)
	require.NotNil(t, wantBinding)

	eventID := r.seedDriftEvent(t, setID, "CLAUDE.md") // 既有辅助
	rev, err := r.svc.Adopt([]string{eventID})
	require.NoError(t, err)

	got, err := r.revs.BindingOf(rev.Id)
	require.NoError(t, err)
	require.Equal(t, wantBinding, got)

	set, err := r.app.FindRecordById("config_sets", setID)
	require.NoError(t, err)
	require.Equal(t, provID, set.GetString("head_provider"))
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/revisions/ -run Binding -v
```

Expected: 编译失败，`BindingOf` 未定义、`PublishFiles` 参数个数不符。

- [ ] **Step 3: 实现**

`hub/internal/revisions/service.go`：

```go
// Publish 把当前草稿冻结成一条新 Revision，连同草稿绑定一起。
func (s *Service) Publish(setID, note, source string) (*core.Record, error) {
	files, err := s.sets.Draft(setID)
	if err != nil {
		return nil, err
	}
	binding, err := s.sets.DraftBinding(setID)
	if err != nil {
		return nil, err
	}
	return s.PublishFiles(setID, files, binding, note, source)
}

// PublishFiles 用给定清单与绑定发布。收编（source=adopt）与回滚走这条。
//
// binding 显式传而不是在函数里读草稿：收编要沿用 head 的绑定、回滚要用
// 旧版本的绑定，两者都不是草稿。绑定必须与文件在同一次写入里冻结——
// 事后补写就会出现「有 head 但 head_provider 还没跟上」的中间态。
func (s *Service) PublishFiles(
	setID string, files []protocol.FileEntry,
	binding *providers.Binding, note, source string,
) (*core.Record, error) {
	// …原有逻辑不变，直到建 revision 记录…
		r.Set("refs", refs)
		if binding != nil {
			r.Set("binding", binding)
		}
	// …

	set.Set("head", rev.Id)
	// head_provider 是冗余字段，唯一写入点就是这里（spec §2.2 / §13）。
	if binding != nil {
		set.Set("head_provider", binding.Provider)
	} else {
		set.Set("head_provider", "")
	}
	if err := s.app.Save(set); err != nil {
		return nil, fmt.Errorf("revisions: 更新 head: %w", err)
	}
```

`Rollback`：

```go
	files, err := s.Files(revisionID)
	if err != nil {
		return nil, err
	}
	binding, err := s.BindingOf(revisionID)
	if err != nil {
		return nil, err
	}

	note := fmt.Sprintf("回滚自 v%d", old.GetInt("seq"))
	rev, err := s.PublishFiles(setID, files, binding, note, "rollback")
	if err != nil {
		return nil, err
	}
	if err := s.sets.SetDraft(setID, files); err != nil {
		return nil, err
	}
	// 草稿绑定也要拉回旧版，否则用户下一次发布会把刚回滚掉的绑定又推上去。
	if err := s.sets.SetDraftBinding(setID, binding); err != nil {
		return nil, err
	}
```

新增：

```go
// BindingOf 读一条 Revision 冻结的绑定。未绑定返回 (nil, nil)。
func (s *Service) BindingOf(revID string) (*providers.Binding, error) {
	r, err := s.app.FindRecordById("revisions", revID)
	if err != nil {
		return nil, fmt.Errorf("revisions: 版本 %s 不存在: %w", revID, err)
	}
	var b providers.Binding
	if err := r.UnmarshalJSONField("binding", &b); err != nil || b.Provider == "" {
		return nil, nil
	}
	return &b, nil
}
```

`hub/internal/drift/adopt.go:135`：

```go
	// 收编改的是文件，不是绑定：沿用 head 的绑定（spec §2.2）。
	var binding *providers.Binding
	if head, err := s.d.Revs.Head(setID); err == nil {
		binding, err = s.d.Revs.BindingOf(head.Id)
		if err != nil {
			return nil, err
		}
	}
	rev, err := s.d.Revs.PublishFiles(setID, merged, binding, note, "adopt")
```

`hub/seed_testing.go` 的 `rs.Publish(set.Id, "v1", "publish")` 签名未变，不用动。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./... -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/revisions/ hub/internal/drift/ && git commit -m "feat(hub): 发布冻结绑定并与 head_provider 同写"
```

---

### Task 4: 三条发布校验与 `auth_field` 一键修复

**Files:**
- Modify: `hub/internal/configsets/validate.go`
- Modify: `hub/internal/configsets/binding.go`（`FixAuthField`）
- Test: `hub/internal/configsets/validate_test.go`（追加）

**Interfaces:**
- Consumes: `providers.Store`、`DraftBinding`
- Produces: `Problem.Warning`、`Problem.Fix`、`Fix`、`FixReplaceEnvKey`、三个新 `Problem*` 常量、`SettingsPath`、`FixAuthField(setID) error`

**三条校验（spec §7，逐条落地）**

| 条件 | 级别 | 文案要点 |
|---|---|---|
| 有 `{{provider.*}}` 引用但 `draft_binding` 为空 | **错误** | 与「未定义凭据引用」同级——引用不可解析，发布出去必然渲染失败 |
| `draft_binding` 非空但全文找不到任何 `{{provider.*}}` | 警告 | 绑了但没用。可能是刚绑完还没插 env 片段，不该阻断发布 |
| 绑定 Provider 的 `auth_field` 与 `settings.json` 里实际的 env 键名不符 | **错误** + 一键修复 | 占位符替的是值，替不了键名（§3.3） |

**为什么不做自动改写**：那会让用户的文件在背后被动过，违背 M1 立下的「宁可不动，不可写坏」。让用户看见那一行、点一下、知道自己改了什么。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/configsets/validate_test.go`：

```go
const settingsWithToken = `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}",` +
	`"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}"}}`

const settingsWithAPIKey = `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}",` +
	`"ANTHROPIC_API_KEY":"{{provider.auth_token}}"}}`

func TestValidateBindingMissingIsError(t *testing.T) {
	s, _ := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, configsets.SettingsPath,
		[]byte(settingsWithToken), 0o600, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id, map[string]bool{})
	require.NoError(t, err)

	var hit *configsets.Problem
	for i := range problems {
		if problems[i].Kind == configsets.ProblemBindingMissing {
			hit = &problems[i]
		}
	}
	require.NotNil(t, hit, "必须报 binding_missing")
	require.False(t, hit.Warning, "这是错误，不是警告")
	require.Equal(t, configsets.SettingsPath, hit.Path)
}

func TestValidateBindingUnusedIsWarning(t *testing.T) {
	s, app := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, "CLAUDE.md", []byte("没有任何占位符"), 0o644, nil)
	require.NoError(t, err)
	require.NoError(t, s.SetDraftBinding(set.Id,
		&providers.Binding{Provider: seedProvider(t, app, "智谱 GLM · 个人")}))

	problems, err := s.Validate(set.Id, map[string]bool{})
	require.NoError(t, err)

	var hit *configsets.Problem
	for i := range problems {
		if problems[i].Kind == configsets.ProblemBindingUnused {
			hit = &problems[i]
		}
	}
	require.NotNil(t, hit)
	require.True(t, hit.Warning, "绑了但没用不该阻断发布")
}

func TestValidateAuthFieldMismatchGivesFix(t *testing.T) {
	s, app := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	// Provider 用 AUTH_TOKEN，文件里写的是 API_KEY。
	require.NoError(t, s.SetDraftBinding(set.Id,
		&providers.Binding{Provider: seedProvider(t, app, "智谱 GLM · 个人")}))
	_, err = s.SetDraftFile(set.Id, configsets.SettingsPath,
		[]byte(settingsWithAPIKey), 0o600, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id, map[string]bool{})
	require.NoError(t, err)

	var hit *configsets.Problem
	for i := range problems {
		if problems[i].Kind == configsets.ProblemAuthFieldMismatch {
			hit = &problems[i]
		}
	}
	require.NotNil(t, hit)
	require.False(t, hit.Warning)
	require.NotNil(t, hit.Fix)
	require.Equal(t, configsets.FixReplaceEnvKey, hit.Fix.Kind)
	require.Equal(t, "ANTHROPIC_API_KEY", hit.Fix.From)
	require.Equal(t, "ANTHROPIC_AUTH_TOKEN", hit.Fix.To)
}

func TestValidateAuthFieldMatchIsQuiet(t *testing.T) {
	s, app := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	require.NoError(t, s.SetDraftBinding(set.Id,
		&providers.Binding{Provider: seedProvider(t, app, "智谱 GLM · 个人")}))
	_, err = s.SetDraftFile(set.Id, configsets.SettingsPath,
		[]byte(settingsWithToken), 0o600, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id, map[string]bool{})
	require.NoError(t, err)
	for _, p := range problems {
		require.NotEqual(t, configsets.ProblemAuthFieldMismatch, p.Kind)
		require.NotEqual(t, configsets.ProblemBindingMissing, p.Kind)
		require.NotEqual(t, configsets.ProblemBindingUnused, p.Kind)
	}
}

func TestFixAuthFieldRewritesDraft(t *testing.T) {
	s, app := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	require.NoError(t, s.SetDraftBinding(set.Id,
		&providers.Binding{Provider: seedProvider(t, app, "智谱 GLM · 个人")}))
	_, err = s.SetDraftFile(set.Id, configsets.SettingsPath,
		[]byte(settingsWithAPIKey), 0o600, nil)
	require.NoError(t, err)

	require.NoError(t, s.FixAuthField(set.Id))

	draft, err := s.Draft(set.Id)
	require.NoError(t, err)
	var content []byte
	for _, f := range draft {
		if f.Path == configsets.SettingsPath {
			content, err = s.Blobs().Get(f.Hash) // 或用该文件既有的读 blob 方式
			require.NoError(t, err)
		}
	}
	require.Contains(t, string(content), `"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}"`)
	require.NotContains(t, string(content), "ANTHROPIC_API_KEY")
	// base_url 那行不能被动到——修复只改一个键名。
	require.Contains(t, string(content), `"ANTHROPIC_BASE_URL":"{{provider.base_url}}"`)

	problems, err := s.Validate(set.Id, map[string]bool{})
	require.NoError(t, err)
	for _, p := range problems {
		require.NotEqual(t, configsets.ProblemAuthFieldMismatch, p.Kind)
	}
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/configsets/ -run 'Binding|AuthField' -v
```

Expected: FAIL，常量未定义。

- [ ] **Step 3: 实现**

`hub/internal/configsets/validate.go`：

```go
// Problem 是一条发布期校验失败。UI 逐条展示；Warning 为真的只展示不阻断。
type Problem struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Detail  string `json:"detail"`
	Warning bool   `json:"warning,omitempty"`
	Fix     *Fix   `json:"fix,omitempty"`
}

// Fix 描述一处「一键修复」。目前只有 auth_field 键名换绑用它。
type Fix struct {
	Kind string `json:"kind"`
	From string `json:"from"`
	To   string `json:"to"`
}

const FixReplaceEnvKey = "replace_env_key"

// 校验类别
const (
	ProblemAlwaysExcluded = "always_excluded"
	ProblemTooLarge       = "too_large"
	ProblemUndefinedRef   = "undefined_ref"
	ProblemBadPath        = "bad_path"
	ProblemMissingBlob    = "missing_blob"

	// M1.5：服务绑定（spec §7）
	ProblemBindingMissing    = "binding_missing"
	ProblemBindingUnused     = "binding_unused"
	ProblemAuthFieldMismatch = "auth_field_mismatch"
)

// SettingsPath 是 settings.json 的受管相对路径。manifest 的根是 HOME。
const SettingsPath = ".claude/settings.json"

// tokenAuthToken 是承载 API key 的那个占位符的字面形态。
const tokenAuthToken = "{{provider.auth_token}}"
```

`Validate` 末尾追加：

```go
	bp, err := s.validateBinding(setID, files)
	if err != nil {
		return nil, err
	}
	return append(problems, bp...), nil
```

`validateBinding`（可放 `binding.go`）：

```go
// validateBinding 是 spec §7 的三条校验。
func (s *Service) validateBinding(setID string, files []protocol.FileEntry) ([]Problem, error) {
	binding, err := s.DraftBinding(setID)
	if err != nil {
		return nil, err
	}

	// 第一遍：谁引用了 provider.*，以及 settings.json 的内容。
	usedIn := ""
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
			if r.Kind == protocol.RefProvider && usedIn == "" {
				usedIn = f.Path
			}
		}
	}

	var out []Problem

	// 1. 引用了但没绑：与「未定义凭据引用」同级——发布出去必然渲染失败。
	if usedIn != "" && binding == nil {
		return append(out, Problem{
			Path: usedIn, Kind: ProblemBindingMissing,
			Detail: "文件里用了 {{provider.*}}，但这个配置集还没有服务绑定。" +
				"到「服务绑定」区选一个 AI 服务配置。",
		}), nil
	}
	if binding == nil {
		return out, nil
	}

	// 2. 绑了但没用：可能是刚绑完还没插 env 片段，不阻断。
	if usedIn == "" {
		out = append(out, Problem{
			Kind: ProblemBindingUnused, Warning: true,
			Detail: "已绑定服务配置，但没有任何文件用到 {{provider.*}}。" +
				"用「插入 env 片段」把它写进 settings.json。",
		})
		return out, nil
	}

	// 3. auth_field 键名不符（spec §3.3）。占位符替的是值，替不了键名。
	prov, err := s.provs.Get(binding.Provider)
	if err != nil {
		return nil, err
	}
	want := prov.GetString("auth_field")
	if got := authFieldMismatch(settings, want); got != "" {
		out = append(out, Problem{
			Path: SettingsPath, Kind: ProblemAuthFieldMismatch,
			Detail: fmt.Sprintf(
				"绑定的服务配置用 %s 鉴权，但 settings.json 里写的是 %s。"+
					"占位符只能替换值，替不了键名——需要把这一行的键名改掉。", want, got),
			Fix: &Fix{Kind: FixReplaceEnvKey, From: got, To: want},
		})
	}
	return out, nil
}

// authFieldMismatch 返回 settings.json 的 env 里实际承载
// {{provider.auth_token}} 的键名；与 want 一致或找不到时返回空串。
//
// 只看值是那个占位符的键：少数平台会同时设两个键（spec §2.3 的统计里
// 有重叠），把用户自己写的另一个键当成错误会很吵。
func authFieldMismatch(settings []byte, want string) string {
	if len(settings) == 0 || !gjson.ValidBytes(settings) {
		return ""
	}
	env := gjson.GetBytes(settings, "env")
	if !env.Exists() {
		return ""
	}
	var others []string
	matched := false
	env.ForEach(func(k, v gjson.Result) bool {
		if v.String() != tokenAuthToken {
			return true
		}
		if k.String() == want {
			matched = true
			return false
		}
		others = append(others, k.String())
		return true
	})
	if matched || len(others) == 0 {
		return ""
	}
	sort.Strings(others) // 多个候选时取值确定，测试才可复现
	return others[0]
}
```

`FixAuthField`（放 `binding.go`）：

```go
// FixAuthField 把 settings.json 的 env 里承载 {{provider.auth_token}} 的
// 键名改成绑定 Provider 的 auth_field（spec §7 第 3 条）。
//
// 改动落在**草稿**上：用户在 diff 里看得见、发布前可撤销。不自动改写——
// 那会让用户的文件在背后被动过，违背 M1 立下的「宁可不动，不可写坏」。
func (s *Service) FixAuthField(setID string) error {
	binding, err := s.DraftBinding(setID)
	if err != nil {
		return err
	}
	if binding == nil {
		return fmt.Errorf("configsets: 没有服务绑定，无从判断该用哪个键名")
	}
	prov, err := s.provs.Get(binding.Provider)
	if err != nil {
		return err
	}
	want := prov.GetString("auth_field")

	files, err := s.Draft(setID)
	if err != nil {
		return err
	}
	var entry *protocol.FileEntry
	for i := range files {
		if files[i].Path == SettingsPath {
			entry = &files[i]
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("configsets: 草稿里没有 %s", SettingsPath)
	}
	content, err := s.blobs.Get(entry.Hash)
	if err != nil {
		return err
	}
	got := authFieldMismatch(content, want)
	if got == "" {
		return nil // 已经是对的，幂等
	}

	out, err := sjson.DeleteBytes(content, "env."+got)
	if err != nil {
		return fmt.Errorf("configsets: 删除 env.%s: %w", got, err)
	}
	out, err = sjson.SetBytes(out, "env."+want, tokenAuthToken)
	if err != nil {
		return fmt.Errorf("configsets: 写入 env.%s: %w", want, err)
	}
	_, err = s.SetDraftFile(setID, SettingsPath, out, entry.Mode, entry.Keys)
	return err
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/... -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/configsets/ && git commit -m "feat(hub): 服务绑定的三条发布校验与 auth_field 一键修复"
```

---

### Task 5: 敏感项检测认得 `provider` 占位符

**Files:**
- Modify: `hub/internal/importer/scan.go`
- Test: `hub/internal/importer/scan_test.go`（追加）

**Interfaces:**
- Consumes: 无
- Produces: 无新导出符号

**为什么**：`placeholderValue` 现在是 `^\{\{(cred|var|machine)\.[A-Za-z0-9_-]+\}\}$`。不改的话，绑了服务的配置集一进导入向导，`{{provider.auth_token}}` 就会被当成明文 key 报出来——一条永远消不掉的假敏感项。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/importer/scan_test.go`：

```go
// 已被抽成占位符的值不该再报敏感项——provider 前缀也一样。
func TestScanIgnoresProviderPlaceholders(t *testing.T) {
	content := []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}",` +
		`"ANTHROPIC_BASE_URL":"{{provider.base_url}}",` +
		`"ANTHROPIC_MODEL":"{{provider.model}}"}}`)
	require.Empty(t, importer.Scan(".claude/settings.json", content))
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/importer/ -run ProviderPlaceholders -v
```

Expected: FAIL——报出 `env.ANTHROPIC_AUTH_TOKEN`。

- [ ] **Step 3: 实现**

`hub/internal/importer/scan.go`：

```go
	// 已被抽成占位符的值不再报——否则 Extract 之后 Findings 会把同一条再吐回来。
	// provider.* 同理：绑了服务的配置集里它到处都是（M1.5 spec §3.1）。
	placeholderValue = regexp.MustCompile(
		`^\{\{(cred|var|machine|provider)\.[A-Za-z0-9_-]+\}\}$`)
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/... -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/importer/ && git commit -m "fix(hub): 敏感项检测跳过 provider 占位符"
```

---

## 本子计划完成后的状态

- 配置集能绑定 Provider、能发布并冻结绑定、`head_provider` 与 `head` 同步。
- 三条发布校验与一键修复可用。
- **还没有任何东西会把 provider 值下发到 agent**——那是子计划 04。
- `go test -tags=testing ./...` 全绿。
