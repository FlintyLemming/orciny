# 子计划 03 · providers 双端点数据模型与预设表

**前置**：01（`secretbox`）、02（`protocol.ProviderKeys`）
**读这份之前先读** [00-overview.md](00-overview.md) 的 Global Constraints、
**两处偏离**与全局接口契约。

**交付物**：迁移 `004_provider_endpoints.go`（追加 + 密文搬运）；`providers` 包的
`Endpoint` / `ClaudeEndpoint` / `OpenAIEndpoint` 类型与 `Configured()`；双端点的
`Input` / `validate` / `apply`；两级 key 取值 `Store.Key`；`Store.VerifyAll`；
`MatchBaseURL` 只扫 claude 端点；预设表七条平台的两组端点。

**这个子计划有两处会造成数据面损坏，都必须有测试**：

1. **密文搬运搬错或搬漏**（Task 1）：`004` 之后如果 `key_cipher` 是空的，
   全机队在下次重注入时拿到空 key。测试要在一个「有 provider + 被它引用的
   凭据」的库上跑迁移，然后**真的解一次密**。
2. **两级 key 取值取反**（Task 4）：端点级应当**覆盖**平台级。取反的后果是
   给 claude 端点单独配了 key 的用户，实际下发的是平台级那把——错的 key
   一路发到全机队，错误现场（401）离原因很远。

---

### Task 1: 迁移 `004`（追加字段 + 密文搬运）

**Files:**
- Create: `hub/internal/migrations/004_provider_endpoints.go`
- Test: `hub/internal/migrations/migrations_test.go`（追加，不改已有用例）

**Interfaces:**
- Consumes: `002` 建的 `credentials`、`003` 建的 `providers`
- Produces: `providers` 的 `key_cipher`(Hidden) / `key_last4` /
  `claude_key_cipher`(Hidden) / `openai_key_cipher`(Hidden) / `claude`(JSON) /
  `openai`(JSON)；`providers.credential` 变为非必填

`credentials` collection、`providers.credential` relation 与四个旧字段
（`base_url` / `auth_field` / `models` / `defaults`）**本任务一律不删**——
它们在子计划 08 的 `005` 里才消失。理由见 00-overview「偏离一」。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/migrations/migrations_test.go`：

```go
func TestProviderEndpointFieldsExist(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)

	for _, f := range []string{
		"key_cipher", "key_last4",
		"claude_key_cipher", "openai_key_cipher",
		"claude", "openai",
	} {
		require.NotNil(t, c.Fields.GetByName(f), "providers.%s 缺失", f)
	}

	// 密文一律 Hidden：PocketBase 的列表查询与 realtime 都不会带上它
	// （M1.6 spec §5.2「密文永不回传前端」）。
	for _, f := range []string{"key_cipher", "claude_key_cipher", "openai_key_cipher"} {
		tf, ok := c.Fields.GetByName(f).(*core.TextField)
		require.True(t, ok, "%s 必须是 TextField", f)
		require.True(t, tf.Hidden, "%s 必须 Hidden", f)
	}

	// last4 是给 UI 回显的，正是要回传的东西。
	l4, ok := c.Fields.GetByName("key_last4").(*core.TextField)
	require.True(t, ok)
	require.False(t, l4.Hidden)

	// credential 在 004 之后必须是非必填，否则新建 provider 会被挡住。
	rel, ok := c.Fields.GetByName("credential").(*core.RelationField)
	require.True(t, ok)
	require.False(t, rel.Required, "004 之后 credential 不再必填")
}

