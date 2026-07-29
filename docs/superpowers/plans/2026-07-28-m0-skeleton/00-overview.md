# Orciny M0 · 骨架 —— 实现计划（统括）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **本文件是索引，不含可执行任务。** 实际任务在 `01-*.md` … `09-*.md` 九个子计划里，按编号顺序执行。每次只交给执行者一个子计划文件 + 本文件。

**Goal:** 交付 M0 骨架——hub 可部署、agent 一行接入、面板实时显示机队在线状态，并把模块边界、wire 协议、双向信任根、连接生命周期这四样地基一次立对。

**Architecture:** 单 Go module，`hub/` 与 `agent/` 各自把实现塞进自己的 `internal/` 子树，由 Go 的 internal 规则做编译期隔离；两者唯一的共享依赖是极瘦的 `protocol/`（CBOR 信封 + 消息结构 + 签名域分隔常量）。hub 以 PocketBase 为内核（`Hub` 嵌 `core.App`，子系统作字段，全部装配挂在 `OnServe`），前端 React 编译产物 `//go:embed` 进二进制。agent 常驻、主动外拨 WebSocket，Ed25519 双向挑战-应答建立连接。

**Tech Stack:** Go 1.26 · PocketBase v0.39.9 · gws v1.10.1 · fxamacker/cbor v2.9.2 · cobra v1.10.2 · blang/semver v4.0.0 · testify v1.11.1 · React 19 + Vite + TypeScript + Tailwind v4 + nanostores + Lingui + PocketBase JS SDK

**上位文档：** [M0 工程设计 spec](../../specs/2026-07-28-m0-skeleton-design.md)（下称「spec」）· [产品设计文档](../../../PRODUCT-DESIGN.md)

---

## Global Constraints

以下约束对**每一个任务**生效，子计划不再逐条重复。

**模块与版本**

- Go module 路径：`github.com/FlintyLemming/orciny`，逐字符一致，大小写敏感，不得更改。
- `go.mod` 的 `go` 指令：`go 1.26.0`。
- 依赖版本**锁死**，不得 `go get -u`：
  - `github.com/pocketbase/pocketbase v0.39.9`
  - `github.com/lxzan/gws v1.10.1`
  - `github.com/fxamacker/cbor/v2 v2.9.2`
  - `github.com/spf13/cobra v1.10.2`
  - `github.com/blang/semver/v4 v4.0.0`
  - `github.com/stretchr/testify v1.11.1`
  - `gopkg.in/yaml.v3 v3.0.1`
- PocketBase 升级是独立任务，任何子计划都不得顺手升级。

**模块边界（编译期强制，spec §2.2）**

- `agent/**` 与 `hub/**` 之间**不允许任何直接依赖**。
- 双方共享的东西只能放 `protocol/`。`protocol` 不含业务逻辑、不 import PocketBase、不 import hub 或 agent 的任何包。
- 顶层 `orciny.go` 只放常量/版本变量。
- `hub/internal/*` 的单元测试必须写在包内部（`hub/internal/ws/ws_test.go` 这种）；`internal/testsupport` 够不到它们，只做跨组件集成测试。

**命名与文案**

- 二进制名：hub = `orciny`，agent = `orciny-agent`。
- agent 目录：`~/.orciny/`。
- Docker 镜像名：`orciny`。
- 事件 kind 字符串（只允许这七个）：`machine.enrolled` / `machine.re-enrolled` / `machine.connected` / `machine.disconnected` / `machine.removed` / `token.issued` / `auth.failed`。
- 用户可见文案中英双语（Lingui），不得硬编码中文字符串在组件里。

**测试纪律（spec §12.3）**

