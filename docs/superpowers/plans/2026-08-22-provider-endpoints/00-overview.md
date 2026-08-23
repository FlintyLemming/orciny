# Orciny M1.6 · 双端点服务配置与凭据内联 —— 实现计划（统括）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **本文件是索引与全局契约，不含可执行任务。** 实际任务在 `01-*.md` … `10-*.md` 十个子计划里。每次只交给执行者**一个子计划文件 + 本文件**。

**Goal:** 把 `providers` 从「一个 Anthropic 协议端点」升成「一家平台，内含 claude / openai 两个协议端点」，同时把 API key 直接内联到 provider 上、废止 `credentials` 实体与 `{{cred.*}}` 占位符。

**Architecture:** `providers` 记录加两个 JSON 子结构（`claude` / `openai`）与三处密文字段（平台级 + 两个端点级）；占位符词法从六个内置名改成九个**端点限定名**（`{{provider.claude.base_url}}` / `{{provider.openai.api_key}}`）；`credentials` 包拆成 `secretbox`（AES-GCM + 主密钥）与 `variables`（机器变量），实体本身连同页面、路由、快照字段一起删除；发布校验新增 `endpoint_missing`；导入向导与漂移的两处「抽成凭据」合并成一条「抽成 provider 的 key」。

**Tech Stack:** 沿用 M1 / M1.5，**不新增任何依赖**。Go 1.26 · PocketBase v0.39.9 · fxamacker/cbor v2.9.2 · tidwall/gjson + sjson · blang/semver · testify；前端 React 19 + Vite + Tailwind v4 + nanostores + Lingui + Vitest。

**Spec:** [M1.6 工程设计](../../specs/2026-08-22-provider-endpoints-design.md)（下称「spec」，条目号如 §3.1 均指它）

**上位/前置文档：** [M1.5 工程设计](../../specs/2026-08-21-provider-binding-design.md)（称「M1.5 spec」）· [M1 工程设计](../../specs/2026-07-31-m1-config-loop-design.md)（称「M1 spec」）· [M1.5 实现计划](../2026-08-21-provider-binding/00-overview.md) · [M1.5 验收记录](../2026-08-21-provider-binding/acceptance.md) · [产品设计文档](../../../PRODUCT-DESIGN.md)

---

## 相对 spec 的两处偏离（先读这一节）

spec 是本计划的唯一需求来源。执行时只有以下两处**刻意不照抄**，理由写在这里，
子计划里不再重复论证。除这两处外，凡与 spec 冲突的，以 spec 为准。

### 偏离一：迁移拆成 `004`（追加）+ `005`（破坏）两条

spec §6.1 把五个步骤放进一条破坏性的 `004`。但 spec §8 同时要求
「每一步结束时 `go test -tags=testing ./...` 与前端测试都应绿」，而
`credentials` collection 一旦在第 3 步被删掉，`hub.go` 的启动自检
（`credentials.Store.VerifyAll` 读 `credentials` 表）会让**每一个 hub 测试**
在 OnServe 阶段就失败——直到第 7 步才可能恢复。两条要求不能同时满足。

拆法与 spec 的五个步骤逐条对应，一条也不少：

| spec §6.1 的步骤 | 落在 |
|---|---|
| 1. `providers` 加字段 | `004`（追加） |
| 2. 逐条搬运密文与旧字段 | `004`（追加） |
| 3. 删 `providers.credential` relation 与索引 | `005`（破坏） |
| 4. 删旧字段 `base_url` / `auth_field` / `models` / `defaults` | `005`（破坏） |
| 5. 删 `credentials` collection | `005`（破坏） |

`004` 另做一件 spec 没写但拆分必需的事：把 `credential` relation 的
`Required` 改成 `false`。否则 `004` 之后、`005` 之前，新建 provider 会被
「credential 必填」挡住。

`down004` 是真的可逆（只删新加的字段）。`down005` 按 spec §6.3 直接返回错误。

### 偏离二：三处密文放在**顶层 Hidden 字段**，不放进端点 JSON 里

