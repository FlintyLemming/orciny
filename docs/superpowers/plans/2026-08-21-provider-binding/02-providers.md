# 子计划 02 · `providers` 包、预设表与凭据引用计数修补

**前置**：无（与 01 可并行）
**读这份之前先读** [00-overview.md](00-overview.md) 的 Global Constraints 与全局接口契约。

**交付物**：迁移 `003_providers.go`；新包 `hub/internal/providers`（预设表、`ModelSlots` / `Binding` 类型、CRUD、base_url 归一化与反查匹配）；`hub/internal/credentials` 的引用计数修补与 `ValueByID`；`hub/internal/events` 的四个新 kind。

**本子计划里唯一会造成数据面损坏的点是 Task 5**：不改引用计数的话，一条正在被 Provider 使用的凭据可以被删掉，然后全机队在下次重注入时拿到空值（spec §5.3 / §13）。它有强制测试。

---

### Task 1: 迁移 003

**Files:**
- Create: `hub/internal/migrations/003_providers.go`
- Test: `hub/internal/migrations/migrations_test.go`（追加，不改已有用例）

**Interfaces:**
- Consumes: `002_configsets.go` 建的 `credentials` / `config_sets` / `revisions` / `drift_events`
- Produces: `providers` collection；`revisions.binding`、`config_sets.draft_binding`、`config_sets.head_provider`、`drift_events.binding_drift`、`drift_events.binding_url`

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/migrations/migrations_test.go`：

```go
func TestProvidersCollectionExists(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	require.Nil(t, c.ListRule, "providers 的 list rule 必须是 nil（仅 superuser）")
	require.Nil(t, c.ViewRule)
	require.Nil(t, c.CreateRule)
	require.Nil(t, c.UpdateRule)
	require.Nil(t, c.DeleteRule)

	for _, f := range []string{
		"name", "preset", "base_url", "auth_field", "credential",
		"models", "defaults", "note", "created", "updated",
	} {
		require.NotNil(t, c.Fields.GetByName(f), "字段 %s 必须存在", f)
	}

	sel, ok := c.Fields.GetByName("auth_field").(*core.SelectField)
	require.True(t, ok)
	require.Equal(t, []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"}, sel.Values)

	rel, ok := c.Fields.GetByName("credential").(*core.RelationField)
	require.True(t, ok)
	require.True(t, rel.Required, "credential 必填")
	require.False(t, rel.CascadeDelete, "删凭据不得连带删掉服务配置")
}

func TestProviderNameIsUnique(t *testing.T) {
	app := newApp(t)
	credID := seedCredential(t, app, "zhipu_key")

	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	mk := func() *core.Record {
		r := core.NewRecord(c)
		r.Set("name", "智谱 GLM · 个人")
		r.Set("base_url", "https://open.bigmodel.cn/api/anthropic")
		r.Set("auth_field", "ANTHROPIC_AUTH_TOKEN")
		r.Set("credential", credID)
		return r
	}
	require.NoError(t, app.Save(mk()))
	require.Error(t, app.Save(mk()), "同名服务配置必须被唯一索引拒绝")
}

func TestBindingFieldsExist(t *testing.T) {
	app := newApp(t)

	revs, err := app.FindCollectionByNameOrId("revisions")
	require.NoError(t, err)
	require.NotNil(t, revs.Fields.GetByName("binding"))

	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	require.NotNil(t, sets.Fields.GetByName("draft_binding"))
	hp, ok := sets.Fields.GetByName("head_provider").(*core.RelationField)
	require.True(t, ok, "head_provider 必须是 relation，前端要 expand 它")
	require.False(t, hp.CascadeDelete)

	drifts, err := app.FindCollectionByNameOrId("drift_events")
	require.NoError(t, err)
	require.NotNil(t, drifts.Fields.GetByName("binding_drift"))
	require.NotNil(t, drifts.Fields.GetByName("binding_url"))
}

// seedCredential 建一条最小可用的凭据记录，返回 id。
func seedCredential(t *testing.T, app core.App, name string) string {
	t.Helper()
	c, err := app.FindCollectionByNameOrId("credentials")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("name", name)
	r.Set("cipher_value", "x")
	r.Set("last4", "1234")
	require.NoError(t, app.Save(r))
	return r.Id
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/migrations/ -run 'Provider|Binding' -v
```

Expected: FAIL，`providers` collection 不存在。

- [ ] **Step 3: 实现**

Create `hub/internal/migrations/003_providers.go`：

```go
package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(up003, down003, "003_providers.go")
}

// M1.5 的服务绑定（M1.5 spec §2）。追加式：不动 001 / 002。
//
// providers 与 credentials 是同一类东西——被配置集引用的资源，不是配置本身。
// key 不另存：服务配置里的 API key 就是一条 credential，复用加密落库、
// 末四位显示、启动自检与引用计数防误删（spec §2.1）。
func up003(app core.App) error {
	creds, err := app.FindCollectionByNameOrId("credentials")
	if err != nil {
		return err
	}

	providers := core.NewBaseCollection("providers")
	providers.Fields.Add(
		&core.TextField{Name: "name", Required: true, Max: 200},
		// preset 为空即「自定义平台」。可编辑的预设表被 YAGNI 掉了（spec §2.3），
		// 自定义平台由这条空 preset 完全覆盖。
		&core.TextField{Name: "preset", Max: 64},
		&core.TextField{Name: "base_url", Required: true, Max: 2000},
		&core.SelectField{Name: "auth_field", MaxSelect: 1, Values: []string{
			"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY",
		}},
		// CascadeDelete 必须是 false：删凭据不该悄悄带走服务配置。
		// 真正的保护在 credentials.Store：被 Provider 引用的凭据直接不许删（spec §5.3）。
		&core.RelationField{Name: "credential", Required: true, CollectionId: creds.Id,
			MaxSelect: 1, CascadeDelete: false},
		&core.JSONField{Name: "models", MaxSize: 8192},
		&core.JSONField{Name: "defaults", MaxSize: 2048},
		&core.TextField{Name: "note", Max: 2000},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	providers.AddIndex("idx_providers_name", true, "name", "")
	// 「这条凭据被哪些服务配置引用」是删除保护的热路径，给它索引。
	providers.AddIndex("idx_providers_credential", false, "credential", "")
	if err := app.Save(providers); err != nil {
		return err
	}

	// --- revisions.binding：引用进版本，值不进（spec §2.2） ---------
	revs, err := app.FindCollectionByNameOrId("revisions")
	if err != nil {
		return err
	}
	revs.Fields.Add(&core.JSONField{Name: "binding", MaxSize: 4096})
	if err := app.Save(revs); err != nil {
		return err
	}

	// --- config_sets.draft_binding / head_provider -------------------
	sets, err := app.FindCollectionByNameOrId("config_sets")
	if err != nil {
		return err
	}
	sets.Fields.Add(
		&core.JSONField{Name: "draft_binding", MaxSize: 4096},
		// head_provider 是冗余字段，唯一目的是让「改了 Provider X，谁要重注入」
		// 变成一次索引查询而不是 JSON 字段扫描 + 三次 join（spec §2.2）。
		// 唯一写入点在 revisions.PublishFiles，与 head 同写。
		&core.RelationField{Name: "head_provider", CollectionId: providers.Id,
			MaxSelect: 1, CascadeDelete: false},
	)
	sets.AddIndex("idx_config_sets_head_provider", false, "head_provider", "")
	if err := app.Save(sets); err != nil {
		return err
	}

	// --- drift_events：绑定漂移的标记与反查素材（spec §6.1） ----------
	drifts, err := app.FindCollectionByNameOrId("drift_events")
	if err != nil {
		return err
	}
	drifts.Fields.Add(
		&core.BoolField{Name: "binding_drift"},
		&core.TextField{Name: "binding_url", Max: 2000},
	)
	return app.Save(drifts)
}

func down003(app core.App) error {
	for name, fields := range map[string][]string{
		"drift_events": {"binding_drift", "binding_url"},
		"config_sets":  {"draft_binding", "head_provider"},
		"revisions":    {"binding"},
	} {
		c, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			continue
		}
		for _, f := range fields {
			c.Fields.RemoveByName(f)
		}
		if err := app.Save(c); err != nil {
			return err
		}
	}
	c, err := app.FindCollectionByNameOrId("providers")
	if err != nil {
		return nil
	}
	return app.Delete(c)
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./hub/internal/migrations/ -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/migrations/ && git commit -m "feat(hub): 迁移 003 建 providers 与绑定字段"
```

---

### Task 2: 预设表

**Files:**
- Create: `hub/internal/providers/presets.go`
- Create: `hub/internal/providers/presets_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `Preset`、`ModelSlots`、`Binding`、`AuthToken` / `AuthAPIKey`、`Presets()`、`PresetByID(id)`

**为什么编译进二进制、只读**：可编辑的预设表会立刻撞上「hub 升级时内置更新 vs 用户改动怎么合并」——三方合并、冲突 UI、用户改了又想要新版默认值。而它换来的唯一能力（自定义平台）用「建一条 `preset` 为空的 Provider」完全覆盖（spec §2.3）。

**四槽而非单槽的实证依据**（照抄 spec §2.3，写进文件头注释）：对 cc-switch 的 69 个 Claude 预设统计，34 个设模型变量，且恒为 `ANTHROPIC_MODEL` + `DEFAULT_OPUS` + `DEFAULT_SONNET` + `DEFAULT_HAIKU` 四个一起设；其余 35 个一个都不设。原因是 Claude Code 会自己去要 haiku 做标题生成一类的轻量活，只钉主模型会让它拿 `claude-haiku-*` 去打人家的 endpoint。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/providers/presets_test.go`：

```go
package providers_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

// 种子数据自检：auth_field 取值合法、四槽要么全空要么全满（spec §11）。
func TestPresetSeedIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range providers.Presets() {
		require.NotEmpty(t, p.ID, "预设必须有 id")
		require.False(t, seen[p.ID], "预设 id %s 重复", p.ID)
		seen[p.ID] = true

		require.NotEmpty(t, p.Name, "%s 缺显示名", p.ID)
		require.NotEmpty(t, p.BaseURL, "%s 缺 base_url", p.ID)
		require.Contains(t,
			[]string{providers.AuthToken, providers.AuthAPIKey}, p.AuthField,
			"%s 的 auth_field 非法", p.ID)

		// 四槽全空 = 透传（中转官方 Claude）；全满 = 有自家模型。
		// 半填是配置错误：Claude Code 会拿 claude-haiku-* 去打人家的 endpoint。
		require.True(t, p.Defaults.Empty() || p.Defaults.Full(),
			"%s 的四个模型槽必须要么全空要么全满，当前 %+v", p.ID, p.Defaults)

		if p.Defaults.Full() {
			require.Contains(t, p.Models, p.Defaults.Main,
				"%s 的默认主模型必须在模型清单里", p.ID)
		}
		// 订阅域预留（spec §9）：填了 type 就必须填 mode，反之亦然。
		require.Equal(t, p.CollectorType == "", p.CollectorMode == "",
			"%s 的 CollectorType 与 CollectorMode 必须同时为空或同时非空", p.ID)
	}
	require.NotEmpty(t, providers.Presets(), "种子表不能是空的")
}

func TestPresetByID(t *testing.T) {
	p, ok := providers.PresetByID("zhipu")
	require.True(t, ok)
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic", p.BaseURL)

	_, ok = providers.PresetByID("不存在的平台")
	require.False(t, ok)
}

func TestModelSlotsEmptyAndFull(t *testing.T) {
	require.True(t, providers.ModelSlots{}.Empty())
	require.False(t, providers.ModelSlots{}.Full())

	full := providers.ModelSlots{Main: "a", Opus: "a", Sonnet: "a", Haiku: "a"}
	require.True(t, full.Full())
	require.False(t, full.Empty())

	half := providers.ModelSlots{Main: "a"}
	require.False(t, half.Empty())
	require.False(t, half.Full())
}

// Presets 返回的切片被改了不能影响下一次调用——它是编译期常量的门面。
func TestPresetsIsNotAliased(t *testing.T) {
	a := providers.Presets()
	a[0].BaseURL = "https://tampered.test"
	b := providers.Presets()
	require.NotEqual(t, "https://tampered.test", b[0].BaseURL)
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/providers/ -v
```

Expected: 包不存在。

- [ ] **Step 3: 实现**

Create `hub/internal/providers/presets.go`：

```go
// Package providers 管 AI 服务配置（M1.5 spec §2）。
//
// 它与 credentials 是同一类东西——被配置集引用的资源，不是配置本身。
// key 不另存：服务配置里的 API key 就是一条 credential。
package providers

// 鉴权字段名。auth_field 决定的是 env 的**键名**，占位符替的是**值**，
// 替不了键名——所以换绑到 auth_field 不同的 Provider 时，settings.json 里
// 那行键名会对不上。方案是发布校验报错 + 一键修复（spec §3.3），
// 不自动改写用户文件。
const (
	AuthToken  = "ANTHROPIC_AUTH_TOKEN"
	AuthAPIKey = "ANTHROPIC_API_KEY"
)

// ModelSlots 是四个模型槽。四空 = 透传模式。
//
// 为什么是四槽而非单槽（spec §2.3 的实证依据，不是猜测）：对 cc-switch 的
// 69 个 Claude 预设统计，34 个设模型变量且恒为四个一起设，其余 35 个一个
// 都不设。原因是 Claude Code 会自己去要 haiku 做标题生成一类的轻量活，
// 只钉主模型会让它拿 claude-haiku-* 去打人家的 endpoint。
type ModelSlots struct {
	Main   string `json:"main"`
	Opus   string `json:"opus"`
	Sonnet string `json:"sonnet"`
	Haiku  string `json:"haiku"`
}

func (m ModelSlots) Empty() bool {
	return m.Main == "" && m.Opus == "" && m.Sonnet == "" && m.Haiku == ""
}

func (m ModelSlots) Full() bool {
	return m.Main != "" && m.Opus != "" && m.Sonnet != "" && m.Haiku != ""
}

// Binding 是配置集的服务绑定：{provider, models{main,opus,sonnet,haiku}}。
//
// **单数，不是数组**（spec §1.3）。多绑定的位置留给以后的迁移，现在不预留
// 结构——一个只可能有一个元素的 map 会诱使实现去支持它。
type Binding struct {
	Provider string     `json:"provider"` // providers 记录 id
	Models   ModelSlots `json:"models"`
}

// Preset 是内置平台条目。编译进二进制、只读（spec §2.3）。
type Preset struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	BaseURL    string     `json:"base_url"`
	AuthField  string     `json:"auth_field"`
	Models     []string   `json:"models"`
	Defaults   ModelSlots `json:"defaults"`
	WebsiteURL string     `json:"website_url,omitempty"`
	APIKeyURL  string     `json:"api_key_url,omitempty"`
	Icon       string     `json:"icon,omitempty"`
	IconColor  string     `json:"icon_color,omitempty"`

	// —— 订阅域预留（spec §9），本期只填数据不消费 ——
	// 预设表是服务配置与订阅采集器的公共接缝：一个平台条目同时携带
	// BaseURL/Models（喂服务配置）与 CollectorType/CollectorMode（喂采集器）。
	// M2 做采集器时只需给新表加一个可选 relation，不用回头返工服务配置。
	CollectorType string `json:"collector_type,omitempty"` // "zhipu" | "kimi" | ... | "" 表示无余额接口
	CollectorMode string `json:"collector_mode,omitempty"` // "fetch" | "local" | ""
}

// presets 是种子表。加一条就是加一行——长尾随用随加。
var presets = []Preset{ /* 见下表 */ }

// Presets 返回种子表的副本。调用方改它不影响下一次调用。
func Presets() []Preset {
	out := make([]Preset, len(presets))
	copy(out, presets)
	return out
}

func PresetByID(id string) (Preset, bool) {
	for _, p := range presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
```

种子数据按下表落地。初始只覆盖作者自用的平台即可（spec §2.3），长尾随用随加：

| ID | Name | BaseURL | AuthField | 四槽 | CollectorType / Mode |
|---|---|---|---|---|---|
| `zhipu` | 智谱 GLM | `https://open.bigmodel.cn/api/anthropic` | `ANTHROPIC_AUTH_TOKEN` | 全满 | `zhipu` / `fetch` |
| `zhipu_intl` | Z.ai (GLM International) | `https://api.z.ai/api/anthropic` | `ANTHROPIC_AUTH_TOKEN` | 全满 | `zhipu` / `fetch` |
| `kimi` | Kimi (Moonshot) | `https://api.moonshot.cn/anthropic` | `ANTHROPIC_AUTH_TOKEN` | 全满 | `kimi` / `fetch` |
| `volcengine` | 火山方舟 | `https://ark.cn-beijing.volces.com/api/v3` | `ANTHROPIC_API_KEY` | 全满 | `volcengine` / `fetch` |
| `zenmux` | ZenMux | `https://zenmux.ai/api/anthropic` | `ANTHROPIC_AUTH_TOKEN` | 全满 | 空 / 空 |
| `minimax` | MiniMax | `https://api.minimax.chat/anthropic` | `ANTHROPIC_AUTH_TOKEN` | 全满 | 空 / 空 |
| `anthropic` | Anthropic 官方 | `https://api.anthropic.com` | `ANTHROPIC_AUTH_TOKEN` | **全空**（透传） | 空 / 空 |

- [ ] **Step 4: 核对种子数据**

上表的 `BaseURL` 与模型 id 是按 spec §2.3 与各平台公开文档写的，**逐条对照各平台当前的 Claude Code 接入文档核一遍**再落地：base_url 的路径段（`/api/anthropic` vs `/anthropic` vs `/api/coding`）与模型 id 各家都在变。核对结果直接写进 `presets.go`，不要留 TODO——它是编译期常量，跟 hub 版本走，错了用户可以用自定义 Provider 随时绕过（spec §13）。

透传型（`anthropic`）的 `Models` 填官方模型 id 供 UI 展示，但 `Defaults` 四槽留空——那 35 个中转预设就是这么干的。

- [ ] **Step 5: 运行测试确认通过**

```bash
go test ./hub/internal/providers/ -v
```

Expected: 全部 PASS。

- [ ] **Step 6: 提交**

```bash
git add hub/internal/providers/ && git commit -m "feat(hub): providers 包与内置预设表"
```

---

### Task 3: base_url 归一化与反查匹配

**Files:**
- Create: `hub/internal/providers/url.go`
- Create: `hub/internal/providers/url_test.go`

**Interfaces:**
- Consumes: Task 2 的 `Presets()`
- Produces: `NormalizeURL(raw) string`、`HostOf(raw) string`

**口径（spec §6.4）**：归一化 = 去尾斜杠 + host 小写 + 去默认端口。**先精确匹配，失败再退到 host 匹配**——同一平台的 `/api/anthropic` 与 `/api/coding` 是不同产品线，精确匹配优先能区分开；host 匹配作为模糊提示，UI 措辞降级为「可能是」。

**不采用 cc-switch 的 `url.contains()` 子串判断**：它在 `bigmodel.cn` 这类既有个人版又有团队版、base_url 完全相同的情形下本来就区分不了（cc-switch 自己在注释里承认了）。我们有 `preset` 字段可以显式记录，不需要靠猜。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/providers/url_test.go`：

```go
package providers_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

func TestNormalizeURL(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"https://open.bigmodel.cn/api/anthropic/", "https://open.bigmodel.cn/api/anthropic"},
		{"https://OPEN.BigModel.CN/api/anthropic", "https://open.bigmodel.cn/api/anthropic"},
		{"https://open.bigmodel.cn:443/api/anthropic", "https://open.bigmodel.cn/api/anthropic"},
		{"http://localhost:80/anthropic", "http://localhost/anthropic"},
		{"http://localhost:8080/anthropic", "http://localhost:8080/anthropic"},
		{"  https://api.z.ai/api/anthropic  ", "https://api.z.ai/api/anthropic"},
		{"https://api.moonshot.cn", "https://api.moonshot.cn"},
		// 路径大小写不动：有的中转拿它区分产品线。
		{"https://x.test/API/Anthropic", "https://x.test/API/Anthropic"},
		// 解不动的原样返回（去空白），不要吞掉用户输入。
		{"不是 URL", "不是 URL"},
	} {
		require.Equal(t, c.want, providers.NormalizeURL(c.in), "输入 %q", c.in)
	}
}

