# Orciny M1.5 · 服务绑定 —— 实现计划（统括）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **本文件是索引与全局契约，不含可执行任务。** 实际任务在 `01-*.md` … `07-*.md` 七个子计划里。每次只交给执行者**一个子计划文件 + 本文件**。

**Goal:** 把「用哪家的哪个模型」从配置文件文本里提出来变成一等实体——配置集绑定一个 AI 服务配置（Provider）+ 四个模型槽，`settings.json` 里只落 `{{provider.*}}` 占位符，真实值在 agent 落盘时注入；改 Provider 重注入全机队而**不产生新 Revision**。

**Architecture:** 新增一个 `providers` collection 与一个编译进二进制的只读预设表；`revisions.binding` / `config_sets.draft_binding` 冻结「绑定引用」（值不进版本，与凭据严格同构）；`protocol` 加一个 `provider` 占位符前缀与六个内置名，`ConfigSnapshot` 加一个 `Provider` 字段；agent 侧的 `render.Render` **一行不动**，只在构造 lookup 与还原分档的地方接线；漂移侧识别「基线是占位符、现状是字面值」的绑定漂移，禁用收编并提供反查改绑。

**Tech Stack:** 沿用 M1，**不新增任何依赖**。Go 1.26 · PocketBase v0.39.9 · fxamacker/cbor v2.9.2 · tidwall/gjson + sjson（auth_field 一键修复的原地 JSON 编辑）· blang/semver（定向版本门槛）· testify；前端 React 19 + Vite + Tailwind v4 + nanostores + Lingui + Vitest。

**Spec:** [M1.5 工程设计](../../specs/2026-08-21-provider-binding-design.md)（下称「spec」，条目号如 §5.3 均指它）

**上位/前置文档：** [M1 工程设计](../../specs/2026-07-31-m1-config-loop-design.md)（称「M1 spec」）· [M1 实现计划](../2026-07-31-m1-config-loop/00-overview.md) · [M1 验收记录](../2026-07-31-m1-config-loop/acceptance.md) · [产品设计文档](../../../PRODUCT-DESIGN.md)

---

## Global Constraints

以下约束对**每一个任务**生效，子计划不再逐条重复。**M0 与 M1 计划的 Global Constraints 全部继续有效**，此处只列 M1.5 新增或加强的部分。

**模块边界（不变）**

- `agent/**` 与 `hub/**` 之间**不允许任何直接依赖**。本期新增的共享面只有 `protocol/`：`RefProvider`、`ProviderKeys`、`ConfigSnapshot.Provider`。
- `protocol/` 不含业务逻辑、不 import PocketBase。占位符包只放词法与「按词法结果回填」，**取值逻辑留在两侧各自的 render / configsync**。
- 数据迁移**追加式**：新建 `hub/internal/migrations/003_providers.go`，绝不修改 `001_initial.go` 与 `002_configsets.go`。
- 新 collection 的 API rule 全部 `nil`（仅 superuser 可访问）。

**协议纪律**

- `ConfigSnapshot` 的字段编号**只增不改不复用**。本期只加 `10`。
- 新字段一律 `omitempty`，保证旧 agent 收到时静默忽略（spec §3.4）。
- **不动 `orciny.MinAgentVersion`**（保持 `0.1.0`）。版本门槛是定向的，在 `configsync` 组装快照时判定（spec §10）。

**版本号**

- `orciny.Version` 由 `"0.1.0"` 抬到 `"0.2.0"`（子计划 04 Task 4）。
- `orciny.MinProviderAgentVersion = semver.MustParse("0.2.0")`：能渲染 `{{provider.*}}` 的最低 agent 版本。

**核心不变量（每个子计划的测试都要能指回这一条）**

| 动作 | 是否产生新 Revision |
|---|---|
| 换绑定（配置集从 A 切到 B、换模型） | **是** |
| 改 Provider 内部（base_url、追加模型、轮换 key） | **否**，走 `ConfigNotify` 重注入 |

**凭据纪律（加强）**

- `{{provider.auth_token}}` 在下发与还原时**一律按凭据处理**：进凭据档、不落 Revision、还原失败即 `Truncated` 且不上报内容（spec §3.2 / §4.2）。**不因为它顶着 `provider.` 前缀就当普通值。**
- 其余五个 provider 值（`base_url` 与四个模型槽）进变量档，受 `render.MinVarLen = 4` 约束。
- **被 Provider 引用的凭据不可删除**（spec §5.3）。这是本设计里唯一会造成数据面损坏的遗漏点，必须有测试。
- 快照只下发 `revisions.refs.provider_keys` 里出现过的键；没引用 `{{provider.model_opus}}` 就不下发该键。

