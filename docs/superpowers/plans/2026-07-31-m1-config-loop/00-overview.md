# Orciny M1 · 配置闭环 —— 实现计划（统括）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **本文件是索引与全局契约，不含可执行任务。** 实际任务在 `01-*.md` … `17-*.md` 十七个子计划里。每次只交给执行者**一个子计划文件 + 本文件**。

**Goal:** 让作者从「看见机队」走到「管住机队」——主力机的 `~/.claude` 被采集成配置集，其余机器自动对齐，任一机器上顺手改的东西能被收编成新版本并同步全机队，apply 失败自动回滚。

**Architecture:** hub 侧新增八个 collection 与七个 internal 包（内容寻址存储 → 配置集/版本 → 凭据 → 下发编排 → 漂移收件箱）；agent 侧新增七个 internal 包（manifest 展开 → 本地 blob 缓存与凭据缓存 → 渲染/还原 → 计划-快照-原子写-回滚 → fsnotify 对账）。两侧唯一新增的共享面是 `protocol` 的 Kind 10–19、占位符词法与 checksum 口径，外加中立的 `internal/manifest`。下发一律「hub 发信号、agent 主动拉」，幂等取代离线队列。

**Tech Stack:** 沿用 M0（Go 1.26 · PocketBase v0.39.9 · gws v1.10.1 · fxamacker/cbor v2.9.2 · cobra · testify · React 19 + Vite + Tailwind v4 + nanostores + Lingui）。M1 新增：`github.com/fsnotify/fsnotify v1.10.1`（已在 go.sum，转直接依赖）、`github.com/tidwall/sjson` + `github.com/tidwall/gjson`（`keys` 模式的原地 JSON 编辑）、`github.com/pmezard/go-difflib`（已在 go.sum，转直接依赖，hub 侧算 unified diff）；前端新增 CodeMirror 6、`diff`（jsdiff）、Vitest + React Testing Library。

**上位文档：** [M1 工程设计 spec](../../specs/2026-07-31-m1-config-loop-design.md)（下称「spec」，条目号如 §7.4 均指它）· [M0 工程设计 spec](../../specs/2026-07-28-m0-skeleton-design.md) · [M0 实现计划](../2026-07-28-m0-skeleton/00-overview.md) · [产品设计文档](../../../PRODUCT-DESIGN.md)

---

## Global Constraints

以下约束对**每一个任务**生效，子计划不再逐条重复。M0 计划的 Global Constraints 全部继续有效（模块路径、依赖锁死、模块边界、日志脱敏、Conventional Commits），此处只列 M1 新增或加强的部分。

**模块边界**

- `agent/**` 与 `hub/**` 之间仍然**不允许任何直接依赖**。新增的共享代码只能进 `protocol/`（wire 格式）或顶层 `internal/`（中立工具，如 `clock` / `atomicfile` / `manifest`）。
- `protocol/` 仍然不含业务逻辑、不 import PocketBase。占位符包只放词法、转义、引用提取与「按词法结果回填」的 `Render`——**取值逻辑（值从哪来）留在 hub 与 agent 各自的 render 包**。
- 数据迁移**追加式**：新建 `hub/internal/migrations/002_configsets.go`，绝不修改 `001_initial.go`。
- 新 collection 的 API rule 全部保持 `nil`（仅 superuser 可访问），与 M0 一致。

**数值常量（逐字符照抄，不得自行调整）**

- 单个受管文件上限 **512 KiB** = `512 << 10`。
- 携带内容的消息按累计 **256 KiB** 分批 = `256 << 10`，最后一批置 `Final`。
- WS `ReadMaxPayloadSize` **1 MiB** = `1 << 20`，**hub 与 agent 两侧都要显式设**（M0 只设了 hub 侧）。
- 漂移去抖 **2 秒**；定时全量对账默认 **5 分钟**；同一路径上报节流 **30 秒**。
- apply 前快照保留最近 **5** 份。
- 凭据值长度下限 **8**；变量还原的长度下限 **4**。
- 敏感项值特征：长度 ≥ **32** 且 Shannon 熵 ≥ **3.5**。

**路径纪律（spec §3.1 / §3.3）**

- manifest 的路径根一律是 **HOME**，不是 `~/.claude`。任何地方都不得出现 `../`。
- 路径校验在 manifest 展开与 apply 落盘**两处**都要做，任一不通过即整体拒绝：拒绝绝对路径、拒绝任何 `..` 段、`filepath.EvalSymlinks` 之后必须仍在 `managed_home` 之下、非常规文件跳过并记 `Skipped`。
- 判定优先级恒为：**恒排除 > manifest.exclude > manifest.include**。

**凭据纪律（spec §6）**

- Revision 与 blob 里**永远只存占位符**，绝不存明文密钥。任何写 blob 的路径都要能被「查库确认无明文」的测试覆盖。
- 凭据还原**必须成功**：还原后仍能搜到任一已知凭据值 → 该条漂移只报路径、置 `Truncated`、不带内容。
- 变量还原**尽力而为**：失败置 `RestorePartial`，不阻断。
- 日志里不得出现凭据明文、`secrets.json` 内容、blob 原文。

**权限位**

- `~/.orciny/state.json`、`~/.orciny/secrets.json`、快照目录内的文件一律 **0600**，快照目录本身 **0700**。
- 落盘到 managed home 的受管文件：含凭据引用的 **0600**，其余 **0644**。

**测试纪律**