- **不允许在测试里 `time.Sleep`**。所有超时与退避走注入的 `clock.Clock`，测试用 `clock.Fake` 推进逻辑时间。
- 等待「推进时钟后产生的可观测效果」用 `require.Eventually(t, cond, 2*time.Second, 5*time.Millisecond)`，不是 sleep 一个固定时长。
- 网络层的真实 deadline（gws 的 `SetDeadline`）无法伪造，因此全部做成 `hub.Config` / `conn.Config` 的字段，测试里填 200ms 级别的值。
- 测试命令统一为 `go test -tags=testing ./...`。
- 断言用 `testify/require`（失败即停），不用 `assert`。

**日志脱敏（spec §7.5）**

- 注册 token 在任何日志中只记前 8 位。
- 私钥内容、hub 公钥内容一律不入日志。

**提交**

- 每个任务的最后一步是一次 commit，message 用 Conventional Commits（`feat:` / `test:` / `chore:` / `docs:`），正文中文。
- 一个任务一次 commit，不要攒。

---

## 子计划索引

| # | 文件 | 交付物 | 验证方式 |
|---|---|---|---|
| 1 | [01-skeleton.md](01-skeleton.md) | go.mod、根包、`internal/clock`、三个 collection 的迁移、hub 装配骨架、agent 配置与原子写、agent CLI 骨架、`internal/testsupport` | `go test -tags=testing ./...`；hub 起得来、`/api/health` 200 |
| 2 | [02-protocol.md](02-protocol.md) | `protocol/` 全部：Envelope、Kind、M0 五种消息、codec、签名域分隔、指纹派生 | 包内单元测试（编解码往返、golden bytes、未知 Kind） |
| 3 | [03-identity.md](03-identity.md) | hub 密钥 Store（PEM 0600，缺则生成）、agent 密钥对与 hub 公钥 TOFU 钉扎 | 包内单元测试（权限位、幂等加载、指纹一致） |
| 4 | [04-enroll.md](04-enroll.md) | `events.Writer`、token 签发与核销、`/api/orciny/enroll-tokens`、`/api/orciny/enroll`、`/api/orciny/hub-info`、agent `enroll` 子命令与 TOFU 钉扎 | spec §12.2「enroll」全部七条用例 + 端到端 enroll 集成测试 |
| 5 | [05-handshake.md](05-handshake.md) | 握手状态机（纯函数式）、`ws.Handler`、`/api/orciny/ws`、agent 侧握手与 `Compromised` 终态 | spec §12.2「握手」全部七条用例 |
| 6 | [06-machines.md](06-machines.md) | `machines.Manager`：注册表、5 秒宽限、幽灵清理、删除联动、心跳与 `MachineInfo` | spec §12.2「连接生命周期」全部六条用例 |
| 7 | [07-agent-reconnect.md](07-agent-reconnect.md) | 退避与抖动、按 `Code` 分流、`probe` 探测、`run` / `status` 子命令、JSON 日志 | spec §12.2「agent 重连」全部四条用例 |
| 8 | [08-frontend.md](08-frontend.md) | 登录、布局壳与主题/语言、机器列表（realtime）、机器详情、添加机器弹窗、设置页 | 手工验收（spec §10.3 明示 M0 不写前端自动化测试） |
| 9 | [09-packaging.md](09-packaging.md) | Dockerfile、compose、goreleaser、`install.sh` 与 `/install.sh` 路由、systemd/launchd 单元、`make dev`、运维文档 | spec §13 的真机验收清单 |

**依赖顺序**：1 → 2 → 3 → 4 → 5 → 6 → 7，严格串行（每一步都依赖前一步产出的类型）。8 只依赖 1、4、6（collection 与路由已就位），可与 5–7 并行。9 依赖 1–8 全部完成。

**分批建议**：1–3 一批（地基，一次跑完不打断）；4–5 一批；6–7 一批；8 单独一批；9 单独一批。

---

## 全局接口契约

跨子计划引用的类型与签名在此定稿。**子计划里的 `Interfaces` 块必须与本表逐字符一致**；发现不一致以本表为准，并回头修正子计划。

### `github.com/FlintyLemming/orciny`（根包，计划 1）