**数值常量（逐字符照抄）**

- provider 内置名恰好六个，顺序固定：`base_url`, `auth_token`, `model`, `model_opus`, `model_sonnet`, `model_haiku`。
- `RefProvider RefKind = 4`（`RefCred=1` / `RefVar=2` / `RefMachine=3` 已占）。
- `ConfigSnapshot.Provider` 的 CBOR keyasint 编号 = `10`。
- `auth_field` 只有两个合法值：`ANTHROPIC_AUTH_TOKEN`、`ANTHROPIC_API_KEY`。
- `settings.json` 的受管相对路径恒为 `.claude/settings.json`（manifest 根是 HOME）。

**测试纪律（不变）**

- 测试里**不允许 `time.Sleep`**；等异步效果用 `require.Eventually(t, cond, 2*time.Second, 5*time.Millisecond)`，条件函数里不能调 `require`。
- 断言 PocketBase 的 JSON 字段子键必须 `rec.UnmarshalJSONField("field", &v)`；`rec.GetString("field.key")` 恒返回空串。
- 测试命令：`go test -tags=testing ./...`（Go）、`cd hub/internal/site && npm test`（前端）。
- 一切碰 `~/.claude` 的测试必须显式传 managed home 临时目录。

**提交**

- 每个任务最后一步是一次 commit，Conventional Commits，正文中文。一个任务一次 commit，不要攒。
- 前端构建会脏 `hub/internal/site/dist/index.html`，提交前 `git checkout -- hub/internal/site/dist/index.html`。

---

## 全局接口契约

下面这些类型跨子计划共用。**先定义者按此逐字符落地，后使用者按此调用**，不得改名。

### `protocol`（子计划 01）

```go
const RefProvider RefKind = 4 // String() 返回 "provider"

// ProviderKeys 是 {{provider.*}} 允许的全部名字。顺序即 UI 展示顺序。
var ProviderKeys = []string{
	"base_url", "auth_token", "model", "model_opus", "model_sonnet", "model_haiku",
}

type ConfigSnapshot struct {
	// ... 0..9 不动
	Provider map[string]string `cbor:"10,keyasint,omitempty"` // 六个内置名 → 真实值
}
```

### `agent/internal/render`（子计划 01）

```go
// Values 是还原时可用的全部值。provider 的六个值在内部按 spec §4.2 分派：
// auth_token 进凭据档，其余五个进变量档。
type Values struct {
	Creds    map[string]string
	Vars     map[string]string
	Provider map[string]string
}

func Restore(content []byte, v Values) RestoreResult
func RestoreWithBase(content, base []byte, v Values) RestoreResult
```

`render.Render` 的签名不变：`func(content []byte, look func(protocol.Ref) (string, bool)) ([]byte, error)`。

### `agent/internal/secrets`（子计划 01）

```go
type File struct {
	Creds    map[string]string `json:"creds"`
	Vars     map[string]string `json:"vars"`
	Machine  map[string]string `json:"machine"`
	Provider map[string]string `json:"provider"`
}
// Lookup 新增 case protocol.RefProvider → f.Provider[r.Name]
```

### `hub/internal/providers`（子计划 02）