// 密文**直接搬、不解密**：同一把主密钥、同一套 AES-GCM，secretbox 只是换了
// 包名（M1.6 spec §6.1 第 2 步）。这条测试是这个前提的证明。
func TestMigration004MovesCipherIntoProvider(t *testing.T) {
	app := newApp(t)

	key, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	cipher, err := secretbox.Encrypt(key, "sk-zhipu-abcdef123456")
	require.NoError(t, err)

	credID := seedCredentialWith(t, app, "zhipu_key", cipher, "3456")
	provID := seedLegacyProvider(t, app, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", credID)

	// 迁移在 newApp 里已经跑过了，这里手动再跑一次 up004 的搬运部分不现实——
	// 改为：先建数据、再新建一个 app 复用同一个数据目录。见下方 runUp004 helper。
	require.NoError(t, runUp004(t, app))

	p, err := app.FindRecordById("providers", provID)
	require.NoError(t, err)

	require.Equal(t, cipher, p.GetString("key_cipher"), "密文必须原样搬过来")
	require.Equal(t, "3456", p.GetString("key_last4"))

	pt, err := secretbox.Decrypt(key, p.GetString("key_cipher"))
	require.NoError(t, err, "搬过来的密文必须还解得开")
	require.Equal(t, "sk-zhipu-abcdef123456", pt)

	var claude struct {
		BaseURL   string   `json:"base_url"`
		AuthField string   `json:"auth_field"`
		Models    []string `json:"models"`
		Defaults  struct {
			Main, Opus, Sonnet, Haiku string
		} `json:"defaults"`
	}
	require.NoError(t, p.UnmarshalJSONField("claude", &claude))
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic", claude.BaseURL)
	require.Equal(t, "ANTHROPIC_AUTH_TOKEN", claude.AuthField)
	require.Equal(t, []string{"glm-5.2", "glm-4.7"}, claude.Models)
	require.Equal(t, "glm-5.2", claude.Defaults.Main)

	// openai 端点没有来源，必须是「未配置」。
	var openai struct {
		BaseURL string `json:"base_url"`
	}
	require.NoError(t, p.UnmarshalJSONField("openai", &openai))
	require.Empty(t, openai.BaseURL)
}

func TestMigration004IsIdempotentOnEmptyProviderTable(t *testing.T) {
	app := newApp(t) // 一条 provider 都没有
	require.NoError(t, runUp004(t, app), "空表上重跑迁移不得报错")
}

// ---------- helpers ----------

// seedCredentialWith 建一条带指定密文的凭据，返回 id。
func seedCredentialWith(t *testing.T, app core.App, name, cipher, last4 string) string {
	t.Helper()
	c, err := app.FindCollectionByNameOrId("credentials")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("name", name)
	r.Set("cipher_value", cipher)
	r.Set("last4", last4)
	require.NoError(t, app.Save(r))
	return r.Id
}

// seedLegacyProvider 用 003 的老字段建一条 provider，返回 id。
func seedLegacyProvider(t *testing.T, app core.App, name, baseURL, credID string) string {
	t.Helper()
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("name", name)
	r.Set("base_url", baseURL)
	r.Set("auth_field", "ANTHROPIC_AUTH_TOKEN")
	r.Set("credential", credID)
	r.Set("models", []string{"glm-5.2", "glm-4.7"})
	r.Set("defaults", map[string]string{
		"main": "glm-5.2", "opus": "glm-5.2", "sonnet": "glm-5.2", "haiku": "glm-4.7",
	})
	require.NoError(t, app.Save(r))
	return r.Id
}
```

`runUp004` 要能在**数据已就位之后**再跑一次搬运。把迁移的搬运逻辑单独导出成
一个可复用函数，测试直接调它——这比伪造迁移执行器可靠得多：

```go
// runUp004 重跑 004 的数据搬运部分。字段已在 newApp 时加好，
// 搬运对已搬过的记录是幂等的（见 up004 的实现）。
func runUp004(t *testing.T, app core.App) error {
	t.Helper()
	return migrations.BackfillProviderEndpoints(app)
}
```

`migrations_test.go` 的 import 里加：

```go
	"github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/hub/internal/secretbox"
```

（原本是 `_ "…/migrations"` 空导入，改成具名导入。）

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/migrations/ -run 'ProviderEndpoint|Migration004' -v
```

Expected: FAIL —— 字段不存在、`migrations.BackfillProviderEndpoints` 未定义。

- [ ] **Step 3: 实现**

Create `hub/internal/migrations/004_provider_endpoints.go`：

```go
package migrations

import (
	"encoding/json"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(up004, down004, "004_provider_endpoints.go")
}

// M1.6 的双端点服务配置（M1.6 spec §2.1 / §6.1 的第 1、2 步）。
//
// **追加式**：本迁移只加字段、搬数据，一个旧字段都不删。破坏性的那一半
// （删 relation、删旧字段、删 credentials collection）在 005 里，
// 拆开是为了让中间每一步的测试都能是绿的（实现计划 00-overview「偏离一」）。
//
// 密文**直接搬、不解密**：同一把主密钥、同一套 AES-GCM，secretbox 只是换了
// 包名。这是「KeyFileName 与 EnvKeyName 都不变」换来的（spec §2.5）。
func up004(app core.App) error {
	provs, err := app.FindCollectionByNameOrId("providers")
	if err != nil {
		return err
	}

	provs.Fields.Add(
		// 三处密文一律 Hidden：PocketBase 的列表查询与 realtime 都带不上它。
		// spec §5.2 说「密文永不回传前端」，JSON 字段没法只隐藏一个子键，
		// 所以密文不进端点 JSON，单独放顶层。
		&core.TextField{Name: "key_cipher", Max: 8192, Hidden: true},
		&core.TextField{Name: "key_last4", Max: 8},
		&core.TextField{Name: "claude_key_cipher", Max: 8192, Hidden: true},
		&core.TextField{Name: "openai_key_cipher", Max: 8192, Hidden: true},
		// 端点子结构。末四位在里面（要回传），密文不在里面。
		&core.JSONField{Name: "claude", MaxSize: 16384},
		&core.JSONField{Name: "openai", MaxSize: 16384},
	)

	// credential 转非必填：004 之后新建的 provider 不再有凭据可指。
	// 字段本身留到 005 才删。
	if rel, ok := provs.Fields.GetByName("credential").(*core.RelationField); ok {
		rel.Required = false
	}

	if err := app.Save(provs); err != nil {
		return err
	}
	return BackfillProviderEndpoints(app)
}

// BackfillProviderEndpoints 把每条 provider 的旧字段与它引用的凭据搬进新结构
// （spec §6.1 第 2 步）。导出是为了让迁移测试能在数据就位之后再跑一次。
//
// 幂等：已经有 claude.base_url 的记录直接跳过，重跑不会把用户后来改过的
// 端点数据覆盖回旧字段的值。
func BackfillProviderEndpoints(app core.App) error {
	recs, err := app.FindAllRecords("providers")
	if err != nil {
		return fmt.Errorf("004: 扫描服务配置: %w", err)
	}
	for _, r := range recs {
		var existing struct {
			BaseURL string `json:"base_url"`
		}
		_ = r.UnmarshalJSONField("claude", &existing)
		if existing.BaseURL != "" {
			continue // 搬过了
		}

		var models []string
		_ = r.UnmarshalJSONField("models", &models)
		if models == nil {
			models = []string{}
		}
		var defaults map[string]string
		_ = r.UnmarshalJSONField("defaults", &defaults)
		if defaults == nil {
			defaults = map[string]string{}
		}

		claude := map[string]any{
			"base_url":   r.GetString("base_url"),
			"auth_field": r.GetString("auth_field"),
			"models":     models,
			"defaults": map[string]string{
				"main":   defaults["main"],
				"opus":   defaults["opus"],
				"sonnet": defaults["sonnet"],
				"haiku":  defaults["haiku"],
			},
		}

		// 凭据的密文与末四位搬到**平台级**：003 的模型里一条 provider
		// 只有一把 key，它对两个端点都适用（spec §2.3 的常见情形）。
		if credID := r.GetString("credential"); credID != "" {
			if cred, cerr := app.FindRecordById("credentials", credID); cerr == nil && cred != nil {
				r.Set("key_cipher", cred.GetString("cipher_value"))
				r.Set("key_last4", cred.GetString("last4"))
				claude["key_last4"] = cred.GetString("last4")
			}
		}

		cb, err := json.Marshal(claude)
		if err != nil {
			return fmt.Errorf("004: 序列化 %s 的 claude 端点: %w", r.Id, err)
		}
		r.Set("claude", json.RawMessage(cb))
		// openai 端点没有来源，写一个「未配置」的空结构而不是留 null——
		// 前端拿到 null 与拿到 {base_url:""} 要走两条分支，少一条是一条。
		r.Set("openai", json.RawMessage(
			`{"base_url":"","auth_field":"OPENAI_API_KEY","models":[],"default_model":""}`))

		if err := app.Save(r); err != nil {
			return fmt.Errorf("004: 保存服务配置 %s: %w", r.Id, err)
		}
	}
	return nil
}

// down004 是真的可逆：本迁移只加字段。
// （破坏性的那一半在 005，它的 down 直接返回错误。）
func down004(app core.App) error {
	provs, err := app.FindCollectionByNameOrId("providers")
	if err != nil {
		return nil
	}
	for _, f := range []string{
		"key_cipher", "key_last4", "claude_key_cipher", "openai_key_cipher",
		"claude", "openai",
	} {
		provs.Fields.RemoveByName(f)
	}
	if rel, ok := provs.Fields.GetByName("credential").(*core.RelationField); ok {
		rel.Required = true
	}
	return app.Save(provs)
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./hub/internal/migrations/ -v
```

Expected: PASS，含既有的 001/002/003 用例。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/migrations/
git commit -m "feat(hub): 迁移 004 给 providers 加双端点字段并搬运密文"
```

---

### Task 2: `Endpoint` 类型与 `Configured()`

**Files:**
- Modify: `hub/internal/providers/presets.go`（类型定义都在这个文件里）
- Test: `hub/internal/providers/endpoint_test.go`（新建）

**Interfaces:**
- Consumes: 无
- Produces: `Endpoint` / `ClaudeEndpoint` / `OpenAIEndpoint` / `Configured()` /
  `ClaudeOf(r)` / `OpenAIOf(r)` / `EndpointClaude` / `EndpointOpenAI` /
  `DefaultOpenAIAuthField`

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/providers/endpoint_test.go`：

```go
package providers_test

import (
	"encoding/json"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

// 「端点配没配」= base_url 是否为空（spec §2.2）。
// 不加 enabled 布尔：两个字段表达同一件事只会产生
// 「enabled=true 但 base_url 为空」这种没有正确处理方式的中间态。
func TestConfiguredIsBaseURLPresence(t *testing.T) {
	require.False(t, providers.Endpoint{}.Configured())
	require.False(t, providers.Endpoint{AuthField: "ANTHROPIC_AUTH_TOKEN"}.Configured(),
		"只填了鉴权字段不算配置")
	require.True(t, providers.Endpoint{BaseURL: "https://x.example/anthropic"}.Configured())
}

func TestClaudeOfAndOpenAIOfDecodeRecord(t *testing.T) {
	app, _ := newTestApp(t)
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("name", "智谱 GLM")
	r.Set("claude", json.RawMessage(`{
		"base_url":"https://open.bigmodel.cn/api/anthropic",
		"auth_field":"ANTHROPIC_AUTH_TOKEN",
		"key_last4":"3456",
		"models":["glm-5.2"],
		"defaults":{"main":"glm-5.2","opus":"glm-5.2","sonnet":"glm-5.2","haiku":"glm-4.7"}
	}`))
	r.Set("openai", json.RawMessage(`{
		"base_url":"https://open.bigmodel.cn/api/paas/v4",
		"auth_field":"OPENAI_API_KEY",
		"models":["glm-5.2"],
		"default_model":"glm-5.2"
	}`))
	require.NoError(t, app.Save(r))

	cl := providers.ClaudeOf(r)
	require.True(t, cl.Configured())
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic", cl.BaseURL)
	require.Equal(t, "3456", cl.KeyLast4)
	require.Equal(t, "glm-4.7", cl.Defaults.Haiku)

	oa := providers.OpenAIOf(r)
	require.True(t, oa.Configured())
	require.Equal(t, "glm-5.2", oa.DefaultModel)
}

// 空 JSON 字段是正常情形（刚建的记录、004 之前的老记录），返回零值而不是炸。
func TestEndpointOfEmptyRecordIsZeroValue(t *testing.T) {
	app, _ := newTestApp(t)
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("name", "空的")
	require.NoError(t, app.Save(r))

	require.False(t, providers.ClaudeOf(r).Configured())
	require.False(t, providers.OpenAIOf(r).Configured())
	require.Empty(t, providers.OpenAIOf(r).DefaultModel)
}
```

`newTestApp` 这个 helper 在 Task 4 与 Task 5 也要用，放在
`hub/internal/providers/store_test.go` 里（改造既有的构造 helper 即可，
见 Task 4 Step 3）。本任务先在 `endpoint_test.go` 里临时写一份：

```go
func newTestApp(t *testing.T) (*tests.TestApp, []byte) {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)
	key, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	return app, key
}
```

（Task 4 会把它挪到 `store_test.go` 并从这里删掉，避免同包重复定义。）

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/providers/ -run 'Configured|ClaudeOf|OpenAIOf|EndpointOfEmpty' -v
```

Expected: FAIL，`providers.Endpoint` 未定义。

- [ ] **Step 3: 实现**

Modify `hub/internal/providers/presets.go`，在 `ModelSlots` 之后插入：

```go
// 端点名。与占位符的端点段（protocol.ProviderKeys）逐字符一致。
const (
	EndpointClaude = "claude"
	EndpointOpenAI = "openai"
)

// DefaultOpenAIAuthField 是 openai 端点的默认鉴权字段名。
//
// 它是**自由文本，不是枚举**（spec §2.4）：auth_field 的二选一枚举是
// Claude Code 的 settings.json env 键名问题；openai 侧本期没有消费者，
// 也就没有依据去定枚举。一个默认 OPENAI_API_KEY 的文本字段既够用，
// 又不会在接 Codex 时挡路。
const DefaultOpenAIAuthField = "OPENAI_API_KEY"

// Endpoint 是两个协议端点的公共部分。
//
// **密文不在这里**：三处密文放在记录的顶层 Hidden 字段
// （key_cipher / claude_key_cipher / openai_key_cipher），
// 因为 PocketBase 没法只隐藏 JSON 字段里的一个子键，而 spec §5.2 要求
// 密文永不回传前端。末四位留在这里——它正是要回传给 UI 回显的东西。
type Endpoint struct {
	BaseURL   string   `json:"base_url"`
	AuthField string   `json:"auth_field"`
	KeyLast4  string   `json:"key_last4,omitempty"`
	Models    []string `json:"models"`
}

// Configured 是「这个端点配没配」的唯一判定（spec §2.2）。
//
// 一个没有 base_url 的端点本来就无从使用，因此不另加 enabled 布尔——
// 两个字段表达同一件事只会产生「enabled=true 但 base_url 为空」这种
// 需要额外校验、且没有正确处理方式的中间态。
// 发布校验、UI 置灰、快照组装三处共用这一个判断。
func (e Endpoint) Configured() bool { return e.BaseURL != "" }

// ClaudeEndpoint 比 openai 侧多四个模型槽。
//
// 两个端点的字段刻意不对称（spec §2.4）：四模型槽是 Claude Code 特有的
// 概念——它会自己去要 haiku 做标题生成一类的轻量活。Codex 没有这个机制，
// 强行统一等于给它编造出不存在的 opus/haiku 概念。
type ClaudeEndpoint struct {
	Endpoint
	Defaults ModelSlots `json:"defaults"`
}

// OpenAIEndpoint 只有一个默认模型。
type OpenAIEndpoint struct {
	Endpoint
	DefaultModel string `json:"default_model"`
}

// ClaudeOf / OpenAIOf 从记录解出端点。
// 空 JSON 字段返回零值——刚建的记录与 004 之前的老记录都是这样，不是错误。
func ClaudeOf(r *core.Record) ClaudeEndpoint {
	var e ClaudeEndpoint
	_ = r.UnmarshalJSONField(EndpointClaude, &e)
	return e
}

func OpenAIOf(r *core.Record) OpenAIEndpoint {
	var e OpenAIEndpoint
	_ = r.UnmarshalJSONField(EndpointOpenAI, &e)
	return e
}
```

`presets.go` 顶部加 import `"github.com/pocketbase/pocketbase/core"`。

同时把包注释里那句「key 不另存：服务配置里的 API key 就是一条 credential」
改掉——它正是本期废止的说法：

```go
// Package providers 管 AI 服务配置（M1.6 spec §2）。
//
// 一条记录 = **一家平台**，内含 claude 与 openai 两个协议端点。
// API key 直接落在这条记录上（平台级一把，端点可单独覆盖）：
// 加密落库、末四位回显、启动自检这套复用降在代码层（secretbox 包），
// 不再做成用户可见的 credentials 实体（spec §1.1 第二条）。
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./hub/internal/providers/ -run 'Configured|ClaudeOf|OpenAIOf|EndpointOfEmpty' -v
```

Expected: PASS。（`providers` 包的其余测试此时还红——Task 4 修。）

- [ ] **Step 5: 提交**

```bash
git add hub/internal/providers/
git commit -m "feat(hub): providers 加双端点类型与 Configured 判定"
```

---

### Task 3: 预设表两组端点

**Files:**
- Modify: `hub/internal/providers/presets.go`
- Test: `hub/internal/providers/presets_test.go`

**Interfaces:**
- Consumes: Task 2 的 `ModelSlots`
- Produces: `Preset` / `PresetEndpoint`（结构见 00-overview）；`Presets()` / `PresetByID()` 签名不变

- [ ] **Step 1: 写失败的测试**

改写 `hub/internal/providers/presets_test.go`（既有用例里读 `p.BaseURL` 的
全部改成 `p.Claude.BaseURL`），并追加：

```go
func TestEveryPresetHasAClaudeEndpoint(t *testing.T) {
	for _, p := range providers.Presets() {
		require.NotEmpty(t, p.Claude.BaseURL, "%s 必须有 claude 端点", p.ID)
		require.Contains(t,
			[]string{providers.AuthToken, providers.AuthAPIKey}, p.Claude.AuthField,
			"%s 的 claude auth_field 只能二选一", p.ID)
		require.NotEmpty(t, p.Claude.Models, "%s 的 claude 端点要有模型清单", p.ID)

		// 四槽要么全空（透传）要么全满，与 Store 的约束同源。
		d := p.Claude.Defaults
		require.True(t, d.Empty() || d.Full(), "%s 的四槽半填了", p.ID)
	}
}

// openai 端点可以为空（该平台没有这个口），但一旦填了就要填齐。
func TestPresetOpenAIEndpointIsCompleteWhenPresent(t *testing.T) {
	for _, p := range providers.Presets() {
		if p.OpenAI.BaseURL == "" {
			require.Empty(t, p.OpenAI.Models,
				"%s 没有 openai base_url 却填了模型清单", p.ID)
			continue
		}
		require.Equal(t, providers.DefaultOpenAIAuthField, p.OpenAI.AuthField,
			"%s 的 openai auth_field 默认就该是 OPENAI_API_KEY", p.ID)
		require.NotEmpty(t, p.OpenAI.Models, "%s 的 openai 端点要有模型清单", p.ID)
		require.Contains(t, p.OpenAI.Models, p.OpenAI.DefaultModel,
			"%s 的 openai 默认模型必须在清单里", p.ID)
		require.True(t, p.OpenAI.Defaults.Empty(),
			"%s 的 openai 端点不该有四槽——那是 Claude Code 特有的概念", p.ID)
	}
}

// 火山方舟同一个域名下两个口，用错会走计费不同的通道（spec §5.6）。
func TestVolcengineHasBothEndpointsOnDifferentPaths(t *testing.T) {
	p, ok := providers.PresetByID("volcengine")
	require.True(t, ok)
	require.Equal(t, "https://ark.cn-beijing.volces.com/api/coding", p.Claude.BaseURL,
		"/api/coding 才是 Anthropic 协议口")
	require.Equal(t, "https://ark.cn-beijing.volces.com/api/v3", p.OpenAI.BaseURL,
		"/api/v3 是 OpenAI 协议口")
}

// Anthropic 官方没有 OpenAI 协议口。UI 会显示「该平台未提供 OpenAI 端点」。
func TestAnthropicHasNoOpenAIEndpoint(t *testing.T) {
	p, ok := providers.PresetByID("anthropic")
	require.True(t, ok)
	require.Empty(t, p.OpenAI.BaseURL)
}

func TestPresetsReturnsACopy(t *testing.T) {
	a := providers.Presets()
	a[0].Name = "被改过了"
	b := providers.Presets()
	require.NotEqual(t, "被改过了", b[0].Name)
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/providers/ -run 'Preset|Volcengine|Anthropic' -v
```

Expected: FAIL，`p.Claude` 不存在。

- [ ] **Step 3: 实现结构**

Modify `hub/internal/providers/presets.go`：

```go
// PresetEndpoint 是预设表里的一个协议端点。BaseURL 为空 = 该平台没有这个口。
type PresetEndpoint struct {
	BaseURL      string     `json:"base_url"`
	AuthField    string     `json:"auth_field"`
	Models       []string   `json:"models"`
	Defaults     ModelSlots `json:"defaults"`      // 仅 claude 侧填
	DefaultModel string     `json:"default_model"` // 仅 openai 侧填
}

// Preset 是内置平台条目。编译进二进制、只读（M1.5 spec §2.3）。
//
// 一条 = 一家平台，两组端点一起带出（spec §5.6）：选预设时对话框把两个
// 端点分区都填好，用户只需要粘一次 key。
type Preset struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Claude PresetEndpoint `json:"claude"`
	OpenAI PresetEndpoint `json:"openai"`

	WebsiteURL string `json:"website_url,omitempty"`
	APIKeyURL  string `json:"api_key_url,omitempty"`
	Icon       string `json:"icon,omitempty"`
	IconColor  string `json:"icon_color,omitempty"`

	// —— 订阅域预留（M1.5 spec §9），本期只填数据不消费 ——
	CollectorType string `json:"collector_type,omitempty"`
	CollectorMode string `json:"collector_mode,omitempty"`
}
```

- [ ] **Step 4: 逐条核填七个平台的两组端点**

**这一步要对着各平台 2026-08 的当前文档核，不要照抄下面的值就交差。**
M1.5 的验收记录里，预设表是唯一出过三处数据错误的地方（火山的协议口、
MiniMax 的域名、Anthropic 的鉴权字段），代价是用错了会走计费不同的通道。

claude 侧七条**原样搬**（`BaseURL` / `AuthField` / `Models` / `Defaults`
从现有的顶层字段挪进 `Claude:` 里，一个值都不改）。

openai 侧按下表填，**每一条都要去平台文档确认**；核不到的把 `BaseURL`
留空，UI 会显示「该平台未提供 OpenAI 端点」，用户仍可手填：

| 预设 | openai `base_url`（待核） | `default_model` |
|---|---|---|
| `zhipu` | `https://open.bigmodel.cn/api/paas/v4` | 与 claude 侧主模型同名的那个 |
| `zhipu_intl` | `https://api.z.ai/api/paas/v4` | 同上 |
| `kimi` | `https://api.moonshot.cn/v1` | 同上 |
| `volcengine` | `https://ark.cn-beijing.volces.com/api/v3` | 同上 |
| `zenmux` | `https://zenmux.ai/api/v1` | 同上 |
| `minimax` | `https://api.minimax.io/v1` | 同上 |
| `anthropic` | **留空**——没有 OpenAI 协议口 | — |

`Models` 用该端点文档上列出的模型 id：**不要**直接抄 claude 侧那份。
同一家平台两个协议口的模型 id 常常不同（带 `[1m]` 一类的后缀是 Claude Code
侧的约定，OpenAI 协议口一般不认）。

结构示例（智谱那条；其余六条同形）：

```go
	{
		ID:   "zhipu",
		Name: "智谱 GLM",
		Claude: PresetEndpoint{
			BaseURL:   "https://open.bigmodel.cn/api/anthropic",
			AuthField: AuthToken,
			// [1m] 后缀开 100 万上下文窗口，官方文档同时要求把
			// CLAUDE_CODE_AUTO_COMPACT_WINDOW 调到 1000000。
			Models: []string{"glm-5.2[1m]", "glm-5.2", "glm-5-turbo", "glm-4.7"},
			Defaults: ModelSlots{
				Main: "glm-5.2[1m]", Opus: "glm-5.2[1m]",
				Sonnet: "glm-5.2[1m]", Haiku: "glm-4.7",
			},
		},
		OpenAI: PresetEndpoint{
			BaseURL:      "https://open.bigmodel.cn/api/paas/v4",
			AuthField:    DefaultOpenAIAuthField,
			Models:       []string{"glm-5.2", "glm-5-turbo", "glm-4.7"},
			DefaultModel: "glm-5.2",
		},
		WebsiteURL:    "https://open.bigmodel.cn",
		APIKeyURL:     "https://open.bigmodel.cn/usercenter/apikeys",
		Icon:          "zhipu",
		IconColor:     "#3859FF",
		CollectorType: "zhipu",
		CollectorMode: "fetch",
	},
```

火山方舟那条的注释必须跟着搬**并且更新**——它现在同时描述两个端点：

```go
	{
		ID:   "volcengine",
		Name: "火山方舟 Coding Plan",
		// 同一个域名下两个协议口，用错会走计费不同的通道：
		//   /api/coding 是 Anthropic 协议口
		//   /api/v3     是 OpenAI 协议口
		Claude: PresetEndpoint{
			BaseURL:   "https://ark.cn-beijing.volces.com/api/coding",
			AuthField: AuthToken,
			...
		},
		OpenAI: PresetEndpoint{
			BaseURL:   "https://ark.cn-beijing.volces.com/api/v3",
			AuthField: DefaultOpenAIAuthField,
			...
		},
		...
	},
```

MiniMax 那条的「国内站是 `https://api.minimaxi.com/anthropic`，建自定义
Provider 即可」注释保留，并补一句国内站的 OpenAI 口同理。

- [ ] **Step 5: 运行测试确认通过**

```bash
go test ./hub/internal/providers/ -run 'Preset|Volcengine|Anthropic' -v
```

Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add hub/internal/providers/presets.go hub/internal/providers/presets_test.go
git commit -m "feat(hub): 预设表一条平台两组端点"
```

---

### Task 4: Store 的双端点 CRUD 与两级 key 取值

**Files:**
- Modify: `hub/internal/providers/store.go`
- Test: `hub/internal/providers/store_test.go`

**Interfaces:**
- Consumes: Task 1 的字段、Task 2 的类型、`secretbox`
- Produces: `NewStore(app, key, ev)`、`Input` / `EndpointInput`（三态 `Key *string`）、
  `Store.Key(r, endpoint)`、`Store.VerifyAll()`、`ErrNoKey`；
  `Store.UsingCredential` **删除**

- [ ] **Step 1: 写失败的测试**

改写 `hub/internal/providers/store_test.go`。把构造 helper 换成：

```go
func newStore(t *testing.T) (*tests.TestApp, *providers.Store, []byte) {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	key, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	return app, providers.NewStore(app, key, events.NewWriter(app)), key
}

func strp(s string) *string { return &s }

// minimalInput 是一条只配了 claude 端点、带平台级 key 的输入。
func minimalInput(name string) providers.Input {
	return providers.Input{
		Name: name,
		Key:  strp("sk-zhipu-abcdef123456"),
		Claude: providers.EndpointInput{
			BaseURL:   "https://open.bigmodel.cn/api/anthropic",
			AuthField: providers.AuthToken,
			Models:    []string{"glm-5.2", "glm-4.7"},
		},
	}
}
```

既有用例里凡是构造 `providers.Input{BaseURL: …, Credential: …}` 的，一律改成
上面这个形状。然后追加：

```go
func TestCreateStoresBothEndpoints(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("智谱 GLM")
	in.OpenAI = providers.EndpointInput{
		BaseURL:      "https://open.bigmodel.cn/api/paas/v4",
		AuthField:    providers.DefaultOpenAIAuthField,
		Models:       []string{"glm-5.2"},
		DefaultModel: "glm-5.2",
	}
	r, err := s.Create(in)
	require.NoError(t, err)

	cl := providers.ClaudeOf(r)
	require.True(t, cl.Configured())
	require.Equal(t, "3456", cl.KeyLast4, "末四位取实际生效的那把 key")

	oa := providers.OpenAIOf(r)
	require.True(t, oa.Configured())
	require.Equal(t, "glm-5.2", oa.DefaultModel)
}

// 只配一个端点是常态，另一个必须是「未配置」而不是校验失败。
func TestCreateWithOnlyClaudeEndpoint(t *testing.T) {
	_, s, _ := newStore(t)
	r, err := s.Create(minimalInput("只有 claude"))
	require.NoError(t, err)
	require.True(t, providers.ClaudeOf(r).Configured())
	require.False(t, providers.OpenAIOf(r).Configured())
}

// 两级 key：端点级非空则覆盖平台级（spec §2.3）。
func TestEndpointKeyOverridesPlatformKey(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("两把 key 的中转")
	in.Claude.Key = strp("sk-claude-side-000111")
	in.OpenAI = providers.EndpointInput{
		BaseURL:   "https://relay.example/v1",
		AuthField: providers.DefaultOpenAIAuthField,
		Models:    []string{"gpt-5.2"}, DefaultModel: "gpt-5.2",
	}
	r, err := s.Create(in)
	require.NoError(t, err)

	got, err := s.Key(r, providers.EndpointClaude)
	require.NoError(t, err)
	require.Equal(t, "sk-claude-side-000111", got, "端点级覆盖平台级")

	got, err = s.Key(r, providers.EndpointOpenAI)
	require.NoError(t, err)
	require.Equal(t, "sk-zhipu-abcdef123456", got, "没设端点级就回落平台级")

	require.Equal(t, "0111", providers.ClaudeOf(r).KeyLast4)
	require.Equal(t, "3456", providers.OpenAIOf(r).KeyLast4)
}

// 「配置了 base_url 的端点必须能解出一把 key」，与四槽约束同级（spec §2.3）。
func TestCreateRejectsConfiguredEndpointWithoutAnyKey(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("没 key")
	in.Key = nil
	_, err := s.Create(in)
	require.ErrorIs(t, err, providers.ErrNoKey)
	require.Contains(t, err.Error(), "claude")
}

// 只有端点级 key、没有平台级也合法——中转平台两个口两把 key 的情形。
func TestEndpointOnlyKeyIsEnough(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("只有端点级")
	in.Key = nil
	in.Claude.Key = strp("sk-claude-only-9988")
	r, err := s.Create(in)
	require.NoError(t, err)
	got, err := s.Key(r, providers.EndpointClaude)
	require.NoError(t, err)
	require.Equal(t, "sk-claude-only-9988", got)
}

func TestKeyRejectsShortValue(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("太短")
	in.Key = strp("1234567") // 7 < MinValueLen
	_, err := s.Create(in)
	require.ErrorIs(t, err, secretbox.ErrShortValue)
}

// Key 的三态（spec §5.2 的「留空则不修改，填写即替换」）。
func TestUpdateKeyTriState(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("三态")
	in.Claude.Key = strp("sk-claude-side-000111")
	r, err := s.Create(in)
	require.NoError(t, err)

	// nil = 不修改
	in.Key, in.Claude.Key = nil, nil
	in.Note = "改了备注"
	r, err = s.Update(r.Id, in)
	require.NoError(t, err)
	got, err := s.Key(r, providers.EndpointClaude)
	require.NoError(t, err)
	require.Equal(t, "sk-claude-side-000111", got, "留空不得清掉已有的 key")

	// "" = 清空端点级 → 回落平台级
	in.Claude.Key = strp("")
	r, err = s.Update(r.Id, in)
	require.NoError(t, err)
	got, err = s.Key(r, providers.EndpointClaude)
	require.NoError(t, err)
	require.Equal(t, "sk-zhipu-abcdef123456", got)
	require.Empty(t, providers.ClaudeOf(r).KeyLast4, "清空后末四位也要清掉")

	// 非空 = 替换
	in.Claude.Key = strp("sk-claude-new-777777")
	r, err = s.Update(r.Id, in)
	require.NoError(t, err)
	got, err = s.Key(r, providers.EndpointClaude)
	require.NoError(t, err)
	require.Equal(t, "sk-claude-new-777777", got)
}

func TestKeyReportsNoKeyForUnconfiguredSide(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("没平台级 key")
	in.Key = nil
	in.Claude.Key = strp("sk-claude-only-9988")
	r, err := s.Create(in)
	require.NoError(t, err)

	_, err = s.Key(r, providers.EndpointOpenAI)
	require.ErrorIs(t, err, providers.ErrNoKey)
}

// 四槽半填仍然拒绝——M1.5 的约束原样继承（spec §7）。
func TestHalfFilledSlotsStillRejected(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("半填")
	in.Claude.Defaults = providers.ModelSlots{Main: "glm-5.2"}
	_, err := s.Create(in)
	require.Error(t, err)
	require.Contains(t, err.Error(), "四个模型槽")
}

// openai 端点的 auth_field 是自由文本，不做二选一校验（spec §2.4）。
func TestOpenAIAuthFieldIsFreeText(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("自定义鉴权字段")
	in.OpenAI = providers.EndpointInput{
		BaseURL:   "https://relay.example/v1",
		AuthField: "X_CUSTOM_TOKEN",
		Models:    []string{"gpt-5.2"}, DefaultModel: "gpt-5.2",
	}
	r, err := s.Create(in)
	require.NoError(t, err)
	require.Equal(t, "X_CUSTOM_TOKEN", providers.OpenAIOf(r).AuthField)
}

// openai 端点留空时不填 auth_field 也行，apply 会补上默认值。
func TestOpenAIAuthFieldDefaults(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("默认鉴权字段")
	in.OpenAI = providers.EndpointInput{
		BaseURL: "https://relay.example/v1",
		Models:  []string{"gpt-5.2"}, DefaultModel: "gpt-5.2",
	}
	r, err := s.Create(in)
	require.NoError(t, err)
	require.Equal(t, providers.DefaultOpenAIAuthField, providers.OpenAIOf(r).AuthField)
}

// 启动自检：解不开就拒绝启动（spec §2.6）。这条不能丢——它防的是
// 「从备份恢复到新机器时忘了带 secret.key」。
func TestVerifyAllRejectsWrongMasterKey(t *testing.T) {
	app, s, _ := newStore(t)
	_, err := s.Create(minimalInput("智谱 GLM"))
	require.NoError(t, err)
	require.NoError(t, s.VerifyAll(), "同一把钥匙必须通过")

	other, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	bad := providers.NewStore(app, other, events.NewWriter(app))

	err = bad.VerifyAll()
	require.Error(t, err)
	require.Contains(t, err.Error(), "智谱 GLM")
	require.Contains(t, err.Error(), secretbox.KeyFileName,
		"错误信息里要有备份提示，指名 secret.key")
	require.Contains(t, err.Error(), secretbox.EnvKeyName)
}

func TestVerifyAllNamesTheFailingEndpoint(t *testing.T) {
	app, s, _ := newStore(t)
	in := minimalInput("智谱 GLM")
	in.Key = nil
	in.Claude.Key = strp("sk-claude-only-9988")
	_, err := s.Create(in)
	require.NoError(t, err)

	other, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	err = providers.NewStore(app, other, events.NewWriter(app)).VerifyAll()
	require.Error(t, err)
	require.Contains(t, err.Error(), "claude 端点")
}

func TestVerifyAllPassesOnEmptyTable(t *testing.T) {
	_, s, _ := newStore(t)
	require.NoError(t, s.VerifyAll())
}
```

既有的 `TestUsingCredential*` 用例**整组删掉**——引用计数保护随凭据实体一起
消失（spec §2.7）：key 现在就在 provider 里，删 provider 就是删 key，
没有第二个实体可以被误删。`Store.BoundBy` 与它的用例**保留不动**。

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/providers/ -v
```

Expected: FAIL —— `NewStore` 少一个参数、`Input.Claude` 不存在、`Store.Key` 未定义。

- [ ] **Step 3: 实现**

Modify `hub/internal/providers/store.go`：

```go
var (
	ErrNotFound     = errors.New("providers: 服务配置不存在")
	ErrInUse        = errors.New("providers: 服务配置仍被绑定")
	ErrBadAuthField = errors.New("providers: auth_field 非法")
	ErrBadBaseURL   = errors.New("providers: base_url 非法")
	// ErrNoKey：某个配置了 base_url 的端点两级都解不出 key。
	// 与「四槽要么全空要么全满」同级的配置错误（spec §2.3）。
	ErrNoKey = errors.New("providers: 端点没有可用的 key")
)

// EndpointInput 的 Key 是三态（spec §5.2「留空则不修改，填写即替换」）：
//
//	nil  = 不修改（编辑态密码框留空）
//	""   = 清空（端点级清空即回落平台级）
//	非空 = 替换
//
// 用 *string 而不是 (string, bool) 一对字段，是因为它要原样从 HTTP 请求体
// 反序列化过来：JSON 里字段缺席 → nil，字段是 "" → 清空。两种意图在
// wire 上就分得开，路由层不必再发明一套约定。
type EndpointInput struct {
	BaseURL      string
	AuthField    string
	Models       []string
	Key          *string
	Defaults     ModelSlots // 仅 claude 端点有意义
	DefaultModel string     // 仅 openai 端点有意义
}

// Input 是建 / 改一条服务配置需要的全部字段。
type Input struct {
	Name   string
	Preset string
	Note   string
	Key    *string // 平台级，三态同 EndpointInput.Key
	Claude EndpointInput
	OpenAI EndpointInput
}

type Store struct {
	app core.App
	key []byte
	ev  *events.Writer
}

func NewStore(app core.App, key []byte, ev *events.Writer) *Store {
	return &Store{app: app, key: key, ev: ev}
}
```

`Create` / `Update` 的骨架不变，但 `validate` 与 `apply` 都要能拿到记录
（清空 / 保留 key 依赖旧值），因此 `apply` 改成带错误返回、并在写完之后
用**记录上的实际值**再校验一次：

```go
func (s *Store) Create(in Input) (*core.Record, error) {
	c, err := s.app.FindCollectionByNameOrId("providers")
	if err != nil {
		return nil, fmt.Errorf("providers: 找不到 collection: %w", err)
	}
	r := core.NewRecord(c)
	if err := s.applyAndValidate(r, in); err != nil {
		return nil, err
	}
	if err := s.app.Save(r); err != nil {
		return nil, fmt.Errorf("providers: 创建 %s: %w", in.Name, err)
	}
	s.write(events.KindProviderCreated, r)
	return r, nil
}

// Update 改任一端点的 base_url / 模型 / key。
//
// **不产生新 Revision**（M1.5 spec §2.2）：Provider 的当前值是活的，跟版本无关。
// 下发由调用方发一次 ConfigNotify 完成（见 configsync.NotifyProvider）。
func (s *Store) Update(id string, in Input) (*core.Record, error) {
	r, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if err := s.applyAndValidate(r, in); err != nil {
		return nil, err
	}
	if err := s.app.Save(r); err != nil {
		return nil, fmt.Errorf("providers: 更新 %s: %w", id, err)
	}
	s.write(events.KindProviderUpdated, r)
	return r, nil
}

// applyAndValidate 把 Input 写进记录，再对**记录上的实际值**做校验。
//
// 顺序是「先写后校验」而不是反过来：key 的三态里有「不修改」这一档，
// 「这个端点有没有 key」只有在合并了记录上的旧密文之后才知道。
func (s *Store) applyAndValidate(r *core.Record, in Input) error {
	if strings.TrimSpace(in.Name) == "" {
		return fmt.Errorf("providers: 需要 name")
	}
	if err := validateEndpointInput(EndpointClaude, in.Claude); err != nil {
		return err
	}
	if err := validateEndpointInput(EndpointOpenAI, in.OpenAI); err != nil {
		return err
	}

	r.Set("name", strings.TrimSpace(in.Name))
	r.Set("preset", in.Preset)
	r.Set("note", in.Note)

	if err := s.applyKey(r, "key_cipher", "key_last4", in.Key); err != nil {
		return err
	}
	if err := s.applyKey(r, "claude_key_cipher", "", in.Claude.Key); err != nil {
		return err
	}
	if err := s.applyKey(r, "openai_key_cipher", "", in.OpenAI.Key); err != nil {
		return err
	}

	claude := ClaudeEndpoint{
		Endpoint: Endpoint{
			// 归一化后存储（M1.5 spec §6.2）：反查靠它，
			// 不能把用户敲的尾斜杠带进库。
			BaseURL:   NormalizeURL(in.Claude.BaseURL),
			AuthField: in.Claude.AuthField,
			Models:    orEmpty(in.Claude.Models),
		},
		Defaults: in.Claude.Defaults,
	}
	openai := OpenAIEndpoint{
		Endpoint: Endpoint{
			BaseURL:   NormalizeURL(in.OpenAI.BaseURL),
			AuthField: in.OpenAI.AuthField,
			Models:    orEmpty(in.OpenAI.Models),
		},
		DefaultModel: in.OpenAI.DefaultModel,
	}
	if openai.AuthField == "" {
		openai.AuthField = DefaultOpenAIAuthField
	}
	r.Set("claude", claude)
	r.Set("openai", openai)

	// 末四位跟着**实际生效的那把 key** 走，两级取值之后才算得出来。
	if err := s.stampLast4(r); err != nil {
		return err
	}
	return nil
}

// applyKey 按三态写一处密文。last4Field 为空表示这一处的末四位存在端点 JSON 里
// （由 stampLast4 统一回填），不需要单独的顶层字段。
func (s *Store) applyKey(r *core.Record, cipherField, last4Field string, v *string) error {
	if v == nil {
		return nil // 不修改
	}
	if *v == "" {
		r.Set(cipherField, "")
		if last4Field != "" {
			r.Set(last4Field, "")
		}
		return nil
	}
	if len(*v) < secretbox.MinValueLen {
		return fmt.Errorf("%w: 至少 %d 个字符", secretbox.ErrShortValue, secretbox.MinValueLen)
	}
	enc, err := secretbox.Encrypt(s.key, *v)
	if err != nil {
		return err
	}
	r.Set(cipherField, enc)
	if last4Field != "" {
		r.Set(last4Field, secretbox.Last4(*v))
	}
	return nil
}

// stampLast4 给两个端点回填「实际生效的那把 key 的末四位」，
// 顺带把「配了 base_url 却解不出 key」这条校验做掉。
func (s *Store) stampLast4(r *core.Record) error {
	for _, ep := range []string{EndpointClaude, EndpointOpenAI} {
		configured := false
		switch ep {
		case EndpointClaude:
			configured = ClaudeOf(r).Configured()
		case EndpointOpenAI:
			configured = OpenAIOf(r).Configured()
		}

		v, err := s.Key(r, ep)
		if err != nil {
			if !configured {
				// 没配的端点没有 key 很正常，末四位清空。
				if err := setEndpointLast4(r, ep, ""); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("%w：%s 端点配了 base_url，"+
				"但平台级与端点级都没有 key", ErrNoKey, ep)
		}
		if err := setEndpointLast4(r, ep, secretbox.Last4(v)); err != nil {
			return err
		}
	}
	return nil
}

func setEndpointLast4(r *core.Record, ep, last4 string) error {
	switch ep {
	case EndpointClaude:
		e := ClaudeOf(r)
		e.KeyLast4 = last4
		r.Set(EndpointClaude, e)
	case EndpointOpenAI:
		e := OpenAIOf(r)
		e.KeyLast4 = last4
		r.Set(EndpointOpenAI, e)
	default:
		return fmt.Errorf("providers: 未知端点 %q", ep)
	}
	return nil
}

// Key 按两级取值解出某端点实际使用的 key（spec §2.3）：
// 端点级密文非空则用它，否则回落平台级。
//
// 为什么允许覆盖而不是强制平台级一把：智谱 / 火山 / Kimi 的两个协议口确实
// 共用同一把 key，但中转类平台不一定；而「需要两把 key 就建两条 provider」
// 会把同一家平台重新拆成两条记录，正好抵消掉本期要解决的问题。
//
// 为什么不做成「只有端点级」：那样共用一把 key 的平台要粘贴两遍、轮换时
// 要改两处——这是本期最常见的情形，不该为边缘情形付代价。
func (s *Store) Key(r *core.Record, endpoint string) (string, error) {
	var field string
	switch endpoint {
	case EndpointClaude:
		field = "claude_key_cipher"
	case EndpointOpenAI:
		field = "openai_key_cipher"
	default:
		return "", fmt.Errorf("providers: 未知端点 %q", endpoint)
	}
	if c := r.GetString(field); c != "" {
		return secretbox.Decrypt(s.key, c)
	}
	if c := r.GetString("key_cipher"); c != "" {
		return secretbox.Decrypt(s.key, c)
	}
	return "", fmt.Errorf("%w: %s 的 %s 端点", ErrNoKey, r.GetString("name"), endpoint)
}

// VerifyAll 在启动时逐条解密自检（spec §2.6）。
//
// **这条不能丢。** 它防的是「从备份恢复到新机器时忘了带 secret.key」——
// 最可能的翻车场景。宁可开不了机，也不能让用户以为一切正常，然后把空值
// 下发到全机队。
func (s *Store) VerifyAll() error {
	recs, err := s.app.FindAllRecords("providers")
	if err != nil {
		return fmt.Errorf("providers: 读取服务配置: %w", err)
	}
	for _, r := range recs {
		for _, pair := range []struct{ field, label string }{
			{"key_cipher", "平台级"},
			{"claude_key_cipher", "claude 端点"},
			{"openai_key_cipher", "openai 端点"},
		} {
			c := r.GetString(pair.field)
			if c == "" {
				continue
			}
			if _, err := secretbox.Decrypt(s.key, c); err != nil {
				return fmt.Errorf("providers: 服务配置 %q 的 %s 密钥解密失败——主密钥不匹配。"+
					"若是从备份恢复，请把原机器的 %s 或 %s 一并带过来: %w",
					r.GetString("name"), pair.label,
					secretbox.KeyFileName, secretbox.EnvKeyName, err)
			}
		}
	}
	return nil
}

// validateEndpointInput 只看输入本身能不能自洽；「有没有 key」要等
// 合并进记录之后才判得了（见 stampLast4）。
func validateEndpointInput(name string, in EndpointInput) error {
	if strings.TrimSpace(in.BaseURL) == "" {
		return nil // 未配置的端点不校验其余字段
	}
	if HostOf(in.BaseURL) == "" {
		return fmt.Errorf("%w: %s 端点的 %q 解析不出 host", ErrBadBaseURL, name, in.BaseURL)
	}
	if name == EndpointClaude {
		// claude 端点的 auth_field 是二选一枚举（M1.5 spec §3.3）：
		// 它是 Claude Code 的 settings.json env 键名问题。
		if in.AuthField != AuthToken && in.AuthField != AuthAPIKey {
			return fmt.Errorf("%w: %q（只允许 %s / %s）",
				ErrBadAuthField, in.AuthField, AuthToken, AuthAPIKey)
		}
		// 半填的四槽是配置错误：只钉主模型会让 Claude Code 拿 claude-haiku-*
		// 去打人家的 endpoint（M1.5 spec §2.3）。
		if !in.Defaults.Empty() && !in.Defaults.Full() {
			return fmt.Errorf("providers: 四个模型槽必须要么全空（透传）要么全满")
		}
	}
	// openai 端点的 auth_field 是自由文本，不校验（spec §2.4）。
	return nil
}

func orEmpty(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
```

`Store.UsingCredential` 整个方法**删掉**（spec §2.7）。`BoundBy`、`Get`、
`Delete` 不动。`Delete` 里的注释「与凭据的删除保护同理」改成：

```go
// Delete 删服务配置。被任何配置集的 head_provider 或 draft_binding 指向时拒绝。
//
// 这是本期唯一的删除保护：key 现在就在 provider 里，删 provider 就是删 key，
// 没有第二个实体可以被误删（spec §2.7）。删掉一条正被指着的 Provider 会让
// 全机队在下次重注入时拿到空值。
```

`store.go` 的 import 里加 `"github.com/FlintyLemming/orciny/hub/internal/secretbox"`。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./hub/internal/providers/ -v
```

Expected: PASS。`hub` 其余包此时还编译不过（`NewStore` 少参数、
`Input.BaseURL` 没了），04–08 会依次修好。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/providers/store.go hub/internal/providers/store_test.go
git commit -m "feat(hub): providers 双端点 CRUD 与两级 key 取值"
```

---

### Task 5: `MatchBaseURL` 只扫 claude 端点

**Files:**
- Modify: `hub/internal/providers/store.go`
- Test: `hub/internal/providers/store_test.go`（追加）

**Interfaces:**
- Consumes: Task 2 的 `ClaudeOf`、Task 3 的 `Preset.Claude`
- Produces: 签名不变的 `MatchBaseURL(raw string) (Match, error)`

**为什么只扫 claude 端点**（spec §1.3）：反查链的唯一消费者是绑定漂移，
而漂移源只有 `.claude/**`。给一个不会产生漂移的端点建反查链是纯粹的空转，
而且会误报——同一家平台两个口的 host 相同，openai 端点参与 host 匹配会让
「可能是」那一档变得毫无信息量。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/providers/store_test.go`：

```go
func TestMatchBaseURLScansClaudeEndpointOnly(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("智谱 GLM")
	in.OpenAI = providers.EndpointInput{
		BaseURL:   "https://openai-only.example/v1",
		AuthField: providers.DefaultOpenAIAuthField,
		Models:    []string{"gpt-5.2"}, DefaultModel: "gpt-5.2",
	}
	r, err := s.Create(in)
	require.NoError(t, err)

	// claude 端点：精确命中
	m, err := s.MatchBaseURL("https://open.bigmodel.cn/api/anthropic/")
	require.NoError(t, err)
	require.Equal(t, providers.MatchProvider, m.Kind)
	require.True(t, m.Exact)
	require.Equal(t, r.Id, m.ProviderID)

	// openai 端点的地址**不参与**反查
	m, err = s.MatchBaseURL("https://openai-only.example/v1")
	require.NoError(t, err)
	require.Equal(t, providers.MatchNone, m.Kind)
}

func TestMatchBaseURLFallsBackToPresetClaudeEndpoint(t *testing.T) {
	_, s, _ := newStore(t)
	m, err := s.MatchBaseURL("https://api.moonshot.cn/anthropic")
	require.NoError(t, err)
	require.Equal(t, providers.MatchPreset, m.Kind)
	require.True(t, m.Exact)
	require.Equal(t, "kimi", m.PresetID)
}
```

既有的 `MatchBaseURL` 用例（host 匹配降级、都不命中）**保留**，只把里面
构造 provider 的地方换成 `minimalInput`。

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/providers/ -run MatchBaseURL -v
```

Expected: FAIL —— 实现还在读 `r.GetString("base_url")` 与 `p.BaseURL`。

- [ ] **Step 3: 实现**

Modify `MatchBaseURL`：`got := r.GetString("base_url")` → `got := ClaudeOf(r).BaseURL`；
`p.BaseURL` → `p.Claude.BaseURL`（两处：精确与 host）。并在文档注释里补一句：

```go
// MatchBaseURL 做反查：先在已有 Provider 的 **claude 端点**里精确匹配，
// 再 host 匹配，然后在内置预设的 claude 端点里精确匹配、host 匹配，
// 都不中返回 MatchNone（M1.5 spec §6.3 / §6.4）。
//
// **只扫 claude 端点**（M1.6 spec §1.3）：反查链的唯一消费者是绑定漂移，
// 而漂移源只有 .claude/**。让 openai 端点参与 host 匹配还会让「可能是」
// 那一档失去信息量——同一家平台两个口的 host 本来就相同。
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./hub/internal/providers/ -v
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/providers/
git commit -m "feat(hub): base_url 反查只扫 claude 端点"
```

---

### Task 6: hub 接线（`NewStore` 的密钥 + 双份启动自检）

**Files:**
- Modify: `hub/hub.go`
- Modify: `hub/seed_testing.go`
- Test: `hub/hub_test.go`（追加一条）

**Interfaces:**
- Consumes: Task 4 的 `providers.NewStore` / `VerifyAll`
- Produces: `h.provs` 带主密钥；`Hub.SeedProvider` 不再建凭据

- [ ] **Step 1: 写失败的测试**

追加到 `hub/hub_test.go`：

```go
// 启动自检必须排在接客之前：一台解不开自己 key 的 hub 不该上线，
// 它会把空值下发到全机队（M1.6 spec §2.6）。
func TestHubRefusesToServeWithWrongMasterKey(t *testing.T) {
	dir := t.TempDir()

	// 第一次起：生成主密钥，建一条 provider。
	h1 := newHubAt(t, dir)
	_ = h1.SeedProvider(t, "智谱 GLM", "https://open.bigmodel.cn/api/anthropic",
		"sk-zhipu-abcdef123456")
	stopHub(t, h1)

	// 换一把主密钥再起：必须失败，且错误信息能指名是谁。
	t.Setenv(secretbox.EnvKeyName,
		base64.StdEncoding.EncodeToString(make([]byte, 32)))
	err := startHubAt(t, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "智谱 GLM")
}
```

`newHubAt` / `startHubAt` / `stopHub` 三个 helper：`hub_test.go` 里已有等价的
构造方式，照它现有的写法包一层即可（不要新引入测试基建）。若既有测试是用
`tests.NewTestApp` + `hub.Attach` 起的，就沿用同一条路径，只把数据目录固定成
同一个 `dir` 以便第二次启动读到同一份 `providers` 表。

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/ -run 'MasterKey' -v
```

Expected: FAIL —— 现在只有 `credentials.Store.VerifyAll` 在跑，它扫的是
`credentials` 表，里面没有东西，于是 hub 照常启动。

- [ ] **Step 3: 实现**

`hub/hub.go`：`h.provs` 的构造挪到主密钥之后，并加一次自检。

```go
		// 主密钥。必须排在 ws 之前：一台解不开自己 key 的 hub 不该接客
		// ——它会把空值下发到全机队（M1.6 spec §2.6）。
		key, err := secretbox.LoadMasterKey(e.App.DataDir())
		if err != nil {
			return fmt.Errorf("加载主密钥: %w", err)
		}
		h.creds = credentials.NewStore(e.App, key, h.events)
		if err := h.creds.VerifyAll(); err != nil {
			return err
		}
		h.vars = variables.NewStore(e.App)

		h.blobs = blobs.New(e.App)
		h.provs = providers.NewStore(e.App, key, h.events)
		if err := h.provs.VerifyAll(); err != nil {
			return err
		}
```

（`h.creds` 与它的 `VerifyAll` 在 08 才删掉；此刻两份自检并存，
`credentials` 表在 `005` 跑过之前还有存量数据要看。）

`hub/seed_testing.go` 的 `SeedProvider` 改成不建凭据：

```go
// SeedProvider 建一条 AI 服务配置（只配 claude 端点，key 内联在平台级），
// 返回 provider id。
func (h *Hub) SeedProvider(t *testing.T, name, baseURL, key string) string {
	t.Helper()
	id, err := h.CreateProvider(providers.Input{
		Name: name,
		Key:  &key,
		Claude: providers.EndpointInput{
			BaseURL:   baseURL,
			AuthField: providers.AuthToken,
			Models:    []string{"glm-5.1", "glm-4.7"},
		},
	})
	require.NoError(t, err, "建服务配置")
	return id
}
```

`ProviderInputOf` 同步改造（测试里改 base_url 用它，不能把 key 冲掉——
`Key` 留 `nil` 正是「不修改」）：

```go
// ProviderInputOf 读回一条 Provider 的当前值，只把 claude 端点的 base_url
// 换成新的。三处 Key 都留 nil = 不修改，免得测试无意间把 key 冲掉。
func (h *Hub) ProviderInputOf(t *testing.T, providerID, baseURL string) providers.Input {
	t.Helper()
	r, err := h.provs.Get(providerID)
	require.NoError(t, err)
	cl := providers.ClaudeOf(r)
	oa := providers.OpenAIOf(r)
	return providers.Input{
		Name:   r.GetString("name"),
		Preset: r.GetString("preset"),
		Note:   r.GetString("note"),
		Claude: providers.EndpointInput{
			BaseURL: baseURL, AuthField: cl.AuthField,
			Models: cl.Models, Defaults: cl.Defaults,
		},
		OpenAI: providers.EndpointInput{
			BaseURL: oa.BaseURL, AuthField: oa.AuthField,
			Models: oa.Models, DefaultModel: oa.DefaultModel,
		},
	}
}
```

`strconv` 那个 import 如果没别的用处，一并删掉。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/ -v
```

Expected: 这一条新用例 PASS。`hub` 包里其余用例若因 04/05 还没做而红
（例如 `configsets.Validate` 的签名），**留给对应子计划**，不要在这里
顺手改掉——那会让两个子计划的 diff 互相纠缠。

- [ ] **Step 5: 提交**

```bash
git add hub/hub.go hub/seed_testing.go hub/hub_test.go
git commit -m "feat(hub): provider 密文启动自检接线"
```