```go
var Version = "0.1.0"                                 // var 而非 const：goreleaser 用 -ldflags 注入
const AppName = "orciny"
var MinAgentVersion = semver.MustParse("0.1.0")
```

### `protocol`（计划 2）

```go
type Kind uint8

const (
    KindHello       Kind = 1
    KindChallenge   Kind = 2
    KindAuth        Kind = 3
    KindAuthResult  Kind = 4
    KindMachineInfo Kind = 5
)

func (k Kind) IsKnown() bool
func (k Kind) String() string

type Envelope struct {
    Kind Kind            `cbor:"0,keyasint"`
    ID   *uint32         `cbor:"1,keyasint,omitempty"`
    Data cbor.RawMessage `cbor:"2,keyasint,omitempty,omitzero"`
}

type Hello struct       { AgentVersion string; Fingerprint string; ClientNonce []byte }
type Challenge struct   { ServerNonce []byte; HubSig []byte }
type Auth struct        { AgentSig []byte }
type AuthResult struct  { OK bool; Reason string; Code uint8 }
type MachineInfo struct { Hostname, OS, Arch, AgentVersion string; ToolVersions map[string]string }

const (
    CodeUnknownFingerprint uint8 = 1
    CodeBadSignature       uint8 = 2
    CodeVersionTooOld      uint8 = 3
    CodeMachineRemoved     uint8 = 4
)

func Encode(kind Kind, id *uint32, payload any) ([]byte, error)
func Decode(b []byte) (Envelope, error)
func DecodePayload[T any](env Envelope) (T, error)

const NonceSize = 32
func NewNonce(r io.Reader) ([]byte, error)
func HubSigPayload(clientNonce, serverNonce []byte) []byte    // "orciny-hub-v1" ‖ client ‖ server
func AgentSigPayload(serverNonce, clientNonce []byte) []byte  // "orciny-agent-v1" ‖ server ‖ client
func Fingerprint(pub ed25519.PublicKey) string                // base64url(SHA256(pub))[:32]
func EncodePublicKey(pub ed25519.PublicKey) string            // base64 std，wire/DB 统一表示
func DecodePublicKey(s string) (ed25519.PublicKey, error)
```

### `internal/clock`（计划 1）

```go
type Clock interface {
    Now() time.Time
    NewTimer(d time.Duration) Timer
    NewTicker(d time.Duration) Ticker
}
type Timer interface  { C() <-chan time.Time; Stop() bool; Reset(d time.Duration) bool }
type Ticker interface { C() <-chan time.Time; Stop() }

func System() Clock
func NewFake(now time.Time) *Fake
func (f *Fake) Now() time.Time
func (f *Fake) Advance(d time.Duration)
func (f *Fake) TimerCount() int
```

> **对 spec 的一处补充：** spec §2.2 只提到 `protocol/` 是共享包。`internal/clock` 放在仓库顶层 `internal/`，hub 与 agent 都能 import，但它**不是 hub 也不是 agent 的代码**，因此不构成两者之间的依赖——与 `internal/testsupport` 同样的定位。替代方案是在两侧各复制一份 30 行的 Clock，收益为负。

### `hub`（计划 1、4、5、6）

```go
type Config struct {
    DataDir           string          // 生产用；Attach 到 TestApp 时忽略
    Clock             clock.Clock     // nil → clock.System()
    OfflineGrace      time.Duration   // 0 → 5s
    HeartbeatInterval time.Duration   // 0 → 30s
    ReadTimeout       time.Duration   // 0 → 70s
    HandshakeTimeout  time.Duration   // 0 → 10s
    EnrollTokenTTL    time.Duration   // 0 → 15min
    MinAgentVersion   semver.Version  // 零值 → orciny.MinAgentVersion
    DownloadBase      string          // 计划 9 加：install.sh 的下载源，"" → GitHub releases
}

func New(cfg Config) (*Hub, error)                    // 生产：包 pocketbase.New()
func Attach(app core.App, cfg Config) (*Hub, error)   // 把子系统绑到任意 core.App（测试脚手架用）
func (h *Hub) Start() error
func (h *Hub) PublicKey() ed25519.PublicKey
func (h *Hub) IssueEnrollToken() (token string, expiresAt time.Time, err error)
func (h *Hub) Shutdown()                              // 计划 6 加：踢连接 → 等读循环 → 停定时器；已绑 OnTerminate
```