spec §2.1 的表把 `key_cipher?` 画在 `claude` / `openai` 两个 JSON 子结构内部。
照做的话密文会随 PocketBase 的列表查询与 realtime 推送发到前端——而 spec §5.2
明写「密文永不回传前端……本期不因为字段搬了家就松口」。JSON 字段没有办法
只隐藏其中一个子键，前端只能像今天的 `stores/credentials.ts` 那样收到之后
自己抹掉，那不是「永不回传」。

PocketBase 的字段有 `Hidden bool`（「hides the field from the API response」），
对顶层字段有效。因此密文用三个顶层 `TextField{Hidden: true}`：

```
key_cipher           平台级密文        Hidden
claude_key_cipher    claude 端点密文   Hidden
openai_key_cipher    openai 端点密文   Hidden
```

`key_last4` 仍按 spec：平台级在顶层，端点级在各自的 JSON 子结构里
（末四位本来就是给 UI 回显用的，正是**要**回传的东西）。

除密文的落点外，端点 JSON 的其余字段与 spec §2.1 逐字符一致。

---

## Global Constraints

以下约束对**每一个任务**生效，子计划不再逐条重复。
**M0 / M1 / M1.5 计划的 Global Constraints 全部继续有效**，此处只列 M1.6
新增、加强或**改写**的部分。

**模块边界（不变）**

- `agent/**` 与 `hub/**` 之间**不允许任何直接依赖**。共享面只有 `protocol/`。
- `protocol/` 不含业务逻辑、不 import PocketBase。占位符包只放词法。
- 数据迁移**追加式**：新建 `004_provider_endpoints.go` 与 `005_drop_credentials.go`，
  绝不修改 `001` / `002` / `003`。
- 新字段的 API rule 不变（`providers` 已是 superuser-only）。

**协议纪律**

- `ConfigSnapshot` 的 CBOR 字段编号**只增不改不复用**。本期**删除** `Credentials`
  （编号 `6`）。`6` 从此**退休**，不得被任何新字段占用——注释里写明这件事。
- `RefCred RefKind = 1` 删除，编号 `1` 同样退休，`RefVar=2` / `RefMachine=3` /
  `RefProvider=4` 一律不动。

**版本号（本期抬一档，spec §9 最后一行）**

- `orciny.Version`：`"0.2.0"` → `"0.3.0"`
- `orciny.MinProviderAgentVersion`：`0.2.0` → `0.3.0`（老 agent 认不得端点限定名）
- `orciny.MinAgentVersion`**不动**（保持 `0.1.0`）：它在握手层拦截，一抬就把
  所有低版本 agent 挡在门外，包括根本没绑服务的机器。

**破坏性纪律（spec §6.2，执行时不要「顺手清洗」）**

- `config_sets.draft_refs` / `revisions.refs` 里存量的 `creds` 键**留在 JSON 里**，
  `Refs` 结构体去掉字段后反序列化自然忽略。不写清洗代码。
- 存量 blob 里的 `{{cred.X}}` 与 `{{provider.base_url}}` 字面量**不改写**。
  它们下次发布时被词法拒绝，是预期行为。
- 历史 `events` 里的 `credential.created` / `.rotated` / `.deleted` 行**留着**。
  Go 侧的三个常量删掉（没有写入方了），前端 `EventKind` 联合类型里的三个
  字符串**保留**，否则老事件渲染不出来。

**秘密纪律（改写 M1.5）**

- 秘密档从「全部凭据 + `provider.auth_token`」改为**恰好两个键**：
  `claude.auth_token` 与 `openai.api_key`。两者语义完全相同——必须还原，
  还原不了就 `Safe=false`、不上报内容。**一把 API key 落进尽力档就等于有机会
  被明文上传**，两个端点在这一点上没有区别（spec §3.4）。
- 其余七个 provider 键进尽力档，受 `render.MinVarLen = 4` 约束。
- 快照只下发 `revisions.refs.provider_keys` 里出现过的键（裁剪口径不变）。
- 「一个配置了 `base_url` 的端点，必须能解出一把 key」——平台级或端点级，
  有一个就行。这条与「四槽要么全空要么全满」同级，在 `providers.validate` 把关。