- **不允许在测试里 `time.Sleep`**。去抖 2 秒、对账 5 分钟、节流 30 秒全部走注入的 `clock.Clock`，测试用 `clock.Fake` 推进。
- 等「推进时钟后产生的可观测效果」用 `require.Eventually(t, cond, 2*time.Second, 5*time.Millisecond)`。`Eventually` 的条件函数跑在自己的 goroutine 上，**里面不能调 `require`**（M0 教训 I / M）。
- 断言 PocketBase 的 JSON 字段子键必须走 `rec.UnmarshalJSONField("field", &v)`，`rec.GetString("field.key")` 恒返回空串（M0 教训 D / K）。
- 一切碰 `~/.claude` 的测试**必须**显式传 managed home 临时目录。`testsupport.NewTestAgent` 的签名强制这一点。
- 测试命令：`go test -tags=testing ./...`（Go）、`npm test`（前端，即 `vitest run`）。

**提交**

- 每个任务最后一步是一次 commit，Conventional Commits，正文中文。一个任务一次 commit，不要攒。
- 前端构建会脏 `hub/internal/site/dist/index.html`，提交前 `git checkout -- hub/internal/site/dist/index.html`（M0 已知项）。

---

## 子计划索引

### 阶段一 · 下发闭环（spec §13 第 1–11 项）

| # | 文件 | 交付物 | 验证方式 |
|---|---|---|---|
| 1 | [01-datamodel.md](01-datamodel.md) | `migrations/002_configsets.go` 八个 collection + 索引；`hub/internal/blobs`；`events` 新 kind 常量 | 包内单测：迁移建表、blob 去重/GC |
| 2 | [02-protocol.md](02-protocol.md) | Kind 10–19 与全部消息结构；`protocol/placeholder.go`；`protocol/checksum.go`；两侧 1 MiB 上限常量 | 包内单测：编解码往返、词法、往返 property test、checksum golden |
| 3 | [03-credentials.md](03-credentials.md) | 主密钥加载与启动自检；`hub/internal/credentials` 加解密、引用保护、机器变量 | 包内单测：加解密往返、主密钥缺失拒绝启动、被引用不可删 |
| 4 | [04-configsets.md](04-configsets.md) | `hub/internal/configsets`（草稿、manifest、指派、发布期校验）与 `hub/internal/revisions`（发布、checksum、diff、回滚） | 包内单测 + `revisions` 的 checksum 固定期望值 |
| 5 | [05-manifest.md](05-manifest.md) | `internal/manifest`：schema、glob、恒排除、路径安全、`Expand`；`agent.Config` 的 `managed_home` / `reconcile_interval`；`testsupport` 强制双临时目录 | 包内单测：三种 mode、恒排除不可覆盖、逃逸被拒 |
| 6 | [06-agent-store.md](06-agent-store.md) | `agent/internal/state`、`agent/internal/secrets`、`agent/internal/blobcache`、`agent/internal/render`（渲染方向） | 包内单测：权限位、缓存命中、未定义引用报错 |
| 7 | [07-applier.md](07-applier.md) | `agent/internal/applier`：plan 五动作、apply 前快照、原子落盘、逆序回滚、`degraded`、`keys` 合并 | 包内单测：五动作各一例、回滚、幂等重放、`keys` 保序 |
| 8 | [08-configsync.md](08-configsync.md) | `hub/internal/configsync`；ws 双向路由；`machines` 按机器发消息；`agent/internal/syncer`；hub/agent 装配；端到端下发集成测试 | `internal/testsupport` 集成测试：指派 → 落盘 → 回执 |
| 9 | [09-import.md](09-import.md) | `hub/internal/importer`：采集编排与三层敏感项检测；agent 侧 `CollectRequest` 应答；导入 API | 包内单测 + 「采集 → 抽取 → 发布 → 落盘」集成测试 |
| 10 | [10-frontend-config.md](10-frontend-config.md) | Vitest 体系；配置集列表/详情/编辑器/发布/版本历史/回滚；凭据页；变量；指派；导入向导 | `npm test` + 手工验收 |

→ 阶段一末尾真机验收 DoD 1、2、7、8、9、10、13、14

### 阶段二 · 漂移闭环（spec §13 第 12–19 项）

| # | 文件 | 交付物 | 验证方式 |
|---|---|---|---|
| 11 | [11-restore.md](11-restore.md) | `agent/internal/render` 的还原方向 + 独立命名的安全测试 | 包内单测：降序替换、还原后不含任何凭据值、`RestorePartial` |
| 12 | [12-watcher.md](12-watcher.md) | `agent/internal/watcher`：fsnotify、2 秒去抖、5 分钟对账、30 秒节流、`tree` 识别新增 | 包内单测（假时钟） |
| 13 | [13-drift.md](13-drift.md) | `DriftReport` 链路、hub 侧 unified diff、`drift_events` 落库与部分唯一索引语义 | 包内单测 + 集成测试：改文件 → 收件箱 |
| 14 | [14-inbox.md](14-inbox.md) | 收编 / 恢复 / 忽略 / 跨机器冲突硬阻止 / `superseded` | 包内单测 + 集成测试：收编 → 新版本 → 其余机器对齐 |
| 15 | [15-survey.md](15-survey.md) | `survey` 模式下发与全量对账；`state.json` 丢失自愈 | 集成测试：survey 不写盘、删 state.json 不覆盖用户文件 |
| 16 | [16-frontend-drift.md](16-frontend-drift.md) | 收件箱、三方对比、机器详情补全、`degraded` 告警与解除 | `npm test` + 手工验收 |
| 17 | [17-cli-and-tails.md](17-cli-and-tails.md) | `sync` / `drift` / `pause` / `resume`；M0 尾巴两项（§1.3）；运维文档补充 | 包内单测 + 手工验收 |