```go
// ModelSlots 是四个模型槽。四空 = 透传模式（spec §2.3）。
type ModelSlots struct {
	Main   string `json:"main"`
	Opus   string `json:"opus"`
	Sonnet string `json:"sonnet"`
	Haiku  string `json:"haiku"`
}

func (m ModelSlots) Empty() bool // 四个都空
func (m ModelSlots) Full() bool  // 四个都非空

// Binding 是配置集的服务绑定。冻结进 revisions.binding，
// 草稿态在 config_sets.draft_binding。
type Binding struct {
	Provider string     `json:"provider"` // providers 记录 id
	Models   ModelSlots `json:"models"`
}

// 鉴权字段名。占位符替的是值，替不了键名（spec §3.3）。
const (
	AuthToken  = "ANTHROPIC_AUTH_TOKEN"
	AuthAPIKey = "ANTHROPIC_API_KEY"
)

type Preset struct {
	ID, Name, BaseURL, AuthField string
	Models                       []string
	Defaults                     ModelSlots
	WebsiteURL, APIKeyURL        string
	Icon, IconColor              string
	CollectorType, CollectorMode string // 订阅域预留（spec §9），本期只填不消费
}

func Presets() []Preset
func PresetByID(id string) (Preset, bool)

// NormalizeURL 归一化 base_url：去尾斜杠 + host 小写 + 去默认端口（spec §6.4）。
func NormalizeURL(raw string) string
// HostOf 返回归一化后的 host（含非默认端口）。
func HostOf(raw string) string

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

type Store struct{ /* app, ev */ }
func NewStore(app core.App, ev *events.Writer) *Store

func (s *Store) Create(in Input) (*core.Record, error)
func (s *Store) Update(id string, in Input) (*core.Record, error)
func (s *Store) Delete(id string) error
func (s *Store) Get(id string) (*core.Record, error)
func (s *Store) BoundBy(id string) ([]string, error)            // config_set 记录 id
func (s *Store) UsingCredential(credID string) ([]string, error) // provider 记录 id
func (s *Store) MatchBaseURL(raw string) (Match, error)

// Match 是反查结果（spec §6.3 / §6.4）。
type Match struct {
	Kind         string `json:"kind"`  // MatchProvider / MatchPreset / MatchNone
	Exact        bool   `json:"exact"` // false = 仅 host 匹配，UI 措辞降级为「可能是」
	ProviderID   string `json:"provider_id,omitempty"`
	ProviderName string `json:"provider_name,omitempty"`
	PresetID     string `json:"preset_id,omitempty"`
	PresetName   string `json:"preset_name,omitempty"`
}

const (
	MatchProvider = "provider"
	MatchPreset   = "preset"
	MatchNone     = "none"
)

var (
	ErrNotFound     = errors.New("providers: 服务配置不存在")
	ErrInUse        = errors.New("providers: 服务配置仍被绑定")
	ErrBadAuthField = errors.New("providers: auth_field 非法")
	ErrBadBaseURL   = errors.New("providers: base_url 非法")
)
```

### `hub/internal/configsets`（子计划 03）

```go
type Refs struct {
	Creds        []string `json:"creds"`
	Vars         []string `json:"vars"`
	ProviderKeys []string `json:"provider_keys"` // 形如 ["auth_token","base_url","model"]
}

type Problem struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Detail  string `json:"detail"`
	Warning bool   `json:"warning,omitempty"` // 真 = 展示但不阻断发布
	Fix     *Fix   `json:"fix,omitempty"`     // 非空 = UI 给「一键修复」
}

type Fix struct {
	Kind string `json:"kind"` // 目前只有 FixReplaceEnvKey
	From string `json:"from"`
	To   string `json:"to"`
}

const FixReplaceEnvKey = "replace_env_key"

// SettingsPath 是 settings.json 的受管相对路径。manifest 的根是 HOME。
const SettingsPath = ".claude/settings.json"

// 新增校验类别（M1 的五个保持原样）
const (
	ProblemBindingMissing    = "binding_missing"     // 错误
	ProblemBindingUnused     = "binding_unused"      // 警告
	ProblemAuthFieldMismatch = "auth_field_mismatch" // 错误 + 一键修复
)

func (s *Service) DraftBinding(setID string) (*providers.Binding, error) // 未绑定返回 (nil, nil)
func (s *Service) SetDraftBinding(setID string, b *providers.Binding) error // b 为 nil 即解绑
func (s *Service) FixAuthField(setID string) error // §3.3 的一键修复
```

### `hub/internal/revisions`（子计划 03）

```go
// PublishFiles 多一个 binding 参数：绑定冻结进版本，不能靠调用方事后补写。
func (s *Service) PublishFiles(
	setID string, files []protocol.FileEntry,
	binding *providers.Binding, note, source string,
) (*core.Record, error)

// BindingOf 读一条 Revision 冻结的绑定。未绑定返回 (nil, nil)。
func (s *Service) BindingOf(revID string) (*providers.Binding, error)
```

`Publish(setID, note, source)` 签名不变，内部读 `draft_binding` 传下去。

### `hub/internal/credentials`（子计划 02）

```go
// ReferencedBy 多返回一组 provider 记录 id（spec §5.3）。
func (s *Store) ReferencedBy(name string) (setIDs, revIDs, providerIDs []string, err error)

// ValueByID 按记录 id 取值。providers.credential 是 relation，存的是 id。
func (s *Store) ValueByID(id string) (string, error)
```

### `hub/internal/configsync`（子计划 04）