**数值常量（逐字符照抄，顺序即 UI 展示顺序与 `rankOf` 定序依据）**

```go
ProviderKeys = []string{
    "claude.base_url", "claude.auth_token",
    "claude.model", "claude.model_opus", "claude.model_sonnet", "claude.model_haiku",
    "openai.base_url", "openai.api_key",
    "openai.model",
}
```

- 恰好九个，**不要重排**。
- claude 端点的 `auth_field` 仍是二选一枚举：`ANTHROPIC_AUTH_TOKEN` / `ANTHROPIC_API_KEY`。
- openai 端点的 `auth_field` 是**自由文本**，默认 `OPENAI_API_KEY`，不做枚举。
- `secretbox.MinValueLen = 8`（原 `credentials.MinValueLen`，值不变）。
- `render.MinVarLen = 4`（不变）。

**核心不变量（每个子计划的测试都要能指回这一条，与 M1.5 相同）**

| 动作 | 是否产生新 Revision |
|---|---|
| 换绑定（配置集从 A 切到 B、换模型） | **是** |
| 改 Provider 内部（任一端点的 base_url、模型、key） | **否**，走 `ConfigNotify` 重注入 |

---

## 全局接口契约

子计划的实现者只看得见自己那一份任务。以下签名是跨子计划的公共词汇，
**逐字符照抄**，不要自行改名。

### `protocol`（子计划 02）

```go
// RefCred 已删除，RefKind = 1 退休不复用。
const (
    RefVar      RefKind = 2
    RefMachine  RefKind = 3
    RefProvider RefKind = 4
)

var ProviderKeys = []string{ /* 上面那九个 */ }

// EndpointOf 从端点限定名里取出端点段：
// "claude.base_url" → "claude"；"openai.api_key" → "openai"；
// 不含点或不在 ProviderKeys 里 → ""。
func EndpointOf(name string) string
```

`Ref.Name` 承载两段名（`"claude.base_url"`）。`parseRef` 的 provider 分支
**不走 `validName`**，直接拿整个 name 去 `ProviderKeys` 白名单查（spec §3.3）。

### `hub/internal/secretbox`（子计划 01）

```go
const KeyFileName = "secret.key"        // 值不变，存量密文靠它继续解得开
const EnvKeyName  = "ORCINY_SECRET_KEY" // 同上
const MinValueLen = 8

var ErrShortValue = errors.New("secretbox: 值过短")

func LoadMasterKey(dataDir string) ([]byte, error)
func Encrypt(key []byte, plaintext string) (string, error)
func Decrypt(key []byte, cipherB64 string) (string, error)
func Last4(v string) string
```

### `hub/internal/variables`（子计划 01）

```go
var ErrBadName = errors.New("variables: 变量名非法")

type Store struct{ /* app core.App */ }
func NewStore(app core.App) *Store
func (s *Store) MachineVariables(machineID string) (map[string]string, error)
func (s *Store) SetVariable(machineID, key, value string) error
func (s *Store) DeleteVariable(machineID, key string) error
```

### `hub/internal/providers`（子计划 03）