→ 阶段二末尾真机验收 DoD 3、4、5、6、11、12、15

**依赖顺序**

```
01 ─┬→ 03 ─┐
02 ─┤      ├→ 04 ─┬───────────────→ 08 ─→ 09 ─→ 10
    └→ 05 ─┴→ 06 ─┴→ 07 ───────────↗
              └→ 11 ─→ 12 ─→ 13 ─→ 14 ─→ 15 ─→ 16 ─→ 17
```

- 01 与 02 互不依赖，可并行。05 只需要 02。
- 03 需要 01+02；**04 需要 01+02+03+05**（`Validate` 用 `internal/manifest`，`refsOf` 用凭据集合）。
- 06 需要 02+05；07 需要 05+06；08 需要 01–07 全部。
- 11 需要 06；12 需要 05+06+07（读 `state`）+11；13 需要 08+12；14 需要 04+13；15 需要 08+14。
- 10 需要 04+08+09；16 需要 14+15；17 需要 12+15。

**分批建议**：01–02 一批；03、05、04 一批（注意 05 在 04 之前）；06–07 一批；08 单独；09 单独；10 单独；11–12 一批；13–15 一批；16 单独；17 单独。

---

## 全局接口契约

跨子计划引用的类型与签名在此定稿。**子计划里的 `Interfaces` 块必须与本表逐字符一致**；发现不一致以本表为准，并回头修正子计划。

### `protocol`（子计划 02）

```go
const (
    KindConfigNotify   Kind = 10
    KindConfigPull     Kind = 11
    KindConfigSnapshot Kind = 12
    KindBlobRequest    Kind = 13
    KindBlobData       Kind = 14
    KindApplyAck       Kind = 15
    KindDriftReport    Kind = 16
    KindDriftCommand   Kind = 17
    KindCollectRequest Kind = 18
    KindCollectResult  Kind = 19
)

// 尺寸上限（spec §5.4）。两侧共用同一份常量。
const (
    MaxFileSize  = 512 << 10 // 单个受管文件
    MaxBatchSize = 256 << 10 // 携带内容的消息的分批阈值
    MaxPayload   = 1 << 20   // WS ReadMaxPayloadSize
)

// ApplyResult.Action
const (
    ActionSkip      uint8 = 1
    ActionCreate    uint8 = 2
    ActionOverwrite uint8 = 3
    ActionDelete    uint8 = 4
    ActionMerge     uint8 = 5
)

// DriftItem.Kind
const (
    DriftAdded    uint8 = 1
    DriftModified uint8 = 2
    DriftDeleted  uint8 = 3
)

// ConfigSnapshot.Mode
const (
    ModeApply  uint8 = 0
    ModeSurvey uint8 = 1
)

// ConfigNotify.Reason
const (
    ReasonPublished = "published"
    ReasonAdopted   = "adopted"
    ReasonRotated   = "rotated"
    ReasonAssigned  = "assigned"
)

// DriftCommand.Op
const (
    OpRestore = "restore"
    OpIgnore  = "ignore"
)

// CollectResult / Expansion 的跳过原因
const (
    SkipTooLarge       = "too_large"
    SkipNotRegular     = "not_regular"
    SkipAlwaysExcluded = "always_excluded"
    SkipUnreadable     = "unreadable"
)
```

消息结构逐字段照抄 spec §5.2，此处不复制。`MachineInfo` 追加 `LocalPaused bool` (5) 与 `ManagedHome string` (6)。

```go
// protocol/placeholder.go —— 只有词法、转义、引用提取与词法的逆运算
type RefKind uint8

const (
    RefCred    RefKind = 1
    RefVar     RefKind = 2
    RefMachine RefKind = 3
)

func (k RefKind) String() string // "cred" / "var" / "machine"

type Ref struct {
    Kind RefKind
    Name string
}

func (r Ref) String() string // "cred.anthropic_key"

// Segment 要么是字面段（Ref == nil），要么是一处引用。
type Segment struct {
    Text string
    Ref  *Ref
}

var ErrBadPlaceholder = errors.New("protocol: 占位符语法错误")

// MachineKeys 是 {{machine.*}} 允许的全部名字。
var MachineKeys = []string{"name", "hostname", "os", "arch"}

func Parse(content []byte) ([]Segment, error)
func Refs(content []byte) ([]Ref, error) // 去重，按 Ref.String() 升序
func Render(segs []Segment, lookup func(Ref) (string, bool)) ([]byte, error)
func EscapeLiteral(s string) string // 把字面的 "{{" 写成 "{{{{"

type MissingRefError struct{ Ref Ref }

func (e *MissingRefError) Error() string

// protocol/checksum.go
// 口径：对每个文件取 path\x00hash\x00mode\n（mode 为八进制、无前导 0），
// 按 path 字典序拼接后整体 sha256，hex 输出。两侧必须逐位一致。
func Checksum(files []FileEntry) string
```

### `internal/manifest`（子计划 05，中立包，hub 与 agent 都 import）