func TestHostOf(t *testing.T) {
	require.Equal(t, "open.bigmodel.cn",
		providers.HostOf("https://OPEN.bigmodel.CN:443/api/anthropic/"))
	require.Equal(t, "localhost:8080", providers.HostOf("http://localhost:8080/x"))
	require.Equal(t, "", providers.HostOf("不是 URL"))
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/providers/ -run 'NormalizeURL|HostOf' -v
```

Expected: FAIL，未定义。

- [ ] **Step 3: 实现**

Create `hub/internal/providers/url.go`：

```go
package providers

import (
	"net/url"
	"strings"
)

// NormalizeURL 归一化 base_url（spec §6.2 / §6.4）：
// 去首尾空白、去尾斜杠、scheme 与 host 小写、去默认端口。
//
// 路径**不动大小写**：有的中转拿路径段区分产品线。
// 解不动的原样返回（只去空白）——吞掉用户输入比留着一个奇怪的字符串更糟。
func NormalizeURL(raw string) string {
	s := strings.TrimSpace(raw)
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return s
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = normalizeHost(u.Scheme, u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}

// HostOf 返回归一化后的 host（含非默认端口）。解不动返回空串。
func HostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return normalizeHost(strings.ToLower(u.Scheme), u.Host)
}

func normalizeHost(scheme, host string) string {
	h := strings.ToLower(host)
	switch {
	case scheme == "https" && strings.HasSuffix(h, ":443"):
		return strings.TrimSuffix(h, ":443")
	case scheme == "http" && strings.HasSuffix(h, ":80"):
		return strings.TrimSuffix(h, ":80")
	}
	return h
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./hub/internal/providers/ -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/providers/ && git commit -m "feat(hub): base_url 归一化"
```

---

### Task 4: `providers.Store` CRUD 与匹配

**Files:**
- Create: `hub/internal/providers/store.go`
- Create: `hub/internal/providers/store_test.go`
- Modify: `hub/internal/events/writer.go`（四个新 kind 常量）

**Interfaces:**
- Consumes: Task 1 的 collection、Task 2 的类型、Task 3 的归一化
- Produces: `Store`、`Input`、`Match`、`MatchProvider` / `MatchPreset` / `MatchNone`、四个 `Err*`

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/providers/store_test.go`（包内测试用 `providers` 而非 `providers_test`，因为要用 `newApp` 起库；参考 `hub/internal/credentials/store_test.go` 的起库方式）：

```go
package providers_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/providers"

	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
)

func newStore(t *testing.T) (*providers.Store, *tests.TestApp) {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)
	return providers.NewStore(app, events.NewWriter(app)), app
}

func seedCred(t *testing.T, app core.App, name string) string {
	t.Helper()
	c, err := app.FindCollectionByNameOrId("credentials")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("name", name)
	r.Set("cipher_value", "x")
	r.Set("last4", "1234")
	require.NoError(t, app.Save(r))
	return r.Id
}

func zhipuInput(credID string) providers.Input {
	p, _ := providers.PresetByID("zhipu")
	return providers.Input{
		Name:       "智谱 GLM · 个人",
		Preset:     p.ID,
		BaseURL:    p.BaseURL + "/", // 故意带尾斜杠，落库时要被归一化掉
		AuthField:  p.AuthField,
		Credential: credID,
		Models:     p.Models,
		Defaults:   p.Defaults,
	}
}

func TestCreateNormalizesBaseURL(t *testing.T) {
	s, app := newStore(t)
	rec, err := s.Create(zhipuInput(seedCred(t, app, "zhipu_key")))
	require.NoError(t, err)
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic", rec.GetString("base_url"))

	var models []string
	require.NoError(t, rec.UnmarshalJSONField("models", &models))
	require.NotEmpty(t, models)

	var defaults providers.ModelSlots
	require.NoError(t, rec.UnmarshalJSONField("defaults", &defaults))
	require.True(t, defaults.Full())
}

func TestCreateRejectsBadAuthField(t *testing.T) {
	s, app := newStore(t)
	in := zhipuInput(seedCred(t, app, "k"))
	in.AuthField = "ANTHROPIC_SECRET"
	_, err := s.Create(in)
	require.ErrorIs(t, err, providers.ErrBadAuthField)
}

func TestCreateRejectsBadBaseURL(t *testing.T) {
	s, app := newStore(t)
	in := zhipuInput(seedCred(t, app, "k"))
	in.BaseURL = "  "
	_, err := s.Create(in)
	require.ErrorIs(t, err, providers.ErrBadBaseURL)
}

func TestUpdateChangesBaseURLAndModels(t *testing.T) {
	s, app := newStore(t)
	rec, err := s.Create(zhipuInput(seedCred(t, app, "k")))
	require.NoError(t, err)

	in := zhipuInput(rec.GetString("credential"))
	in.BaseURL = "https://open.bigmodel.cn/api/coding/paas/v4"
	in.Models = append(in.Models, "glm-experimental")
	got, err := s.Update(rec.Id, in)
	require.NoError(t, err)
	require.Equal(t, "https://open.bigmodel.cn/api/coding/paas/v4", got.GetString("base_url"))

	var models []string
	require.NoError(t, got.UnmarshalJSONField("models", &models))
	require.Contains(t, models, "glm-experimental")
}

func TestUsingCredential(t *testing.T) {
	s, app := newStore(t)
	credID := seedCred(t, app, "shared_key")
	a, err := s.Create(zhipuInput(credID))
	require.NoError(t, err)

	in := zhipuInput(credID)
	in.Name = "智谱 GLM · 团队"
	b, err := s.Create(in)
	require.NoError(t, err)

	ids, err := s.UsingCredential(credID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{a.Id, b.Id}, ids)

	ids, err = s.UsingCredential(seedCred(t, app, "lonely"))
	require.NoError(t, err)
	require.Empty(t, ids)
}

// 精确匹配优先于 host 匹配：同一平台的 /api/anthropic 与 /api/coding
// 是不同产品线（spec §6.4）。
func TestMatchBaseURLPrefersExactProvider(t *testing.T) {
	s, app := newStore(t)
	credID := seedCred(t, app, "k")

	coding := zhipuInput(credID)
	coding.Name = "智谱 · Coding Plan"
	coding.BaseURL = "https://open.bigmodel.cn/api/coding/paas/v4"
	codingRec, err := s.Create(coding)
	require.NoError(t, err)

	anthropic := zhipuInput(credID)
	anthropic.Name = "智谱 · Anthropic 兼容"
	anthropicRec, err := s.Create(anthropic)
	require.NoError(t, err)

	m, err := s.MatchBaseURL("https://open.bigmodel.cn/api/coding/paas/v4/")
	require.NoError(t, err)
	require.Equal(t, providers.MatchProvider, m.Kind)
	require.True(t, m.Exact)
	require.Equal(t, codingRec.Id, m.ProviderID)

	m, err = s.MatchBaseURL("https://open.bigmodel.cn/api/anthropic")
	require.NoError(t, err)
	require.Equal(t, anthropicRec.Id, m.ProviderID)
}

// 路径对不上但 host 对得上 → 模糊命中，Exact=false。
func TestMatchBaseURLFallsBackToHost(t *testing.T) {
	s, app := newStore(t)
	rec, err := s.Create(zhipuInput(seedCred(t, app, "k")))
	require.NoError(t, err)

	m, err := s.MatchBaseURL("https://open.bigmodel.cn/api/paas/v4")
	require.NoError(t, err)
	require.Equal(t, providers.MatchProvider, m.Kind)
	require.False(t, m.Exact)
	require.Equal(t, rec.Id, m.ProviderID)
}

// 库里没有但内置预设里有 → 第二档。
func TestMatchBaseURLFallsBackToPreset(t *testing.T) {
	s, _ := newStore(t)
	m, err := s.MatchBaseURL("https://api.moonshot.cn/anthropic/")
	require.NoError(t, err)
	require.Equal(t, providers.MatchPreset, m.Kind)
	require.True(t, m.Exact)
	require.Equal(t, "kimi", m.PresetID)
}

func TestMatchBaseURLNoMatch(t *testing.T) {
	s, _ := newStore(t)
	m, err := s.MatchBaseURL("https://某个没人听说过的中转.test/v1")
	require.NoError(t, err)
	require.Equal(t, providers.MatchNone, m.Kind)
	require.Empty(t, m.ProviderID)
	require.Empty(t, m.PresetID)
}

func TestDeleteRefusesWhenBound(t *testing.T) {
	s, app := newStore(t)
	rec, err := s.Create(zhipuInput(seedCred(t, app, "k")))
	require.NoError(t, err)

	// 造一个 head_provider 指向它的配置集。
	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	set := core.NewRecord(sets)
	set.Set("name", "主力配置")
	set.Set("head_provider", rec.Id)
	require.NoError(t, app.Save(set))

	require.ErrorIs(t, s.Delete(rec.Id), providers.ErrInUse)
}

func TestDeleteRefusesWhenDraftBound(t *testing.T) {
	s, app := newStore(t)
	rec, err := s.Create(zhipuInput(seedCred(t, app, "k")))
	require.NoError(t, err)

	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	set := core.NewRecord(sets)
	set.Set("name", "草稿里绑着")
	set.Set("draft_binding", providers.Binding{Provider: rec.Id})
	require.NoError(t, app.Save(set))

	require.ErrorIs(t, s.Delete(rec.Id), providers.ErrInUse)
}

func TestDeleteWhenUnbound(t *testing.T) {
	s, app := newStore(t)
	rec, err := s.Create(zhipuInput(seedCred(t, app, "k")))
	require.NoError(t, err)
	require.NoError(t, s.Delete(rec.Id))

	_, err = s.Get(rec.Id)
	require.ErrorIs(t, err, providers.ErrNotFound)
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/providers/ -v
```

Expected: FAIL，`NewStore` 未定义。

- [ ] **Step 3: 实现**

先给 `hub/internal/events/writer.go` 追加常量：

```go
	KindProviderCreated = "provider.created"
	KindProviderUpdated = "provider.updated"
	KindProviderDeleted = "provider.deleted"
	KindBindingChanged  = "binding.changed"
```

Create `hub/internal/providers/store.go`：

```go
package providers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/events"
)

var (
	ErrNotFound     = errors.New("providers: 服务配置不存在")
	ErrInUse        = errors.New("providers: 服务配置仍被绑定")
	ErrBadAuthField = errors.New("providers: auth_field 非法")
	ErrBadBaseURL   = errors.New("providers: base_url 非法")
)

// 反查命中的档位（spec §6.3）。
const (
	MatchProvider = "provider"
	MatchPreset   = "preset"
	MatchNone     = "none"
)

// Match 是一次 base_url 反查的结果。
// Exact 为 false 表示只有 host 对得上——UI 措辞降级为「可能是」，
// 且动作仍需用户确认，不自动执行（spec §13）。
type Match struct {
	Kind         string `json:"kind"`
	Exact        bool   `json:"exact"`
	ProviderID   string `json:"provider_id,omitempty"`
	ProviderName string `json:"provider_name,omitempty"`
	PresetID     string `json:"preset_id,omitempty"`
	PresetName   string `json:"preset_name,omitempty"`
}

// Input 是建 / 改一条服务配置需要的全部字段。
type Input struct {
	Name       string
	Preset     string
	BaseURL    string
	AuthField  string
	Credential string // credentials 记录 id
	Models     []string
	Defaults   ModelSlots
	Note       string
}

type Store struct {
	app core.App
	ev  *events.Writer
}

func NewStore(app core.App, ev *events.Writer) *Store {
	return &Store{app: app, ev: ev}
}

func (s *Store) Get(id string) (*core.Record, error) {
	r, err := s.app.FindRecordById("providers", id)
	if err != nil || r == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return r, nil
}

func (s *Store) Create(in Input) (*core.Record, error) {
	if err := validate(in); err != nil {
		return nil, err
	}
	c, err := s.app.FindCollectionByNameOrId("providers")
	if err != nil {
		return nil, fmt.Errorf("providers: 找不到 collection: %w", err)
	}
	r := core.NewRecord(c)
	apply(r, in)
	if err := s.app.Save(r); err != nil {
		return nil, fmt.Errorf("providers: 创建 %s: %w", in.Name, err)
	}
	s.write(events.KindProviderCreated, r)
	return r, nil
}

// Update 改 base_url / 模型 / 凭据。
//
// **不产生新 Revision**（spec §2.2）：Provider 的当前值是活的，跟版本无关。
// 下发由调用方发一次 ConfigNotify 完成（见 configsync.NotifyProvider）。
func (s *Store) Update(id string, in Input) (*core.Record, error) {
	if err := validate(in); err != nil {
		return nil, err
	}
	r, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	apply(r, in)
	if err := s.app.Save(r); err != nil {
		return nil, fmt.Errorf("providers: 更新 %s: %w", id, err)
	}
	s.write(events.KindProviderUpdated, r)
	return r, nil
}

// Delete 删服务配置。被任何配置集的 head_provider 或 draft_binding 指向时拒绝。
//
// 与凭据的删除保护同理（spec §5.3）：删掉一条正被指着的 Provider 会让
// 全机队在下次重注入时拿到空值。
func (s *Store) Delete(id string) error {
	r, err := s.Get(id)
	if err != nil {
		return err
	}
	setIDs, err := s.BoundBy(id)
	if err != nil {
		return err
	}
	if len(setIDs) > 0 {
		return fmt.Errorf("%w: 被 %d 个配置集的当前版本或草稿绑定", ErrInUse, len(setIDs))
	}
	if err := s.app.Delete(r); err != nil {
		return fmt.Errorf("providers: 删除 %s: %w", id, err)
	}
	s.write(events.KindProviderDeleted, r)
	return nil
}

// BoundBy 返回绑定了该 Provider 的配置集 id（head_provider 或 draft_binding）。
// UI 的「被 N 个配置集引用」与重注入的反查链都用它。
func (s *Store) BoundBy(id string) ([]string, error) {
	sets, err := s.app.FindAllRecords("config_sets")
	if err != nil {
		return nil, fmt.Errorf("providers: 扫描配置集: %w", err)
	}
	var out []string
	for _, set := range sets {
		if set.GetString("head_provider") == id {
			out = append(out, set.Id)
			continue
		}
		var b Binding
		if err := set.UnmarshalJSONField("draft_binding", &b); err == nil && b.Provider == id {
			out = append(out, set.Id)
		}
	}
	return out, nil
}

// UsingCredential 返回引用该凭据的服务配置 id。
// credentials 的删除保护与轮换重注入都靠它（spec §5.3）。
func (s *Store) UsingCredential(credID string) ([]string, error) {
	recs, err := s.app.FindRecordsByFilter("providers",
		"credential = {:c}", "name", 0, 0, map[string]any{"c": credID})
	if err != nil {
		return nil, fmt.Errorf("providers: 查询引用凭据 %s 的服务配置: %w", credID, err)
	}
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.Id)
	}
	return out, nil
}

// MatchBaseURL 做反查：先在已有 Provider 里精确匹配，再 host 匹配，
// 然后在内置预设里精确匹配、host 匹配，都不中返回 MatchNone（spec §6.3 / §6.4）。
func (s *Store) MatchBaseURL(raw string) (Match, error) {
	want := NormalizeURL(raw)
	wantHost := HostOf(raw)

	recs, err := s.app.FindAllRecords("providers")
	if err != nil {
		return Match{}, fmt.Errorf("providers: 扫描服务配置: %w", err)
	}
	var hostHit *core.Record
	for _, r := range recs {
		got := r.GetString("base_url")
		if NormalizeURL(got) == want {
			return Match{Kind: MatchProvider, Exact: true,
				ProviderID: r.Id, ProviderName: r.GetString("name")}, nil
		}
		if hostHit == nil && wantHost != "" && HostOf(got) == wantHost {
			hostHit = r
		}
	}
	if hostHit != nil {
		return Match{Kind: MatchProvider, Exact: false,
			ProviderID: hostHit.Id, ProviderName: hostHit.GetString("name")}, nil
	}

	var hostPreset *Preset
	for i := range presets {
		p := presets[i]
		if NormalizeURL(p.BaseURL) == want {
			return Match{Kind: MatchPreset, Exact: true,
				PresetID: p.ID, PresetName: p.Name}, nil
		}
		if hostPreset == nil && wantHost != "" && HostOf(p.BaseURL) == wantHost {
			hostPreset = &presets[i]
		}
	}
	if hostPreset != nil {
		return Match{Kind: MatchPreset, Exact: false,
			PresetID: hostPreset.ID, PresetName: hostPreset.Name}, nil
	}
	return Match{Kind: MatchNone}, nil
}

func validate(in Input) error {
	if strings.TrimSpace(in.Name) == "" {
		return fmt.Errorf("providers: 需要 name")
	}
	if in.AuthField != AuthToken && in.AuthField != AuthAPIKey {
		return fmt.Errorf("%w: %q（只允许 %s / %s）",
			ErrBadAuthField, in.AuthField, AuthToken, AuthAPIKey)
	}
	if HostOf(in.BaseURL) == "" {
		return fmt.Errorf("%w: %q 解析不出 host", ErrBadBaseURL, in.BaseURL)
	}
	if in.Credential == "" {
		return fmt.Errorf("providers: 需要 credential")
	}
	// 半填的四槽是配置错误：只钉主模型会让 Claude Code 拿 claude-haiku-*
	// 去打人家的 endpoint（spec §2.3）。
	if !in.Defaults.Empty() && !in.Defaults.Full() {
		return fmt.Errorf("providers: 四个模型槽必须要么全空（透传）要么全满")
	}
	return nil
}

func apply(r *core.Record, in Input) {
	models := in.Models
	if models == nil {
		models = []string{}
	}
	r.Set("name", strings.TrimSpace(in.Name))
	r.Set("preset", in.Preset)
	// 归一化后存储（spec §6.2）：反查靠它，不能把用户敲的尾斜杠带进库。
	r.Set("base_url", NormalizeURL(in.BaseURL))
	r.Set("auth_field", in.AuthField)
	r.Set("credential", in.Credential)
	r.Set("models", models)
	r.Set("defaults", in.Defaults)
	r.Set("note", in.Note)
}

// write 记事件。事件里只有名字与平台，不含 key、不含 base_url 之外的值。
func (s *Store) write(kind string, r *core.Record) {
	if s.ev == nil {
		return
	}
	if err := s.ev.Write(kind, "", map[string]any{
		"provider": r.Id, "name": r.GetString("name"), "preset": r.GetString("preset"),
	}); err != nil {
		s.app.Logger().Warn("写 "+kind+" 事件失败", "error", err)
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./hub/internal/providers/ ./hub/internal/events/ -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/providers/ hub/internal/events/ && git commit -m "feat(hub): providers CRUD、反查匹配与事件 kind"
```

---

### Task 5: 凭据引用计数认得 Provider（**强制**）

**Files:**
- Modify: `hub/internal/credentials/store.go`
- Modify: `hub/internal/credentials/store_test.go`（追加）
- Modify: `hub/internal/configsync/notify.go`（`ReferencedBy` 的调用点）
- Modify: `hub/hub.go`（如有其他调用点）

**Interfaces:**
- Consumes: Task 1 的 `providers` collection
- Produces: `ReferencedBy(name) (setIDs, revIDs, providerIDs []string, err error)`、`ValueByID(id) (string, error)`

**为什么必须做**：`Store` 现在的 `ErrInUse` 判定只看 `revisions.refs` 与 `config_sets.draft_refs`。Provider 引用凭据的方式是 relation 字段，不在这两处——**不改的话，一条正在被 Provider 使用的凭据可以被删掉**，然后全机队在下次重注入时拿到空值（spec §5.3）。这是本设计里唯一会造成数据面损坏的遗漏点。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/credentials/store_test.go`（用该文件既有的起库与建 Store 方式）：

```go
// 被 Provider 引用的凭据不可删除（spec §5.3）。
// 不拦住的话：删凭据 → 下次重注入 → 全机队拿到空值 → Claude Code 全体 401。
func TestDeleteRefusesWhenReferencedByProvider(t *testing.T) {
	s, app := newStore(t) // 该文件既有的构造函数
	_, err := s.Create("zhipu_key", "sk-zhipu-abcdefghij", "")
	require.NoError(t, err)

	cred, err := app.FindFirstRecordByData("credentials", "name", "zhipu_key")
	require.NoError(t, err)

	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	p := core.NewRecord(c)
	p.Set("name", "智谱 GLM · 个人")
	p.Set("base_url", "https://open.bigmodel.cn/api/anthropic")
	p.Set("auth_field", "ANTHROPIC_AUTH_TOKEN")
	p.Set("credential", cred.Id)
	require.NoError(t, app.Save(p))

	err = s.Delete("zhipu_key")
	require.ErrorIs(t, err, credentials.ErrInUse)
	require.Contains(t, err.Error(), "服务配置", "错误信息要说清是被谁引用的")
}

func TestReferencedByReportsProviders(t *testing.T) {
	s, app := newStore(t)
	_, err := s.Create("zhipu_key", "sk-zhipu-abcdefghij", "")
	require.NoError(t, err)
	cred, err := app.FindFirstRecordByData("credentials", "name", "zhipu_key")
	require.NoError(t, err)

	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	p := core.NewRecord(c)
	p.Set("name", "智谱 GLM · 个人")
	p.Set("base_url", "https://open.bigmodel.cn/api/anthropic")
	p.Set("auth_field", "ANTHROPIC_AUTH_TOKEN")
	p.Set("credential", cred.Id)
	require.NoError(t, app.Save(p))

	setIDs, revIDs, providerIDs, err := s.ReferencedBy("zhipu_key")
	require.NoError(t, err)
	require.Empty(t, setIDs)
	require.Empty(t, revIDs)
	require.Equal(t, []string{p.Id}, providerIDs)
}

func TestValueByID(t *testing.T) {
	s, app := newStore(t)
	_, err := s.Create("zhipu_key", "sk-zhipu-abcdefghij", "")
	require.NoError(t, err)
	cred, err := app.FindFirstRecordByData("credentials", "name", "zhipu_key")
	require.NoError(t, err)

	v, err := s.ValueByID(cred.Id)
	require.NoError(t, err)
	require.Equal(t, "sk-zhipu-abcdefghij", v)

	_, err = s.ValueByID("不存在的 id")
	require.ErrorIs(t, err, credentials.ErrNotFound)
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/credentials/ -run 'Provider|ValueByID' -v
```

Expected: FAIL——凭据被删掉了 / `ReferencedBy` 返回值个数不符。

- [ ] **Step 3: 实现**

`hub/internal/credentials/store.go`：

`refsField` 追加（保持与 `configsets.Refs` 同形）：

```go
type refsField struct {
	Creds        []string `json:"creds"`
	Vars         []string `json:"vars"`
	ProviderKeys []string `json:"provider_keys"`
}
```

`ReferencedBy` 改签名并在末尾追加 Provider 扫描：

```go
// ReferencedBy 返回引用该凭据的配置集 id（head revision 或 draft）、
// 历史 revision id，以及**引用它的 AI 服务配置 id**（spec §5.3）。
//
// 前两者查的是 refs 字段而不是全库扫 blob 内容（M1 spec §6.5）；
// Provider 引用凭据的方式是 relation 字段，因此单独扫一遍——
// 不扫的话一条正在被 Provider 使用的凭据可以被删掉，
// 然后全机队在下次重注入时拿到空值。
func (s *Store) ReferencedBy(name string) ([]string, []string, []string, error) {
	// …原有 setIDs / revIDs 逻辑不变，只把每处 return 补上第三个返回值…

	cred, err := s.find(name)
	if err != nil {
		return nil, nil, nil, err
	}
	provs, err := s.app.FindRecordsByFilter("providers",
		"credential = {:c}", "name", 0, 0, map[string]any{"c": cred.Id})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("credentials: 扫描服务配置: %w", err)
	}
	providerIDs := make([]string, 0, len(provs))
	for _, p := range provs {
		providerIDs = append(providerIDs, p.Id)
	}
	return setIDs, revIDs, providerIDs, nil
}
```

`Delete` 追加判定：

```go
	setIDs, _, providerIDs, err := s.ReferencedBy(name)
	if err != nil {
		return err
	}
	if len(setIDs) > 0 {
		return fmt.Errorf("%w: 被 %d 个配置集的当前版本或草稿引用", ErrInUse, len(setIDs))
	}
	if len(providerIDs) > 0 {
		return fmt.Errorf("%w: 被 %d 个 AI 服务配置引用", ErrInUse, len(providerIDs))
	}
```

追加 `ValueByID`：

```go
// ValueByID 按记录 id 取值。providers.credential 是 relation 字段，
// 存的是 id 而不是名字，因此下发时走这条而不是 Value(name)。
func (s *Store) ValueByID(id string) (string, error) {
	r, err := s.app.FindRecordById("credentials", id)
	if err != nil || r == nil {
		return "", fmt.Errorf("%w: id %s", ErrNotFound, id)
	}
	return Decrypt(s.key, r.GetString("cipher_value"))
}
```

`hub/internal/configsync/notify.go` 的 `NotifyCredential` 调用点改成四返回值（本任务只改签名，用 `_` 接住 `providerIDs`；真正消费它是子计划 04 Task 2）：

```go
	setIDs, _, _, err := s.d.Creds.ReferencedBy(name)
```

还有三处调用点要跟着改签名，改完不动语义：
- `hub/internal/credentials/store.go:126`（`Delete` 内部，见上）
- `hub/internal/credentials/store_test.go:89`（`setIDs, _, err :=` → `setIDs, _, _, err :=`）
- `hub/internal/credentials/store_test.go:130`（`_, revIDs, err :=` → `_, revIDs, _, err :=`）

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/... -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/credentials/ hub/internal/configsync/ && git commit -m "fix(hub): 凭据引用计数认 Provider，防止误删导致全机队拿到空值"
```

---

## 本子计划完成后的状态

- 库里能建 Provider、能反查 base_url、能拒绝删掉在用的凭据与在用的 Provider。
- 预设表在二进制里，有自检测试兜住数据形状。
- **还没有任何东西会读 `binding` / `draft_binding` / `head_provider`**——那是子计划 03。
- `go test -tags=testing ./...` 全绿。