```go
var (
	ErrAgentTooOld     = errors.New("configsync: agent 版本过低")
	ErrBindingMissing  = errors.New("configsync: 引用了 {{provider.*}} 但没有服务绑定")
	ErrEmptyModelSlot  = errors.New("configsync: 引用了模型槽但绑定里该槽为空")
)

// NotifyProvider 在改了 Provider 之后重注入全机队。不产生新 Revision（spec §5.2）。
func (s *Service) NotifyProvider(providerID string) error
```

`configsync.Deps` 新增 `Providers *providers.Store`。

### `hub/internal/drift`（子计划 06、07）

```go
// DetectBindingDrift 找出「基线侧是 {{provider.base_url}}、现状侧是字面值」的行。
// 命中时返回现状侧那一段字面 URL（spec §6.1）。
func DetectBindingDrift(base, cur []byte) (url string, ok bool)

var ErrBindingDrift = errors.New(
	"drift: 绑定漂移不能收编——收编会把占位符拍平成硬编码字面值，绑定会当场失效")

// MatchBinding 对一条绑定漂移做反查三档（spec §6.3）。
func (s *Service) MatchBinding(eventID string) (BindingMatch, error)

type BindingMatch struct {
	URL         string          `json:"url"`
	Match       providers.Match `json:"match"`
	KeyLocation string          `json:"key_location,omitempty"` // gjson 路径，如 env.ANTHROPIC_AUTH_TOKEN
	KeyMasked   string          `json:"key_masked,omitempty"`
}

// Rebind 把漂移所属配置集的绑定改成指定 Provider 并发布新 Revision（spec §2.2）。
func (s *Service) Rebind(eventID, providerID string) (*core.Record, error)

// ExtractKey 把漂移内容里 location 处的值抽成凭据，返回凭据记录 id。
func (s *Service) ExtractKey(eventID, location, credName string) (string, error)
```

`drift.Deps` 新增 `Providers *providers.Store`、`Creds *credentials.Store`。

### `hub` 公开入口（子计划 04、07）

```go
// ProviderInput 是 providers.Input 的公开别名。internal/testsupport 够不到
// hub/internal/*，因此凡是要跨出 hub 包的类型都在这里重新导出。
type ProviderInput = providers.Input

// —— 子计划 04 ——
func (h *Hub) CreateProvider(in providers.Input) (string, error)
func (h *Hub) UpdateProvider(id string, in providers.Input) error
func (h *Hub) DeleteProvider(id string) error
func (h *Hub) SetBinding(setID string, b *providers.Binding) error
func (h *Hub) FixAuthField(setID string) error
func (h *Hub) ProviderPresets() []providers.Preset

// —— 子计划 07 ——
func (h *Hub) MatchBindingDrift(eventID string) (drift.BindingMatch, error)
func (h *Hub) RebindFromDrift(eventID, providerID string) (string, error)

// CreateProviderFromDrift 先把漂移内容里 location 处的值抽成名为 credName
// 的凭据，再用它建服务配置（spec §6.3 第二档）。in.Credential 由本方法填。
func (h *Hub) CreateProviderFromDrift(
	eventID, location, credName string, in providers.Input,
) (string, error)
```

---

## 数据模型（迁移 003）

### 新 collection `providers`

| 字段 | 类型 | 约束 |
|---|---|---|
| `name` | text | required, max 200, 唯一索引 |
| `preset` | text | max 64（自定义留空） |
| `base_url` | text | required, max 2000（归一化后存储） |
| `auth_field` | select | MaxSelect 1，值 `ANTHROPIC_AUTH_TOKEN` / `ANTHROPIC_API_KEY` |
| `credential` | relation → `credentials` | required, MaxSelect 1, **CascadeDelete false** |
| `models` | json | MaxSize 8192 |
| `defaults` | json | MaxSize 2048 |
| `note` | text | max 2000 |
| `created` / `updated` | autodate | |

索引：`idx_providers_name`（唯一，`name`）、`idx_providers_credential`（非唯一，`credential`）。

### 既有 collection 追加字段

| collection | 字段 | 类型 | 说明 |
|---|---|---|---|
| `revisions` | `binding` | json, MaxSize 4096 | 冻结的绑定；空 = 无绑定 |
| `config_sets` | `draft_binding` | json, MaxSize 4096 | 草稿绑定 |
| `config_sets` | `head_provider` | relation → `providers`, MaxSelect 1, CascadeDelete false | 冗余字段，唯一写入点在发布路径 |
| `drift_events` | `binding_drift` | bool | 命中 spec §6.1 的模式 |
| `drift_events` | `binding_url` | text, max 2000 | 现状侧那段字面 URL，供反查用 |