```go
type Mode string

const (
    ModeFile Mode = "file"
    ModeTree Mode = "tree"
    ModeKeys Mode = "keys"
)

type Include struct {
    Path string   `json:"path"`
    Mode Mode     `json:"mode"`
    Keys []string `json:"keys,omitempty"`
}

type Manifest struct {
    Version int       `json:"version"`
    Include []Include `json:"include"`
    Exclude []string  `json:"exclude,omitempty"`
}

func Default() Manifest                  // spec §3.1 的那份
func Parse(b []byte) (Manifest, error)   // 含 Validate
func (m Manifest) Validate() error
func (m Manifest) JSON() ([]byte, error) // 稳定序列化，用于冻结进 revision

// AlwaysExcluded 是恒排除清单（spec §3.2）。用户不可去除。
func AlwaysExcluded() []string
func IsAlwaysExcluded(rel string) bool

// SafeRelPath 拒绝绝对路径与任何 ".." 段。
func SafeRelPath(rel string) error

// ResolveUnder 在 EvalSymlinks 之后确认 abs 仍在 root 之下。
func ResolveUnder(root, rel string) (abs string, err error)

// Match 报告 rel 命中哪条 include。判定顺序：恒排除 > exclude > include。
func (m Manifest) Match(rel string) (Include, bool)

func MatchGlob(pattern, rel string) bool // 支持 ** 跨层匹配

type Entry struct {
    Rel  string
    Abs  string
    Inc  Include
    Mode os.FileMode
}

type Skip struct {
    Rel    string
    Reason string // protocol.Skip* 之一
}

type Expansion struct {
    Files   []Entry // 按 Rel 升序
    Skipped []Skip
}

// Expand 在真实文件系统上展开 manifest。root 是 managed home。
func (m Manifest) Expand(root string) (Expansion, error)
```

### `hub/internal/blobs`（子计划 01）

```go
var ErrNotFound = errors.New("blobs: 内容不存在")

func Hash(content []byte) string // sha256 hex

type Store struct{ /* ... */ }

func New(app core.App) *Store
func (s *Store) Put(content []byte) (hash string, err error)
func (s *Store) PutTx(txApp core.App, content []byte) (hash string, err error)
func (s *Store) Get(hash string) ([]byte, error)
func (s *Store) Has(hash string) (bool, error)
func (s *Store) GCOrphans(setID string) (int, error)
```

### `hub/internal/credentials`（子计划 03）

```go
const MinValueLen = 8

var (
    ErrNotFound   = errors.New("credentials: 凭据不存在")
    ErrInUse      = errors.New("credentials: 凭据仍被引用")
    ErrShortValue = errors.New("credentials: 凭据值过短")
    ErrBadName    = errors.New("credentials: 凭据名非法")
)

func LoadMasterKey(dataDir string) ([]byte, error) // ORCINY_SECRET_KEY 优先，否则 pb_data/secret.key
func Encrypt(key []byte, plaintext string) (string, error)
func Decrypt(key []byte, cipherB64 string) (string, error)

type Store struct{ /* ... */ }

func NewStore(app core.App, key []byte, ev *events.Writer) *Store
func (s *Store) VerifyAll() error // 启动自检：解不开任一条即报错
func (s *Store) Create(name, value, note string) (*core.Record, error)
func (s *Store) Rotate(name, value string) error
func (s *Store) Delete(name string) error
func (s *Store) Value(name string) (string, error)
func (s *Store) Values(names []string) (map[string]string, error)
func (s *Store) ReferencedBy(name string) (sets []string, revs []string, err error)

func (s *Store) MachineVariables(machineID string) (map[string]string, error)
func (s *Store) SetVariable(machineID, key, value string) error
func (s *Store) DeleteVariable(machineID, key string) error
```

### `hub/internal/configsets` 与 `hub/internal/revisions`（子计划 04）

```go
package configsets

type Problem struct {
    Path   string
    Kind   string // always_excluded / too_large / undefined_ref / bad_path
    Detail string
}

type Service struct{ /* ... */ }

func NewService(app core.App, b *blobs.Store, ev *events.Writer) *Service
func (s *Service) Create(name, note string) (*core.Record, error)
func (s *Service) SetDraftFile(setID, path string, content []byte, mode uint32, keys []string) (protocol.FileEntry, error)
func (s *Service) RemoveDraftFile(setID, path string) error
func (s *Service) Draft(setID string) ([]protocol.FileEntry, error)
func (s *Service) SetDraft(setID string, files []protocol.FileEntry) error
func (s *Service) Manifest(setID string) (manifest.Manifest, error)
func (s *Service) SetManifest(setID string, m manifest.Manifest) error
func (s *Service) Validate(setID string, known map[string]bool) ([]Problem, error)
func (s *Service) Assign(machineID, setID, mode string) (*core.Record, error)
func (s *Service) AssignedMachines(setID string) ([]string, error)
func (s *Service) Assignment(machineID string) (*core.Record, error)

package revisions

type FileChange struct {
    Path     string
    Kind     string // added / removed / modified
    FromHash string
    ToHash   string
}

type Service struct{ /* ... */ }

func NewService(app core.App, b *blobs.Store, ev *events.Writer) *Service
func (s *Service) Publish(setID, note, source string) (*core.Record, error)
func (s *Service) PublishFiles(setID string, files []protocol.FileEntry, note, source string) (*core.Record, error)
func (s *Service) Rollback(setID, revisionID string) (*core.Record, error)
func (s *Service) Files(revID string) ([]protocol.FileEntry, error)
func (s *Service) Head(setID string) (*core.Record, error)
func Diff(from, to []protocol.FileEntry) []FileChange
```

`revisions.source` 取值：`publish` / `adopt` / `rollback` / `import`。
`assignments.state` 取值：`pending` / `applying` / `aligned` / `failed` / `degraded` / `paused`。
`assignments.mode` 取值：`apply` / `survey`。

### `hub/internal/configsync`（子计划 08）