### `agent`（计划 1、4、7）

```go
type Config struct {
    HubURL    string `yaml:"hub_url"`
    MachineID string `yaml:"machine_id"`
    LogFile   bool   `yaml:"log_file"`
}

func DefaultDir() string                        // $ORCINY_HOME 或 ~/.orciny
func LoadConfig(dir string) (*Config, error)
func SaveConfig(dir string, c *Config) error
func Execute() error                            // cobra root：enroll / run / status / version

// 公开的 enroll 入口（计划 4）：CLI 与测试脚手架都走它
type EnrollOptions struct {
    HubURL, Token, ExpectHubKey, Dir string
    RetryDelay                       time.Duration
}
type EnrollResult struct{ MachineID, Fingerprint, HubKeyFingerprint string }
func Enroll(ctx context.Context, o EnrollOptions) (*EnrollResult, error)

// 公开的连接入口（计划 5）
type ConnectOptions struct {
    Dir, HubURL                   string
    HandshakeTimeout, ReadTimeout time.Duration
}
type Session = conn.Session
func Connect(ctx context.Context, o ConnectOptions) (*Session, error)
```

### `internal/testsupport`（计划 1，`//go:build testing`）

```go
type TestHub struct {
    App     *tests.TestApp
    Hub     *hub.Hub
    Clock   *clock.Fake
    Server  *httptest.Server
    HTTPURL string   // http://127.0.0.1:PORT
    WSURL   string   // ws://127.0.0.1:PORT/api/orciny/ws
}
func NewTestHub(t *testing.T, opts ...func(*hub.Config)) *TestHub

type TestAgent struct {
    Dir         string
    IdentityDir string
    Fingerprint string
    MachineID   string
}
func NewTestAgent(t *testing.T, th *TestHub) *TestAgent          // 计划 4：已完成 enroll
func (a *TestAgent) Connect(t *testing.T, th *TestHub) *agent.Session // 计划 5：完成握手的连接
```

### 数据模型（计划 1 建表，全程只增不改）

`machines`：`name` `fingerprint`(unique) `pub_key` `hostname` `os` `arch` `agent_version` `tool_versions`(json) `status`(select: online/offline/paused) `last_seen`(date) `created` `updated`(autodate)

`enroll_tokens`：`token_hash`(unique) `expires_at`(date) `used_at`(date) `machine`(relation→machines)

`events`：`kind`(text) `machine`(relation→machines, 可空) `detail`(json) `created`(autodate)

全部 collection 的 API rule 为 `nil`（仅 superuser 可访问，spec §5.3）。

---

## 与 spec 的偏离记录

实现过程中若再产生偏离，追加到本表。