```go
const (
    EndpointClaude = "claude"
    EndpointOpenAI = "openai"

    // claude 端点的二选一枚举（不变）
    AuthToken  = "ANTHROPIC_AUTH_TOKEN"
    AuthAPIKey = "ANTHROPIC_API_KEY"

    // openai 端点的默认鉴权字段名。自由文本，不是枚举。
    DefaultOpenAIAuthField = "OPENAI_API_KEY"
)

// Endpoint 是两个端点的公共部分。密文不在这里——它在顶层 Hidden 字段。
type Endpoint struct {
    BaseURL   string   `json:"base_url"`
    AuthField string   `json:"auth_field"`
    KeyLast4  string   `json:"key_last4,omitempty"`
    Models    []string `json:"models"`
}

// Configured 是「端点配没配」的唯一判定（spec §2.2）。
// 发布校验、UI 置灰、快照组装三处共用它。
func (e Endpoint) Configured() bool { return e.BaseURL != "" }

type ClaudeEndpoint struct {
    Endpoint
    Defaults ModelSlots `json:"defaults"`
}

type OpenAIEndpoint struct {
    Endpoint
    DefaultModel string `json:"default_model"`
}

// ClaudeOf / OpenAIOf 从记录解出端点。空 JSON 字段返回零值，不是错误。
func ClaudeOf(r *core.Record) ClaudeEndpoint
func OpenAIOf(r *core.Record) OpenAIEndpoint

// EndpointInput 里的 Key 是三态（spec §5.2 的「留空则不修改」）：
//   nil  = 不修改（编辑态密码框留空）
//   ""   = 清空（端点级清空即回落平台级）
//   非空 = 替换
type EndpointInput struct {
    BaseURL      string
    AuthField    string
    Models       []string
    Key          *string
    Defaults     ModelSlots // 仅 claude 端点有意义
    DefaultModel string     // 仅 openai 端点有意义
}

type Input struct {
    Name   string
    Preset string
    Note   string
    Key    *string // 平台级，三态同上
    Claude EndpointInput
    OpenAI EndpointInput
}

func NewStore(app core.App, key []byte, ev *events.Writer) *Store

// Key 按 spec §2.3 的两级取值解出某端点实际使用的 key：
// 端点级密文非空则用它，否则回落平台级。两级都空返回 ErrNoKey。
func (s *Store) Key(r *core.Record, endpoint string) (string, error)

// VerifyAll 启动自检：扫三处密文，解不开则拒绝启动（spec §2.6）。
func (s *Store) VerifyAll() error

// SetEndpointKey 把一把明文 key 写进某端点并落库（子计划 07 加）。
// 导入向导与漂移的两处「抽取」共用它，不碰端点的其余字段。
func (s *Store) SetEndpointKey(r *core.Record, endpoint, value string) error

var (
    ErrNotFound     = errors.New("providers: 服务配置不存在")
    ErrInUse        = errors.New("providers: 服务配置仍被绑定")
    ErrBadAuthField = errors.New("providers: auth_field 非法")
    ErrBadBaseURL   = errors.New("providers: base_url 非法")
    ErrNoKey        = errors.New("providers: 端点没有可用的 key")
)
```

`Preset` 结构：

```go
type Preset struct {
    ID, Name              string
    Claude                PresetEndpoint // BaseURL == "" 表示该平台没有这个口
    OpenAI                PresetEndpoint
    WebsiteURL, APIKeyURL string
    Icon, IconColor       string
    CollectorType, CollectorMode string // M2 预留，不动
}

type PresetEndpoint struct {
    BaseURL      string     `json:"base_url"`
    AuthField    string     `json:"auth_field"`
    Models       []string   `json:"models"`
    Defaults     ModelSlots `json:"defaults"`      // 仅 claude 侧填
    DefaultModel string     `json:"default_model"` // 仅 openai 侧填
}
```

### `hub/internal/configsets`（子计划 04）

```go
type Refs struct {
    Vars         []string `json:"vars"`          // Creds 字段删除
    ProviderKeys []string `json:"provider_keys"`
}

const ProblemEndpointMissing = "endpoint_missing"

// Validate 去掉 known 参数：它原本装的是「已定义的凭据名」。
// 机器变量的「已定义」改由 Service 自己查 variables 表（spec §3.5）。
func (s *Service) Validate(setID string) ([]Problem, error)
```

### `hub/internal/configsync`（子计划 05）

```go
var ErrEndpointMissing = errors.New("configsync: 引用了某端点但绑定的服务配置没配它")

type Deps struct {
    // Creds 删除，换成：
    Vars *variables.Store
    // 其余不变
}
```

`isBindingFault` 加上 `ErrEndpointMissing`：指派置 `failed` 并写明原因，
**不置 `degraded`**（机器上什么都没被改过）。

### `agent`（子计划 06）