```go
// Sender 是「往某台机器发一条消息」的能力，由 *machines.Manager 实现。
type Sender interface {
    SendTo(machineID string, kind protocol.Kind, payload any) error
    Online(machineID string) bool
}

type Deps struct {
    App    core.App
    Blobs  *blobs.Store
    Sets   *configsets.Service
    Revs   *revisions.Service
    Creds  *credentials.Store
    Events *events.Writer
    Sender Sender
    Logger *slog.Logger
}

type Service struct{ /* ... */ }

func NewService(d Deps) *Service
func (s *Service) NotifyConfigSet(setID, revisionID, reason string) error
func (s *Service) NotifyMachine(machineID, reason string) error
func (s *Service) NotifyCredential(name string) error
func (s *Service) Snapshot(machineID string) (protocol.ConfigSnapshot, error)

// ws.AgentMessages 的实现
func (s *Service) Pull(machineID string, p protocol.ConfigPull)
func (s *Service) BlobRequest(machineID string, r protocol.BlobRequest)
func (s *Service) ApplyAck(machineID string, a protocol.ApplyAck)
func (s *Service) DriftReport(machineID string, d protocol.DriftReport)
func (s *Service) CollectResult(machineID string, c protocol.CollectResult)
func (s *Service) OnMachineOnline(machineID string)
```

### `hub` 包的公开入口（`hub/api.go`，子计划 08、09、14）

与 M0 的 `IssueEnrollToken` 同性质：**hub 对外暴露的管理动作**。HTTP 路由层只做编解码与状态码映射，业务规则一律留在这里（M0 spec §5.2 的分工），测试脚手架也走同一组入口。

```go
func (h *Hub) AssignConfigSet(machineID, setID, mode string) error
func (h *Hub) PublishConfigSet(setID, note string) (revisionID string, err error)
func (h *Hub) RollbackConfigSet(setID, revisionID string) (newRevisionID string, err error)
func (h *Hub) CloneConfigSet(setID, newName string) (string, error)
func (h *Hub) DeleteConfigSet(setID string) error // 删除后调 blobs.GCOrphans(setID)
func (h *Hub) CreateCredential(name, value, note string) error
func (h *Hub) RotateCredential(name, value string) error
func (h *Hub) DeleteCredential(name string) error
func (h *Hub) StartImport(machineID string) (token, setID string, err error)
func (h *Hub) ImportFindings(setID string) ([]importer.Finding, error)
func (h *Hub) ExtractCredential(setID, path, location, name string) error
func (h *Hub) AdoptDrift(eventIDs []string) (revisionID string, err error)   // 子计划 14
func (h *Hub) RestoreDrift(eventIDs []string) error                          // 子计划 14
func (h *Hub) IgnoreDrift(eventIDs []string, global bool) error              // 子计划 14
func (h *Hub) ClearDegraded(machineID string) error                          // 子计划 15
```

### `hub/internal/ws` 与 `hub/internal/machines` 的增量（子计划 08）

```go
package ws

// AgentMessages 是 ws 对业务层的全部需求。由 *configsync.Service 实现。
// 接口定义在 ws 侧，configsync 反向满足它——避免 ws 与 configsync 互相 import。
type AgentMessages interface {
    Pull(machineID string, p protocol.ConfigPull)
    BlobRequest(machineID string, r protocol.BlobRequest)
    ApplyAck(machineID string, a protocol.ApplyAck)
    DriftReport(machineID string, d protocol.DriftReport)
    CollectResult(machineID string, c protocol.CollectResult)
}

// Deps 追加：
//   Agent AgentMessages

package machines

// Conn 接口追加：
//   Send(kind protocol.Kind, payload any) error

// Manager 追加（实现 configsync.Sender）：
func (m *Manager) SendTo(machineID string, kind protocol.Kind, payload any) error
func (m *Manager) Online(machineID string) bool
func (m *Manager) OnOnline(fn func(machineID string)) // 握手成功后回调，用于补发 ConfigNotify
```

### `hub/internal/importer`（子计划 09）

```go
type Finding struct {
    Path      string // 受管相对路径
    Location  string // 结构化位置，如 "settings.json:env.ANTHROPIC_AUTH_TOKEN"
    Key       string
    Masked    string // 前 4 后 4，中间固定 "…"
    Suggested string // 建议凭据名
    Rule      string // structured / key_name / value_prefix / value_entropy / fulltext
}

func Scan(path string, content []byte) []Finding
func Mask(v string) string
func SuggestName(key string) string

type Service struct{ /* ... */ }

// Sender 与 configsync.Sender 同形，在 importer 里重新声明（两者平级，不互相 import）
type Sender interface {
    SendTo(machineID string, kind protocol.Kind, payload any) error
    Online(machineID string) bool
}

func NewService(app core.App, b *blobs.Store, sets *configsets.Service, creds *credentials.Store, ev *events.Writer, sender Sender) *Service
func (s *Service) Start(machineID string) (token string, setID string, err error)
func (s *Service) HandleResult(machineID string, r protocol.CollectResult) error
func (s *Service) Findings(setID string) ([]Finding, error)
func (s *Service) Extract(setID, path, location, credName string) error
```

### `hub/internal/drift`（子计划 13、14）

```go
var ErrConflict = errors.New("drift: 多台机器改了同一路径，必须先做三方对比")

type Deps struct {
    App    core.App
    Blobs  *blobs.Store
    Sets   *configsets.Service
    Revs   *revisions.Service
    Events *events.Writer
    Sync   *configsync.Service
    Logger *slog.Logger
}

type Service struct{ /* ... */ }

func NewService(d Deps) *Service
func (s *Service) HandleReport(machineID string, rep protocol.DriftReport) error
func (s *Service) Adopt(eventIDs []string) (*core.Record, error)
func (s *Service) Restore(eventIDs []string) error
func (s *Service) Ignore(eventIDs []string, global bool) error
func (s *Service) Supersede(machineID string, paths []string, revID string) error
func (s *Service) IgnorePaths(machineID string) ([]string, error)
func UnifiedDiff(path string, base, cur []byte) string
```