| # | spec 原文 | 计划的做法 | 理由 |
|---|---|---|---|
| 1 | §2.2 只有 `protocol/` 共享 | 增加顶层 `internal/clock` 与 `internal/atomicfile` | 两者中立于 hub/agent，不构成两者依赖；替代方案是各复制一份 |
| 2 | §3.4 `Version` 为 `const` | 改 `var` | goreleaser 需要 `-ldflags -X` 注入版本 |
| 3 | §5.1 `h.App.(*pocketbase.PocketBase).Start()` | `Hub` 多存一个 `pb *pocketbase.PocketBase` 字段 | 去掉运行时类型断言；`Attach` 到 `tests.TestApp` 时该字段为 nil，`Start()` 显式报错 |
| 4 | §3.1 protocol「只有数据结构和编解码」 | 指纹派生 `protocol.Fingerprint` 也放 protocol | 指纹是 wire 上的身份格式，两侧必须逐位一致；放任一侧都会导致另一侧复制实现 |
| 5 | §12.1 `NewTestHub` 包 `tests.TestApp` | 同时提供 `hub.Attach`，由 testsupport 用 `apis.NewRouter` + `OnServe().Trigger` + `httptest.Server` 组装 | `tests.TestApp` 不自带 HTTP 服务；这是 PocketBase 自身 `apis.Serve` 的同款装配路径，能跑真实路由与 WS 升级 |
| 6 | spec 未定义 hub 的关停路径 | 新增 `Hub.Shutdown()`（踢连接 → 等 WS 读循环退出 → `machines.Manager.Stop()`），绑到 `OnTerminate`；配套 `ws.Handler.CloseAll/Wait`，`Manager.Stop` 由「摘定时器」升级为「摘定时器并等在途协程收工」 | 计划 6 把真的 `Manager` 接进 `ws` 之后，读循环与置离线协程都会写库。没有关停入口就没有「先停写、再拆库」的次序：`httptest.Server.Close` 不等被劫持的连接（Go 在 `StateHijacked` 直接 `wg.Done`），`t.Cleanup` 拆掉 `TestApp` 后在途的写库会让 PocketBase 对着 nil 的 DB 解引用而段错误。生产上同属真实缺口，计划 9 的服务单元也需要它 |

### 实现期对计划代码的修正

下面这些不是对 spec 的设计偏离，而是执行子计划时发现计划正文里给的代码编译不过、或者断言恒真/恒假，被迫改的写法。对外接口契约一律未变，登记在此以免后续计划照抄同样的错误。