```go
// render
type Values struct {          // Creds 字段删除
    Vars     map[string]string
    Provider map[string]string
}

// secretKeys 是 provider 里走秘密档的键。两个都是（spec §3.4）。
var secretKeys = []string{"claude.auth_token", "openai.api_key"}

// secrets
type File struct {            // Creds 字段删除
    Vars     map[string]string `json:"vars"`
    Machine  map[string]string `json:"machine"`
    Provider map[string]string `json:"provider"`
}
```

### HTTP 端点（子计划 07 / 08）

| 方法 | 路径 | 变化 |
|---|---|---|
| POST | `/api/orciny/credentials` | **删除** |
| POST | `/api/orciny/credentials/{id}/rotate` | **删除** |
| DELETE | `/api/orciny/credentials/{id}` | **删除** |
| POST | `/api/orciny/providers` | 请求体改成双端点（见下） |
| PUT | `/api/orciny/providers/{id}` | 同上 |
| POST | `/api/orciny/config-sets/{id}/extract` | 请求体 `{path, location, provider, endpoint}` |
| GET | `/api/orciny/config-sets/{id}/provider-match?path=` | **新增**，返回 `providers.Match` |

`providerBody`（`*string` 承载三态，JSON 里字段缺席 = 不修改）：

```go
type endpointBody struct {
    BaseURL      string               `json:"base_url"`
    AuthField    string               `json:"auth_field"`
    Models       []string             `json:"models"`
    Key          *string              `json:"key"`
    Defaults     providers.ModelSlots `json:"defaults"`
    DefaultModel string               `json:"default_model"`
}

type providerBody struct {
    Name   string       `json:"name"`
    Preset string       `json:"preset"`
    Note   string       `json:"note"`
    Key    *string      `json:"key"`
    Claude endpointBody `json:"claude"`
    OpenAI endpointBody `json:"openai"`

    // FromDrift 非空时先把漂移里的 key 取出来内联进 provider（spec §5.5）。
    FromDrift *struct {
        Event    string `json:"event"`
        Location string `json:"location"`
        Endpoint string `json:"endpoint"`
    } `json:"from_drift"`
}
```

### 前端（子计划 09）

```ts
export interface EndpointRecord {
  base_url: string
  auth_field: string
  key_last4?: string
  models: string[] | null
}
export interface ClaudeEndpointRecord extends EndpointRecord { defaults: ModelSlots | null }
export interface OpenAIEndpointRecord extends EndpointRecord { default_model: string }

export interface ProviderRecord {
  id: string
  name: string
  preset: string
  note: string
  key_last4: string
  claude: ClaudeEndpointRecord | null
  openai: OpenAIEndpointRecord | null
  created: string
  updated: string
}

/** 与 Go 侧 protocol.ProviderKeys 逐字符一致，顺序相同。 */
export const PROVIDER_KEYS = [
  'claude.base_url', 'claude.auth_token',
  'claude.model', 'claude.model_opus', 'claude.model_sonnet', 'claude.model_haiku',
  'openai.base_url', 'openai.api_key',
  'openai.model',
] as const
```

---

## 落地顺序与依赖

spec §8 给了十一步。本计划把它整成十个子计划，两处与 spec 的顺序不同，
理由随之写明：

| # | 子计划 | 前置 | 对应 spec §8 |
|---|---|---|---|
| 01 | [`secretbox` / `variables` 拆包](01-secretbox-variables.md) | 无 | 1 |
| 02 | [protocol 词法端点限定](02-protocol-lexicon.md) | 无（与 01 可并行） | 2 |
| 03 | [providers 双端点数据模型与预设表](03-providers-endpoints.md) | 01、02 | 3 + 8 |
| 04 | [configsets 发布校验](04-validation.md) | 02、03 | 4 |
| 05 | [configsync 快照九键](05-snapshot.md) | 03、04 | 5 |
| 06 | [agent render / restore](06-agent-render.md) | 02 | 6 |
| 07 | [导入向导与漂移的抽取重定向](07-extract-redirect.md) | 03 | 10 |
| 08 | [删除 credentials 的服务端残留 + 迁移 005](08-drop-credentials.md) | 04、05、06、07 | 7 |
| 09 | [前端](09-frontend.md) | 03、08 的路由契约 | 9 |
| 10 | [i18n 与验收](10-i18n-acceptance.md) | 全部 | 11 |