### agent 侧（子计划 05–07、11–12）

```go
// agent.Config 追加
type Config struct {
    HubURL            string `yaml:"hub_url"`
    MachineID         string `yaml:"machine_id"`
    LogFile           bool   `yaml:"log_file"`
    ManagedHome       string `yaml:"managed_home"`       // 空 = os.UserHomeDir()
    ReconcileInterval string `yaml:"reconcile_interval"` // 空 = "5m"
}

func (c *Config) ManagedHomeDir() (string, error)
func (c *Config) ReconcileEvery() time.Duration

// agent/internal/state
const (
    HealthOK       = "ok"
    HealthDegraded = "degraded"
)

type FileState struct {
    Blob     string   `json:"blob"`
    Rendered string   `json:"rendered"`
    Mode     uint32   `json:"mode"`
    Size     uint32   `json:"size,omitempty"`
    Keys     []string `json:"keys,omitempty"`
}

type State struct {
    ConfigSet string               `json:"config_set"`
    Revision  string               `json:"revision"`
    Seq       uint32               `json:"seq"`
    Checksum  string               `json:"checksum"`
    AppliedAt time.Time            `json:"applied_at"`
    Mode      string               `json:"mode"`
    Health    string               `json:"health"`
    Files     map[string]FileState `json:"files"`
    Ignored   []string             `json:"ignored"`
    Paused    bool                 `json:"paused"`
    // Manifest 随快照冻结（spec §4.1）。watcher 展开受管范围、恢复时定位
    // 基线都要用它——存 revision id 而不存 manifest 本身，agent 离线时
    // 就没法对账了。
    Manifest json.RawMessage `json:"manifest,omitempty"`
}

func Path(dir string) string
func Load(dir string) (*State, error) // 不存在时返回包装了 os.ErrNotExist 的错误
func Save(dir string, s *State) error // 0600 原子写

// agent/internal/secrets
type File struct {
    Creds   map[string]string `json:"creds"`
    Vars    map[string]string `json:"vars"`
    Machine map[string]string `json:"machine"`
}

func Path(dir string) string
func Load(dir string) (*File, error) // 不存在 → 空 File，不报错
func Save(dir string, f *File) error // 0600 原子写
func (f *File) Lookup(r protocol.Ref) (string, bool)
func (f *File) Equal(o *File) bool

// agent/internal/blobcache
type Cache struct{ /* ... */ }

func New(dir string) *Cache
func (c *Cache) Has(hash string) bool
func (c *Cache) Get(hash string) ([]byte, error)
func (c *Cache) Put(content []byte) (string, error)
func (c *Cache) PutHash(hash string, content []byte) error // 内容与 hash 不符即报错
func (c *Cache) Missing(hashes []string) []string

// agent/internal/render
func Render(content []byte, look func(protocol.Ref) (string, bool)) ([]byte, error)

type RestoreResult struct {
    Content []byte
    Partial bool // 有变量未能还原
    Safe    bool // 全部已知凭据值都已替出；false 时调用方不得上报内容
}

func Restore(content []byte, creds, vars map[string]string) RestoreResult

// agent/internal/applier
type FS interface {
    Write(path string, data []byte, perm os.FileMode) error
    Read(path string) ([]byte, error)
    Remove(path string) error
    Stat(path string) (os.FileInfo, error)
}

func OSFS() FS

type Step struct {
    Rel      string
    Action   uint8 // protocol.Action*
    Blob     string
    Rendered string
    Mode     os.FileMode
    Keys     []string
    Content  []byte // 已渲染，Action 为 skip/delete 时为 nil
}

type Plan struct {
    Steps []Step
    Skip  []manifest.Skip
}

type Options struct {
    Dir         string // agent 目录（~/.orciny）
    ManagedHome string
    Clock       clock.Clock
    Logger      *slog.Logger
    FS          FS
}

type Applier struct{ /* ... */ }

func New(o Options) *Applier
func BuildPlan(snap protocol.ConfigSnapshot, st *state.State, content map[string][]byte, look func(protocol.Ref) (string, bool)) (Plan, error)
func (a *Applier) Apply(snap protocol.ConfigSnapshot, p Plan, st *state.State) (protocol.ApplyAck, *state.State)

// agent/internal/watcher
type Options struct {
    Dir         string
    ManagedHome string
    Clock       clock.Clock
    Debounce    time.Duration // 0 → 2s
    Reconcile   time.Duration // 0 → 5m
    Throttle    time.Duration // 0 → 30s
    Report      func(items []protocol.DriftItem, full bool) error
    Logger      *slog.Logger
}

type Watcher struct{ /* ... */ }

func New(o Options) (*Watcher, error)
func (w *Watcher) Run(ctx context.Context) error
func (w *Watcher) Reload(st *state.State, m manifest.Manifest, sec *secrets.File) error
func (w *Watcher) Scan(full bool) ([]protocol.DriftItem, error)

// agent/internal/syncer —— 拥有 agent 侧配置闭环的全部状态
type Deps struct {
    Dir               string
    ManagedHome       string
    Clock             clock.Clock
    Logger            *slog.Logger
    FS                FS
    Send              func(kind protocol.Kind, payload any) error
    MachineName       string
    ReconcileInterval time.Duration // 0 → agent.DefaultReconcileInterval（子计划 13 加）
}

type Syncer struct{ /* ... */ }

func New(d Deps) (*Syncer, error)
func (s *Syncer) Handle(env protocol.Envelope) // ConfigNotify/Snapshot/BlobData/DriftCommand/CollectRequest
func (s *Syncer) SyncNow() error
func (s *Syncer) Report(items []protocol.DriftItem, full bool) error
func (s *Syncer) StartWatcher(ctx context.Context) error // 子计划 13
func (s *Syncer) ReconcileNow() error                    // 子计划 13
func (s *Syncer) State() *state.State
```