索引：`idx_config_sets_head_provider`（非唯一，`head_provider`）。

> **`binding_url` 是本计划相对 spec 的一处增补。** spec §6.3 要求「hub 拿漂移里的字面 base_url 做归一化匹配」，但没说这个值存哪。存下来让反查退化成一次字段读取，而不是每次打开卡片都重新解析基线与现状内容；它与 `binding_drift` 在同一处写入（`drift.upsert`），一致性维护点只有一个。

### 事件 kind（追加到 `hub/internal/events`）

```go
KindProviderCreated = "provider.created"
KindProviderUpdated = "provider.updated"
KindProviderDeleted = "provider.deleted"
KindBindingChanged  = "binding.changed"
```

---

## HTTP API（全部 `Bind(apis.RequireSuperuserAuth())`）

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | `/api/orciny/provider-presets` | 内置预设表（编译期常量，只读） |
| POST | `/api/orciny/providers` | 新建服务配置；可带 `from_drift` 抽 key |
| PUT | `/api/orciny/providers/{id}` | 改 base_url / 模型 / 凭据 → 立即重注入 |
| DELETE | `/api/orciny/providers/{id}` | 删除；被绑定时 409 |
| PUT | `/api/orciny/config-sets/{id}/binding` | 设置 / 清空草稿绑定 |
| POST | `/api/orciny/config-sets/{id}/fix-auth-field` | §3.3 的一键修复 |
| GET | `/api/orciny/drift/{id}/binding-match` | 反查三档 |
| POST | `/api/orciny/drift/{id}/rebind` | 改绑定并发布新版本 |

读列表（`providers` 记录本身）继续走 PocketBase JS SDK，与凭据页一致。

---

## 子计划索引

### 阶段一 · 数据面（spec §12 第 1–4 项，无 UI，靠端到端测试验收）

| # | 文件 | 交付物 | 验证方式 |
|---|---|---|---|
| 1 | [01-protocol-render.md](01-protocol-render.md) | `RefProvider` + `ProviderKeys` + `ConfigSnapshot.Provider`；agent 侧 `secrets` / `render` / `watcher` 接线 | 包内单测：词法、wire 兼容、往返律 property test 扩展到含 provider 值 |
| 2 | [02-providers.md](02-providers.md) | 迁移 003；`hub/internal/providers` 新包（预设表、CRUD、归一化、反查匹配）；凭据引用计数修补 | 包内单测：CRUD、归一化、精确优先于 host、**被 Provider 引用的凭据不可删** |
| 3 | [03-binding-publish.md](03-binding-publish.md) | `binding` / `draft_binding` / `head_provider` / `refs.provider_keys`；三条发布校验 + 一键修复；`importer` 正则修补 | 包内单测：refs 扫描准确、head_provider 同步、三条校验正反用例 |
| 4 | [04-reinject-gate.md](04-reinject-gate.md) | 快照组装与裁剪、定向版本门槛、`NotifyProvider` 反查链、HTTP 路由与 `hub` 公开入口、端到端 | `configsync` 单测 + `internal/testsupport` 端到端：改 base_url → 落盘变了而 Revision 没变 |

### 阶段二 · UI（spec §12 第 5 项）

| # | 文件 | 交付物 | 验证方式 |
|---|---|---|---|
| 5 | [05-frontend-providers.md](05-frontend-providers.md) | 「AI 服务」页、配置集的服务绑定区、编辑器补全与告警 | Vitest：纯逻辑（占位符前端词法、绑定表单状态机、预设带出） |

### 阶段三 · 绑定漂移（spec §12 第 6–7 项）

| # | 文件 | 交付物 | 验证方式 |
|---|---|---|---|
| 6 | [06-binding-drift.md](06-binding-drift.md) | 识别 + 禁用收编 + 说明文案（反查第三档骨架） | `drift` 单测 + 前端卡片测试 |
| 7 | [07-drift-lookup.md](07-drift-lookup.md) | 反查前两档：命中已有 Provider → 改绑定；命中预设 → 新建向导 | `drift` 单测 + 端到端：手改 base_url → 反查 → 改绑 → 全机队对齐 |

**依赖顺序**：01 → 02 → 03 → 04 → 05；06 依赖 04；07 依赖 06 与 05。01 与 02 之间没有代码依赖，可并行。

---

## spec 之外的增量（实现时按此办，验收时按此记）

计划相对 spec 做了四处补充，每一处都是为了让某个 spec 要求真正可落地：