**与 spec §8 顺序不同的两处：**

1. **预设表（spec 第 8 步）并进 03。** `Preset.BaseURL` 变成 `Preset.Claude.BaseURL`
   是编译期依赖——`MatchBaseURL` 扫预设表，`providers` 包不可能只改一半。
   数据核填仍是 03 里独立的一个任务，可以单独被驳回。
2. **抽取重定向（spec 第 10 步）提到 08 之前。** `importer` 与 `drift` 是
   `credentials.Store` 的最后两个使用者；不先把它们改掉，08 删不掉那个包。

01→02→03 与 03→04→05 是硬依赖。06 只依赖 02，可与 03–05 并行。09 只依赖
03 定下的记录形状与 08 定下的路由，可与 04–08 并行开工，但**合并要排在 08 之后**
（`Refs.Creds` 与凭据路由消失前，前端删凭据页会让 e2e 断链）。

---

## Definition of Done

来自 spec §1.2（交付物）、§7（测试策略）与 §9（风险）。验收记录写进
`acceptance.md`，逐条给依据。

| # | 标准 |
|---|---|
| 1 | 一条 provider 同时承载 claude 与 openai 两个端点；`name` 唯一索引仍在 |
| 2 | 端点级 key 覆盖平台级；两级都空 + `base_url` 非空 → `providers.validate` 拒绝 |
| 3 | 只配 claude 端点的 provider：`OpenAIOf(r).Configured() == false`，UI 显示置灰的「未配置」行 |
| 4 | 九个端点限定名全部 parse 通过；`{{provider.base_url}}` 报「不是内置名」；`{{cred.X}}` 报「未知前缀」 |
| 5 | 引用 `{{provider.openai.*}}` + 绑定的 provider 没配 openai 端点 → 发布校验 `endpoint_missing`，**阻断** |
| 6 | `auth_field_mismatch` 与 `FixAuthField` 在 `{{provider.claude.auth_token}}` 下仍然工作，且只管 claude 端点 |
| 7 | 快照裁剪：只发 `refs.provider_keys` 里出现过的键；`openai.model` 取 provider 的 `default_model` |
| 8 | 端点缺失走到 configsync → `ErrEndpointMissing`，指派置 `failed` 且**不置** `degraded` |
| 9 | `claude.auth_token` 与 `openai.api_key` **都**落进秘密档；其余七个进尽力档 |
| 10 | 两个端点 key 相同时，还原后留下的是 `ProviderKeys` 里靠前的那个 |
| 11 | 库里有 provider、主密钥不匹配 → 拒绝启动，错误信息含「服务配置 X 的 claude 端点」与备份提示 |
| 12 | 在含「provider + 被它引用的凭据」的库上跑 `004` + `005`：密文搬运后仍解得开，旧字段与 `credentials` collection 消失，`down005` 返回错误 |
| 13 | 改 provider 的任一端点 → 重注入全机队、**不产生新 Revision**（M1.5 端到端测试改词法后保留并通过） |
| 14 | 凭据页、`/credentials` 三个路由、`{{cred.*}}` 词法、`ConfigSnapshot.Credentials`、`secrets.json` 的 `creds` 全部消失；机器变量编辑器**保留** |
| 15 | 导入向导与漂移的「抽取」都写进 provider 的端点 key，并把文件里**每一处**该值替成对应端点的占位符 |
| 16 | `ProviderDialog` 选预设带出两组端点；端点级 key 留空则不覆盖；编辑态密码框为空且提示「留空则不修改」 |
| 17 | `BindingBar` 里没配 claude 端点的 provider 置灰，悬停说明原因 |
| 18 | `go test -tags=testing ./...` 与 `npm test` 全绿；`make lint` 通过 |
| 19 | 二进制体积仍在 M1.5 量级（agent < 30 MB、hub < 64 MB） |

---

## 每个子计划结束时都要跑的命令

```bash
go test -tags=testing ./... && cd hub/internal/site && npm test
```

```bash
make lint
```