### `internal/testsupport` 的增量

```go
// 签名变更（spec §2.3 的硬要求）：managed home 必须由调用方显式给出。
func NewTestAgent(t *testing.T, th *TestHub, managedHome string) *TestAgent

type TestAgent struct {
    Dir         string
    ManagedHome string // 新增
    IdentityDir string
    Fingerprint string
    MachineID   string
}

// 子计划 08 追加
func (a *TestAgent) RunSync(t *testing.T, th *TestHub) *agent.Session // 连接并跑起 syncer
func (a *TestAgent) ReadManaged(t *testing.T, rel string) []byte
func (a *TestAgent) WriteManaged(t *testing.T, rel string, content []byte, perm os.FileMode)
func (a *TestAgent) State(t *testing.T) *state.State

// 子计划 04 追加
func (h *TestHub) SeedConfigSet(t *testing.T, name string, files map[string]string) (setID, revID string)
func (h *TestHub) RequireAssignmentState(t *testing.T, machineID, want string)
func (h *TestHub) RequireDrift(t *testing.T, machineID, path string) *core.Record
```

### 数据模型（子计划 01 建表，全程只增不改）

字段逐条照抄 spec §4.1。索引清单：

| collection | 索引 | 唯一 | where |
|---|---|---|---|
| `blobs` | `hash` | 是 | |
| `config_sets` | `name` | 是 | |
| `revisions` | `config_set, seq` | 是 | |
| `revisions` | `config_set` | 否 | |
| `assignments` | `machine` | 是 | |
| `assignments` | `config_set` | 否 | |
| `credentials` | `name` | 是 | |
| `variables` | `machine, key` | 是 | |
| `drift_events` | `machine, path` | 是 | `state = 'open'` |
| `drift_events` | `config_set, state` | 否 | |
| `ignore_rules` | `machine, path` | 否 | |

### `events.kind` 新增取值（子计划 01 一次性全部定义）

```go
const (
    KindConfigSetPublished  = "configset.published"
    KindConfigSetRolledBack = "configset.rolled_back"
    KindAssignChanged       = "assign.changed"
    KindApplyOK             = "apply.ok"
    KindApplyFailed         = "apply.failed"
    KindApplyRollbackFailed = "apply.rollback_failed"
    KindDriftReported       = "drift.reported"
    KindDriftAdopted        = "drift.adopted"
    KindDriftRestored       = "drift.restored"
    KindDriftIgnored        = "drift.ignored"
    KindDriftSuperseded     = "drift.superseded"
    KindCredentialCreated   = "credential.created"
    KindCredentialRotated   = "credential.rotated"
    KindCredentialDeleted   = "credential.deleted"
    KindImportCompleted     = "import.completed"
)
```

### HTTP API（子计划 04、08、09、13、14 陆续注册，全部 `Bind(apis.RequireSuperuserAuth())`）

```
POST   /api/orciny/config-sets                      {name, note} → 记录
POST   /api/orciny/config-sets/{id}/clone           {name} → 新配置集（复制 manifest 与 draft）
DELETE /api/orciny/config-sets/{id}                 删除并 blobs.GCOrphans(id)
POST   /api/orciny/config-sets/{id}/files           {path, content, mode, keys} → FileEntry
DELETE /api/orciny/config-sets/{id}/files           {path}
PUT    /api/orciny/config-sets/{id}/manifest        manifest JSON
POST   /api/orciny/config-sets/{id}/validate        → []Problem
POST   /api/orciny/config-sets/{id}/publish         {note} → revision
POST   /api/orciny/config-sets/{id}/rollback        {revision} → revision
GET    /api/orciny/config-sets/{id}/diff?from=&to=  from/to 为 revision id 或 "draft" → []FileChange + unified diff
GET    /api/orciny/blobs/{hash}                     → 原始内容（编辑器读文件）
POST   /api/orciny/assignments                      {machine, config_set, mode}
POST   /api/orciny/credentials                      {name, value, note}
POST   /api/orciny/credentials/{id}/rotate          {value}
DELETE /api/orciny/credentials/{id}
PUT    /api/orciny/machines/{id}/variables          {key: value}
POST   /api/orciny/machines/{id}/import             → {token, config_set}
POST   /api/orciny/config-sets/{id}/extract         {path, location, name}
POST   /api/orciny/drift/adopt                      {events: []} → revision
POST   /api/orciny/drift/restore                    {events: []}
POST   /api/orciny/drift/ignore                     {events: [], global: bool}
POST   /api/orciny/machines/{id}/clear-degraded
```

---

## 与 spec 的偏离记录

实现过程中若再产生偏离，追加到本表。