| # | 出处 | 计划原文 | 实现的做法 | 理由 |
|---|---|---|---|---|
| A | 01 Task 2 `internal/clock/fake.go` | `func (f *Fake) NewTicker(d time.Duration) Ticker { return f.add(d, d) }` | 返回 `fakeTicker{f.add(d, d)}`，新增内嵌 `*fakeTimer` 的 5 行适配器只覆写 `Stop` | `fakeTimer.Stop() bool` 不满足 `Ticker.Stop()`（无返回值），原式编译不过。`Clock` / `Timer` / `Ticker` 三个接口本身不变 |
| B | 01 Task 4 `hub/config.go` | `if (c.MinAgentVersion == semver.Version{})` | `if c.MinAgentVersion.EQ(semver.Version{})` | `semver.Version` 含 `Pre []PRVersion` 与 `Build []string` 两个切片字段，类型不可比较，`==` 编译不过 |
| C | 01 Task 1 `.gitignore` | 「Create `.gitignore`」 | 合并进已有文件，保留原有的 OS/编辑器条目 | 仓库里已存在 `.gitignore`；覆盖会丢掉 `*.swp` / `Thumbs.db` 等条目 |
| D | 04 Task 1 `writer_test.go`、04 Task 2 `issue_test.go` | `rec.GetString("detail.prefix")` / `rec.GetString("detail.token_prefix")` | `rec.UnmarshalJSONField("detail", &m)` 后取 map 的键 | PocketBase 的 `JSONField` 没有实现 `GetterFinder`，`Record.Get` 拿点号 key 一律落到 `GetRaw` 返回空串——原式不报错但恒过不了断言。后续计划凡是断言 JSON 字段的子键都要走 `UnmarshalJSONField` |
| E | 04 Task 3 `consume.go` | `handleNotConsumed(tx, hash, pubKey string, now types.DateTime, out *Result)` | 去掉 `now` 参数 | 该参数在函数体里从未使用；核销时刻已由前面那条 UPDATE 写进库了 |
| F | 05 Task 4 Step 3 `dial.go` | `go socket.ReadLoop()` 紧跟在 `gws.NewClient` 之后，`h.session = session` 在其后 | 先挂 `h.session`，再起 `ReadLoop` | `OnClose` 在读循环那个 goroutine 上读 `h.session`，原顺序是真数据竞争，`-race` 必报 |
| G | 05 Task 3 Step 5 `handler.go` | `NewHandler` 只对 `Clock` / `Registry` 兜底 | 三个 `time.Duration` 字段 `<= 0` 时也兜底 | `HeartbeatInterval` 为 0 会让 `time.NewTicker` panic，而它跑在 goroutine 里，会连带整个 hub 进程一起死。生产走 `hub.Config.WithDefaults` 碰不到，但 `ws.Deps` 是导出类型，挡一手成本极低 |
| H | 05 Task 3 Step 7 `routes.go` | 无条件 `g.GET("/ws", ...)` | `if d.WS != nil` 才注册 | `routes.Deps` 被 `routes_test.go` 直接构造（不带 WS），留一条一被访问就 nil deref 的路由不如不注册 |
| I | 05 Task 3 Step 2 `ws_test.go` | httptest handler 里 `require.NoError(t, h.UpgradeHTTP(w, r))` | 改为 `_ = h.UpgradeHTTP(w, r)` | `require` 失败会调 `t.FailNow`，而它只允许在测试 goroutine 上调用；在 HTTP handler 里调用属于未定义行为，且会盖掉真正的失败信息 |
| J | 05 Task 4 Step 1 `dial_test.go` | `fakeHandler` 带 `clientNonce []byte` 字段 | 删掉该字段 | 只写不读；且 handler 实例被所有连接共享，留着会在多连接场景下变成数据竞争 |
| K | 06 Task 1 `manager_test.go` | `require.Equal(t, "2.1.3", r.GetString("tool_versions.claude-code"))` | `r.UnmarshalJSONField("tool_versions", &tools)` 后取键 | 与 D 同一个坑：`JSONField` 读不了点号 key，原式恒过不了断言 |
| L | 06 Task 3 `lifecycle_test.go` | `require.Eventually(TimerCount >= 1)` 之后 `Clock.Advance(6s)` | 新增 `(*TestHub).AdvanceUntilStatus`，在轮询里每轮推进一点 | WS 的心跳 ticker 与宽限定时器共用同一只假时钟，连接一上线 `TimerCount` 就不为零，这个守卫恒真、证明不了宽限定时器已挂上。定时器挂在 WS 读循环那个 goroutine 上，一次性推进会赶在它前面，实测每 10 次必挂 1–2 次 |
| M | 06 Task 1 Step 1、Task 3 Step 3 | 等到 status 变化后直接 `require.Equal(1, eventCount(...))` / `require.Contains(EventKinds(...))` | 改等事件本身：新增 `(*TestHub).RequireEvent` 与包内 `fixture.countEvents`，用 `Eventually` 轮询 | `Manager` 是先写状态再写事件的两次独立写库，「状态已经翻了」的那一刻事件可能还没落地。另注意 `Eventually` 的条件函数跑在它自己的 goroutine 上，里面不能调 `require`（同 I） |
| N | 07 Task 2 Step 1 `client_test.go` | `dialRecorder` 记录每次拨号时刻，`clk.Advance(time.Minute)` 一次推过，事后断言 `gaps[i]` | 新增 `waitAndFire(t, clk, d, want)`：等定时器挂上 → 推到差 1ms（此刻定时器必须还在册、且未重拨）→ 推完 1ms（必须重拨）。`dialRecorder` 只留 `count`，删掉 `times` / `gaps` | `clock.Fake.Advance` 触发到期定时器之后会把 `now` 一路推到目标时刻，于是重拨时读到的 `Now()` 恒等于推进量，`gaps` 断言恒真、证明不了等待时长。改后是逐次精确断言，比原式更强。另：基准计数必须在「定时器已挂上」之后取，否则会把首次拨号算进去 |
| O | 07 Task 2 Step 2 | `NewTestSession` / `CloseForTest` 追加进 `agent/internal/conn/dial.go` | 移到 `agent/internal/conn/export_test.go`（仅测试构建存在）；`dial.go` 里新增未导出的 `(*Session).finish(err)`，`Close()` 与 `handler.OnClose` 都改走它 | 测试构造器不该留在生产代码的对外接口上。顺带把「记录结束原因 + 关 done」收敛成一处，`handler.closeOnce` 只剩 session 为 nil 的兜底分支 |
| P | 07 Task 4 Step 5 `run.go` | 同时 `import "crypto/rand"` 与 `"math/rand/v2"` | `crand "crypto/rand"` | 两个包的默认名都是 `rand`，原式重复声明，编译不过 |
| Q | 07 Task 3 Step 1 `claude_test.go` | 伪造脚本内容 `sleep 30` | 脚本改成 `exec sleep 30`，并在 `claude.go` 里设 `cmd.WaitDelay = time.Second` | macOS 的 `/bin/sh`（bash 3.2）不会 exec 脚本的最后一条命令，被 `CommandContext` 杀掉的是 shell，`sleep` 成孤儿并继续攥着 stdout 管道，`Output()` 要等满 30 秒才返回——用例会「通过」但跑 30 秒，且掩盖了真实的挂起风险。`WaitDelay` 是同一问题在生产侧的兜底 |
| R | 07 Task 4 Step 6 `cli.go` | `run` / `status` 直接 `LoadConfig(dir)` | 经 `loadConfigForCmd(dir)` 包一层，`os.ErrNotExist` 翻成「尚未 enroll：… 请先执行 orciny-agent enroll」 | Task 4 Step 10 的手工验收要求「明确报尚未 enroll」，裸 ENOENT 达不到；`TestStatusCommandFailsWithoutEnroll` 相应加断错误文案 |