1. **`drift_events.binding_url`**：见上「数据模型」注。
2. **`providers` 被绑定时不可删**（`ErrInUse`）：spec §5.3 只写了凭据的对称保护，但删掉一条被 `head_provider` 指着的 Provider 会让全机队在下次重注入时拿到空值——与凭据误删是同一类损坏。判定口径：被任何 `config_sets.head_provider` 或 `draft_binding.provider` 指向即拒绝。
3. **`ErrEmptyModelSlot`**：绑定存在、但引用了一个空模型槽（透传模式下写了 `{{provider.model}}`）。只在 `configsync` 组装快照时判定并置 assignment 为 `failed`，**不加第四条发布校验**——spec §7 明确只有三条，不扩面。
4. **`importer` 的 `placeholderValue` 正则要认 `provider`**：现有正则是 `^\{\{(cred|var|machine)\.[A-Za-z0-9_-]+\}\}$`，不改的话 `{{provider.auth_token}}` 会被敏感项检测当成明文 key 报出来。见子计划 03 Task 5。

---

## 已知代价（照抄 spec §13，实现时不要试图消解）

| 风险 | 应对 |
|---|---|
| `auth_field` 键名换不了（§3.3） | 发布校验报错 + 一键修复。**不自动改写用户文件**——这是有意的粗糙 |
| 凭据误删导致全机队拿到空值（§5.3） | 引用计数必须认 Provider。强制测试项，见子计划 02 Task 5 |
| `head_provider` 冗余字段不一致 | 唯一写入点在 `revisions.PublishFiles`。若实现中出现第二个写入点，停下来重看设计 |
| 手工升级 agent（§10） | 目标场景 ≤50 台，可接受。**不为此提前 M2 的自更新** |
| 预设种子数据过时 | 编译期常量，跟 hub 版本走；用户可用自定义 Provider 绕过。不做在线更新 |
| 反查误判（host 匹配把个人版认成团队版） | 精确匹配优先；host 匹配的 UI 措辞降级为「可能是」，动作仍需用户确认 |

---

## 验收标准（DoD）

全部完成后逐条核对，写进 `acceptance.md`：

1. 建一条 Provider（选预设 → 自动带出 base_url / 模型 / auth_field → 选已有凭据）→ 保存成功，列表显示「被 0 个配置集引用」。
2. 配置集绑定该 Provider + 主模型 → 「插入 env 片段」写进 `settings.json` 草稿 → 发布 → agent 落盘的 `settings.json` 里是真实 base_url 与模型 id，**blob 里仍是占位符**。
3. 改 Provider 的 base_url → 全机队重注入 → 落盘内容变了、**Revision 的 id 与 checksum 都没变**、**没有产生漂移**。
4. 轮换 Provider 引用的凭据 → 同样重注入、不产生新 Revision。
5. 尝试删除被 Provider 引用的凭据 → 被拒绝，错误信息指明被哪个服务配置引用。
6. 配置集里写了 `{{provider.*}}` 但没绑定 → 发布校验报**错误**，阻断发布。
7. 绑定的 Provider 用 `ANTHROPIC_API_KEY` 而 `settings.json` 里写的是 `ANTHROPIC_AUTH_TOKEN` → 校验报错并给「一键修复」；点一下之后草稿 diff 里能看见那一行的变化。
8. 绑了但全文没有 `{{provider.*}}` → 报**警告**，不阻断。
9. 指派给一台 `agent_version = 0.1.0` 的机器 → 不下发，assignment 置 `failed`，`last_error` 写明「agent 版本过低（0.1.0 < 0.2.0），请升级后重试」，机器行与配置集页都看得见。
10. ssh 上某台机器把 `ANTHROPIC_BASE_URL` 改成另一家 → 收件箱出现该条漂移，标记为绑定漂移，「收编」置灰且 hover 有说明，「恢复」「忽略」可用。
11. 该漂移的 base_url 命中已有 Provider → 卡片给「改成它」→ 点击后产生新 Revision，其余机器跟着对齐。
12. 该漂移的 base_url 命中内置预设 → 卡片给「新建服务配置」→ 向导预填平台与 base_url，机器上手写的 key 被抽成凭据 → 建成后可一键改绑。
13. 都不命中 → 卡片只给「恢复」「忽略」，并写明「无法识别这个 base_url 属于哪个平台」。
14. `go test -tags=testing ./...` 与 `npm test` 全绿；`make lint` 通过。
15. 二进制体积仍在 M1 的量级（agent < 30 MB、hub < 64 MB）。