| # | spec 原文 | 计划的做法 | 理由 |
|---|---|---|---|
| 1 | §2.2「`placeholder.go` 只放三样……**不含**任何取值逻辑」 | 同时放 `Render(segs, lookup)` | 它是词法的逆运算，不含任何「值从哪来」的知识（值由调用方传的闭包提供）。不放的话 hub 与 agent 各写一份 15 行的回填循环，而 spec §6.1 的往返律要求两侧字节级一致——两份实现正是往返律最可能破掉的地方 |
| 2 | §3.2「恒排除硬编码在 agent 与 hub **双侧**，两处各有一份常量」 | 一份常量（`internal/manifest.AlwaysExcluded`），**两处独立的执行点**（hub 发布期校验 + agent 展开/采集期过滤），各有各的测试 | 纵深防御的实质是「agent 不信任 hub 的判断，自己再判一次」——agent 编译进自己的检查即可达成，与常量放几份无关。两份常量只多出一处会静默漂移的地方 |
| 3 | §2.1 把 `manifest` 列在 `agent/internal/` | 放顶层 `internal/manifest` | hub 发布期要用同一套 glob 与恒排除规则做校验（§3.2 自己也这么要求），而 hub 碰不到 `agent/internal/*`。定位同 `internal/clock`：中立于 hub/agent，不构成两者依赖 |
| 4 | §2.1 未列 `state` 包 | 新增 `agent/internal/state` | `state.json` 被 applier（写）、watcher（读基线）、CLI（`status` / `drift` / `pause`）三方共用。塞进 applier 会让 watcher 依赖 applier，而两者本无关系 |
| 5 | §2.1 未列 agent 侧的编排者 | 新增 `agent/internal/syncer` | ConfigNotify → Pull → 补 blob → apply → Ack 是一台状态机，且要跨消息保存「等哪些 hash」。挂在 `conn` 里会让连接层长出业务，挂在 `applier` 里会让它依赖网络 |
| 6 | §11「一个纯算法 diff 库（约 10 KB）算差异」 | 前端只在**编辑器实时预览**（草稿 vs head，边打字边算）用 jsdiff；漂移 diff 与版本间 diff 一律由 hub 用 `go-difflib` 算 | §8.2 已定「diff 由 hub 计算，算法只有一份实现」。编辑器里的内容还没落 blob，hub 算不了，这一处必须在前端 |
| 7 | §2.1 列了 `hub/internal/render`（「hub 侧只用于预览与校验」） | 不建这个包。校验落在 `configsets.Validate`（用 `protocol.Refs`），预览落在前端（`src/lib/placeholder.ts`） | hub 侧从不真的渲染——它手上没有 machine.* 的值，渲染出来的东西对任何一台机器都不成立。剩下的「校验」只是一次 `Refs` 调用，独立成包会是个空壳 |
| 8 | §2.2「占位符的取值逻辑留给 hub 与 agent 各自」 | 前端另有一份 TS 实现（`src/lib/placeholder.ts`） | 编辑器要在内容**还没提交到 hub** 时就给出未定义引用告警与补全，那时没有后端可问。两份实现的一致性靠双方各自的测试跑同一组例子（转义、非法名、machine 白名单）来钉住 |

## 实现期对计划代码的修正

子计划执行中与计划正文不一致、但正确的写法。详见各阶段提交与 [acceptance.md](acceptance.md)。

| # | 计划原文 | 实际做法 | 理由 |
|---|---|---|---|
| 1 | watcher 去抖测试「Notify 五次再 Advance 一次」即稳 | `waitReport`：若 Advance 后无上报且 debounce 仍在册则再推 2s | 假时钟下 `debounceC` 与 `manual` 可同时就绪，select 先走 manual 会丢弃已到期滴答 |
| 2 | probe 测试用 PATH 假 claude 壳脚本 | 解析类用例注入 `runClaudeVersion`；超时用例仍真 fork | 全量并行时 fork 壳脚本会被调度延迟拖过 3s 探测超时 |
| 3 | `Status` 结构体追加配置字段 | 配置字段从 `state.json` 现读打印，不写入 `status.json` | `status.json` 是连接态；配置态属于 `state.json` |
| 4 | `syncer.Deps` 无 `OnApplied` | 追加可选 `OnApplied func(ApplyAck, Plan)` | CLI `sync` 需要 plan 与回执，常驻进程不设 |

---

## 验收对照（spec §12）

| DoD # | 标准 | 由哪个子计划保证 |
|---|---|---|
| 1 | 导入向导 → 配置集 v1；敏感项抽成凭据；查库确认 blob 无明文 | 09、10 |
| 2 | 第二台机器 `apply` 指派 → 落盘 → 内容一致 | 07、08 |
| 3 | 第三台机器 `survey` 指派 → 不写盘，差异进收件箱 | 15 |
| 4 | 新增 `skills/foo/SKILL.md` → 60 秒内进收件箱 → 收编 → 其余机器落盘 | 12、13、14 |
| 5 | 改 CLAUDE.md → diff 正确 → 恢复 → 回到基线 | 13、14 |
| 6 | 改密钥值 → 上报内容不含明文 | 11、13 |
| 7 | `.claude.json` 非受管键保留且不重排 | 07 |
| 8 | apply 失败自动回滚 + 面板显示原因 | 07、08、16 |
| 9 | 凭据轮换不产生新 Revision、不产生漂移 | 03、08、12 |
| 10 | 发布 v3 → 回滚 v1 → 生成 v4 → 全机队对齐 | 04、08 |
| 11 | 跨机器同路径冲突强制三方对比 | 14、16 |
| 12 | 删 `state.json` → survey 全量对账，不覆盖用户文件 | 15 |
| 13 | `go test -tags=testing ./...` 与 `vitest run` 全绿 | 全部 |
| 14 | agent < 30 MB、hub < 64 MB | 17（实测记录） |
| 15 | M0 尾巴两项 | 17 |