| S | 08 Task 1 Step 1 | `npm create vite@latest . -- --template react-ts` | 手写 `package.json` / `index.html` / `tsconfig.json` / `src/main.tsx`，再 `npm install` 各依赖 | `hub/internal/site/` 里已有 `embed.go` 与 `dist/`，脚手架拒绝在非空目录里跑，且它是交互式的 |
| T | 08 Task 1 Step 2 `vite.config.ts` | `server: { port: 5173, strictPort: true, proxy: {...} }` | 补 `host: '127.0.0.1'` | Vite 默认的 `localhost` 在 macOS 上只落到 `::1`，而 `site.DevTarget` 走 `127.0.0.1`，dev 反代必然 502。实测复现 |
| U | 08 Task 2 Step 1 `lingui.config.ts` | `format: 'po'`，且文件在 Task 2 才创建 | `format: formatter({ lineNumbers: false })`（引入 `@lingui/format-po`），且文件提前到 Task 1 | Lingui v6 的 `format` 收 `CatalogFormatter` 对象，字符串写法类型不过。另外 Task 1 Step 2 就把 `lingui()` 插件挂进了 vite.config，缺这个文件 Task 1 Step 7 的 `npm run build` 直接起不来 |
| V | 08 Task 7 Step 2 `.gitignore` | 「确认保留 `hub/internal/site/dist/*` 与 `!.../dist/index.html`」 | 把根部的 `dist/` 改成 `/dist/` | 两行本身在，但恒久失效：裸 `dist/` 在任意层级匹配，把 `hub/internal/site/dist/` 整个排除，而被排除目录里的文件无法再用 `!` 捞回来。占位 `index.html` 从来没进过库，新克隆的仓库 `//go:embed all:dist` 直接编译失败——已实测复现。改钉根目录后恢复正常 |
| W | 08 Task 4 Step 1 `stores/machines.ts` | 模块级 `let unsub` 在 `.then` 里赋值，退订时判 `if (refCount === 0 && unsub)` | 改存 `Promise<() => void>`，退订走 `pending.then((fn) => fn())` | `subscribe()` 是异步的，而 StrictMode 下 React 会「挂载→卸载→再挂载」。卸载那一刻 `unsub` 还是 null，退订被跳过；再挂载 `refCount` 又回到 1 于是再订一次——第一条订阅永久泄漏，每个 realtime 事件被处理两遍。同步顺手让首屏 `getFullList` 的结果也过一遍 `byName`：服务端按 `name` 排、realtime 更新按 `name \|\| hostname` 排，两者不一致时任一更新到达都会让整张表重排 |
| X | 08 Task 5 Step 1 `stores/events.ts` | 同 W 的 `let unsub`；且切换机器时无防护 | 同 W 改存 Promise；另加自增票号 `ticket`，`getList` 与订阅回调都核对票号 | 除 W 的泄漏外，快速切换机器时先发出的 `getList` 可能后返回，把后一台机器的事件流覆盖掉 |
| Y | 08 Task 6 Step 1 `AddMachineDialog.tsx` | 签发 token 的 effect 无守卫；`knownIds` 在首次渲染即从 `$machines` 取基线 | 签发加 `issued` ref 守卫；基线改为等 `$machinesLoading` 落下再取；`navigator.clipboard` 加 try/catch | StrictMode 会把签发跑两遍，每开一次弹窗白签一枚 token 并多写一条 `token.issued`。基线更严重：弹窗可能在首屏 `getFullList` 还没回来时打开，那一刻列表是空的，基线取空集会让随后加载进来的每一台都算「新机器」，弹窗立刻自己关掉。clipboard API 在非安全上下文不可用，不兜底会抛未捕获异常 |
| Z | 08 Task 1 Step 3 `tokens.css` | `@theme inline` 块里没有 `--color-accent-soft` | 补一条 `--color-accent-soft: var(--accent-soft)` | Task 3 的 `Sidebar` 用了 `bg-accent-soft` 标高亮项，token 不在 `@theme` 里 Tailwind 就不生成这个 utility，当前项没有背景色 |

另记若干计划正文的笔误与执行期约定，不影响产物：

- 01 Task 5 Step 3 的验证命令写成 `go test ./agent/...`，但该步创建的是 atomicfile 的测试，实际应为 `go test ./internal/atomicfile/...`；`go mod init` 生成的 go 指令是当前工具链版本（`go 1.26.5`），需按 Global Constraints 手工改回 `go 1.26.0`。
- 08 的 Tech Stack 写明 Vite 7 + TS 5，但 `npm install` 默认装到 Vite 8 + TS 7。按计划钉回 7 / 5 时须同时钉 `@vitejs/plugin-react@^5`——v6 的 peer 要求 `vite@^8`，不钉会解析失败。
- `hub/internal/site/dist/index.html` 是**入库的占位文件**，而 `vite build` 每次都会覆盖它（`emptyOutDir` 连 `.gitkeep` 之类的点文件也一并删，换不成别的锚点）。因此 `make build` / `npm run build` 之后工作区必然「脏」一个 `dist/index.html`，提交前需 `git checkout -- hub/internal/site/dist/index.html` 还原，切勿把构建产物提交进去。

---

## 验收对照（spec §13）

| DoD # | 标准 | 由哪个子计划保证 |
|---|---|---|
| 1 | `make build` 产出两个二进制，各 < 30MB | 01（Makefile）、09（体积核对） |
| 2 | `docker compose up -d` 起 hub，可登录见空列表 | 09、08 |
| 3 | 3 台真机一行接入，单台 < 2 分钟 | 09（install.sh）、04（enroll） |
| 4 | `kill -9` → 5–10 秒转 offline，自动重启转回 online | 06、07、09（服务单元） |
| 5 | 断网 60 秒转 offline，恢复自动重连 | 06、07 |
| 6 | 重启 hub → 全部重连，无幽灵 online | 06 |
| 7 | 篡改 `hub.pub` → 拒绝连接、明确日志、不再重试 | 05、07 |
| 8 | UI 删除机器 → agent 收到原因并停止重试 | 06、07 |
| 9 | `go test -tags=testing ./...` 全绿 | 01–07 |
| 10 | hub < 64MB RAM，agent < 30MB RAM，空闲 CPU ≈ 0 | 09（实测记录） |
