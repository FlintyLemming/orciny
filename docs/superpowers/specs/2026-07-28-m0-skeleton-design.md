# Orciny M0 · 骨架 —— 工程设计

| 文档信息 | |
|---|---|
| 版本 | v1（待实现） |
| 日期 | 2026-07-28 |
| 层次 | 工程设计（模块边界、协议、数据模型、测试策略） |
| 上位文档 | [产品设计文档 v0.1](../../PRODUCT-DESIGN.md) —— 业务与产品层面的唯一依据 |
| 覆盖范围 | 里程碑 M0（产品文档 §10）。M1/M2/M3 各自另立 spec |
| 主要参照 | [beszel](https://github.com/henrygd/beszel) v0.18.7 —— 连接模型、PocketBase 用法、构建与分发形态 |

本文档只描述 M0。凡产品层面的定位、非目标、术语、路线图，一律以上位文档为准，此处不重复。

---

## 1. M0 的目标与边界

### 1.1 交付物

M0 结束时，作者本人拥有一个**能看见自己机队的面板**：hub 跑在服务器上，三台以上真机装了 agent，面板实时显示谁在线、谁掉线。配置同步、用量、订阅一概没有——那是 M1 之后的事。

具体到功能：

- hub 可部署（`docker compose up -d` 或单二进制直跑）
- Web UI 可登录，含布局壳与导航（未实现的页面显示占位）
- 「添加机器」签发一次性注册 token，给出一行安装命令
- `install.sh` 一行接入：探测平台 → 下载 agent → 装服务单元 → enroll
- agent 常驻（systemd / launchd），断线自动重连
- 机器列表实时显示在线状态；机器详情显示基本信息与事件流
- 设置页雏形

### 1.2 明确不做（推迟到 M1+）

配置集与版本、下发与落盘、漂移侦测、凭据库、机器变量、用量采集、订阅采集、告警与通知、agent 自更新、多用户与权限。

agent 在 M0 **完全不读写 `~/.claude`**。它只探测 Claude Code 的版本号（`claude --version`），除此之外不碰用户任何文件。

### 1.3 为什么先做这个

M0 的价值不在功能，在于**把所有后续工作依赖的地基一次立对**：模块边界、wire 协议的演进机制、信任根、连接生命周期。这四样任何一个在 M1 之后返工，代价都是全机队重新 enroll 或全协议改版。

---

## 2. 仓库结构与模块边界

### 2.1 布局

```
orciny/
├── orciny.go                    package orciny — Version / AppName / MinAgentVersion
├── go.mod                       单 module: github.com/FlintyLemming/orciny
├── Makefile
├── .goreleaser.yml
│
├── protocol/                    双方唯一的公共依赖
│   ├── envelope.go              Envelope / Kind 枚举
│   ├── messages.go              各 Kind 的 payload 结构体
│   └── codec.go                 CBOR 编解码与校验
│
├── hub/
│   ├── hub.go                   package hub — 唯一对外入口（NewHub / Start）
│   └── internal/
│       ├── ws/                  WS 升级、连接对象、读写循环
│       ├── handshake/           双向挑战-应答
│       ├── machines/            连接注册表与在线状态机
│       ├── enroll/              注册 token 签发与核销
│       ├── identity/            hub 密钥对的生成与加载
│       ├── routes/              自定义 HTTP 路由
│       ├── migrations/          PocketBase collections 定义
│       ├── events/              事件流写入
│       └── site/                前端源码 + embed.go
│
├── agent/
│   ├── agent.go                 package agent — 唯一对外入口
│   ├── cli.go                   cobra 子命令
│   └── internal/
│       ├── identity/            Ed25519 密钥对、指纹、hub 公钥钉扎
│       ├── conn/                WS 客户端与重连状态机
│       ├── enroll/              enroll 流程
│       └── probe/               主机信息与工具版本探测
│
├── cmd/
│   ├── orciny/main.go           → import ".../hub"
│   └── orciny-agent/main.go     → import ".../agent"
│
├── internal/testsupport/        //go:build testing —— 测试脚手架
│
└── supplemental/
    ├── scripts/install-agent.sh
    ├── docker/{Dockerfile,docker-compose.yml}
    └── units/{orciny-agent.service,moe.flinty.orciny-agent.plist}
```

### 2.2 边界的强制方式

**这是本节唯一重要的设计决定。** beszel 把 agent 放顶层、hub 放 `internal/`，靠约定隔离——但同一 Go module 内 `agent/` 完全可以 import `internal/hub/`，编译器不拦。

Orciny 改用 Go 的 internal 规则做**编译期强制**：`hub/internal/X` 只有 `hub/` 树下的代码能导入，`agent/` 想碰直接编译失败。`cmd/orciny/main.go` 不在 `hub/` 树下，但它只需要 `hub` 这一个入口包，`hub` 再去 import 自己的 internal 子包——链路成立。

三条约束：

1. `agent/**` 与 `hub/**` 之间**不允许任何直接依赖**，编译器保证。
2. 双方共享的东西只能放 `protocol/`。这个包必须保持极瘦——只有数据结构和编解码，不含任何业务逻辑、不 import hub 或 agent 的任何包、不 import PocketBase。
3. 顶层 `orciny.go` 只放常量。

这条边界在 M2 会承受最大压力（用量采集、订阅采集都会诱使人"复用一下 hub 那个 helper"）。趁现在没代码，用目录结构钉死，成本为零。

**一个直接后果**：`internal/testsupport` 也在 `hub/` 树之外，因此它只能通过 `hub` 和 `agent` 的公开入口包做集成测试，够不到 `hub/internal/*`。这些包的单元测试必须写在包内部（`hub/internal/ws/ws_test.go` 这种）。这是正确的分工——跨组件的时序行为走集成测试，包内逻辑走同包单元测试——但要在写第一个测试之前知道，否则会先写出一个够不到目标的脚手架。

**模块路径**为 `github.com/FlintyLemming/orciny`，与仓库地址逐字符一致。Go 的模块路径**大小写敏感**（模块代理把大写字母编码成 `!x` 形式），路径与仓库名不符会导致 `go get` 失败，而路径一旦被引用就极难更改。仓库名同步取小写，与二进制名 `orciny`、agent 目录 `~/.orciny/`、Docker 镜像名保持一致。

### 2.3 依赖选型

| 用途 | 选择 | 理由 |
|---|---|---|
| hub 框架 | `pocketbase/pocketbase` | 产品文档决策 B1；版本锁定，薄封装（§14 R4） |
| WebSocket | `lxzan/gws` | beszel 同款；内置 deadline 与 ping/pong 管理，比 gorilla 省一层自研 |
| wire 编码 | `fxamacker/cbor/v2` | 见 §3 |
| CLI | `spf13/cobra` | agent 子命令；hub 侧 PocketBase 已自带 |
| 版本比较 | `blang/semver` | 协议兼容门槛 |
| 签名 | 标准库 `crypto/ed25519` | 无外部依赖 |
| 测试断言 | `stretchr/testify` | — |

前端：React 19 + Vite + TypeScript + PocketBase JS SDK + nanostores（状态）+ Tailwind + shadcn/radix（组件）+ Lingui（i18n，对应产品文档的中英双语内置）。

---

## 3. 协议层（`protocol/`）

### 3.1 编码

CBOR，字段用 `keyasint` 整数标签：

```go
type Envelope struct {
    Kind Kind            `cbor:"0,keyasint"`
    ID   *uint32         `cbor:"1,keyasint,omitempty"`          // 请求-响应配对；通知类消息为 nil
    Data cbor.RawMessage `cbor:"2,keyasint,omitempty,omitzero"` // 延迟解码
}
```

选 CBOR 而非 JSON 的理由：字段按整数编号传输，重命名不破坏兼容；二进制体积小（M1 起要传配置快照）；`cbor.RawMessage` 支持按 `Kind` 分派后再解码 payload，不需要先解成 `map[string]any`。

**信封统一为一种结构**。beszel 分了 `HubRequest[T]` 和 `AgentResponse` 两个结构体，且 `AgentResponse` 里塞了七八个互斥的可选字段（`SystemData` / `SmartData` / `ServiceInfo`…），每加一种响应就要改这个结构体。Orciny 用单一 `Envelope` + `Kind` 分派，payload 类型与 Kind 一一对应，加消息不动信封。

### 3.2 Kind 枚举

```go
type Kind uint8

const (
    KindHello       Kind = 1  // agent → hub
    KindChallenge   Kind = 2  // hub   → agent
    KindAuth        Kind = 3  // agent → hub
    KindAuthResult  Kind = 4  // hub   → agent
    KindMachineInfo Kind = 5  // agent → hub
    // 10-19 预留给 M1 配置下发（ConfigNotify / ConfigPull / ApplyAck / DriftReport / DriftRestore）
    // 20-29 预留给 M2 数据面（UsageBatch / CollectorReport）
    // 30-39 预留给 agent 生命周期（AgentUpdate）
)
```

**枚举值只增不改不复用**。删掉一个消息类型时，注释掉并保留编号。分段预留是为了让 M1/M2 加消息时编号读起来仍有结构，代价为零。

未知 `Kind` 的处理：**记 warn 日志后丢弃，不断开连接**。这样新版 hub 给旧版 agent 发新消息时，agent 只是忽略而不是崩溃或重连风暴。

### 3.3 M0 消息定义

```go
type Hello struct {
    AgentVersion string `cbor:"0,keyasint"`
    Fingerprint  string `cbor:"1,keyasint"`
    ClientNonce  []byte `cbor:"2,keyasint"` // 32 字节
}

type Challenge struct {
    ServerNonce []byte `cbor:"0,keyasint"` // 32 字节
    HubSig      []byte `cbor:"1,keyasint"` // Ed25519 签名
}

type Auth struct {
    AgentSig []byte `cbor:"0,keyasint"`
}

type AuthResult struct {
    OK     bool   `cbor:"0,keyasint"`
    Reason string `cbor:"1,keyasint,omitempty"` // 失败原因，供 agent 决定重试策略
    Code   uint8  `cbor:"2,keyasint,omitempty"` // 见 §4.6
}

type MachineInfo struct {
    Hostname     string            `cbor:"0,keyasint"`
    OS           string            `cbor:"1,keyasint"` // linux / darwin
    Arch         string            `cbor:"2,keyasint"` // amd64 / arm64
    AgentVersion string            `cbor:"3,keyasint"`
    ToolVersions map[string]string `cbor:"4,keyasint,omitempty"` // {"claude-code": "2.1.3"}
}
```

心跳**不走信封**，直接用 WebSocket 原生 ping/pong 帧——省一次序列化，且 `gws` 已内置帧级 deadline 管理。

`AuthResult` 有一个额外用途：**握手完成后 hub 仍可发送 `AuthResult{OK:false}` 作为"授权已撤销"通知**，随后关闭连接。M0 唯一的触发场景是机器在 UI 中被删除（§6.4c）。agent 在任何时候收到 `OK:false` 都按 `Code` 走 §7.2 的分流，不区分是握手期还是连接期——这样 agent 侧只有一条处理路径。

### 3.4 版本兼容机制

根包 `orciny.go`：

```go
const (
    Version  = "0.1.0"
    AppName  = "orciny"
)

// 低于此版本的 agent 一律拒绝连接。
var MinAgentVersion = semver.MustParse("0.1.0")
```

握手第一步读 `Hello.AgentVersion`，低于门槛直接以 `AuthResult{OK:false, Code:CodeVersionTooOld}` 拒绝，UI 在机器列表标注"agent 版本过旧"。

M0 只有"拒绝"这一条分支。将来真需要兼容旧 agent 时，按 beszel 的做法在根包加 `MinVersionXxx` 门槛常量、在结构体里保留旧字段并注释 `// Legacy (<= x.y)`。**M0 不为此预先抽象**——没有真实的旧版本，抽象必然抽错。

---

## 4. 认证与信任根

### 4.1 为什么是双向

beszel 的模型是单向的另一半：agent 用可窃取的 `X-Token` 表明身份、指纹由硬件派生并 TOFU 钉扎；而 SSH 密钥用于 **agent 验证 hub**，防的是假 hub 钓鱼。

Orciny 两侧都要：

- **agent 侧身份**用 Ed25519 挑战-应答，强于 beszel 的 token——私钥永不上网，没有可窃取的长期凭证。
- **hub 侧身份**必须验证，而且比 beszel 更要紧：beszel 的假 hub 最多骗走 CPU 温度，Orciny 的 agent 从 M1 起会**接收文件并写进 `~/.claude`**。能冒充 hub 的攻击者可以往所有机器的 `settings.json` 里塞 hook、往 `skills/` 里塞脚本——等价于全机队远程代码执行。

M0 虽然还不下发配置，但**信任根在 enroll 那一刻建立**。推到 M1 再补，意味着全机队重新 enroll。

### 4.2 握手流程

```
1. agent → hub   Hello{ agentVersion, fingerprint, clientNonce }

2. hub 校验 agentVersion ≥ MinAgentVersion
   hub 按 fingerprint 查 machines 表 —— 查不到则以 CodeUnknownFingerprint 拒绝
   hub → agent   Challenge{ serverNonce,
                            hubSig = Sign(hubPriv, "orciny-hub-v1" ‖ clientNonce ‖ serverNonce) }

3. agent 用钉扎的 hub 公钥验 hubSig
   不匹配 → 立即断开、写 fatal 日志、进入 Compromised 状态不再重试该地址

4. agent → hub   Auth{ agentSig = Sign(agentPriv, "orciny-agent-v1" ‖ serverNonce ‖ clientNonce) }

5. hub 用该 fingerprint 记录中的公钥验 agentSig
   通过 → 登记进连接注册表 → AuthResult{OK:true}
   失败 → AuthResult{OK:false, Code:CodeBadSignature} → 记 auth.failed 事件 → 断开

6. agent → hub   MachineInfo{...}   （握手后首条业务消息，hub 据此更新记录）
```

设计要点：

- **两个 nonce 都参与两边的签名**。只用一个 nonce 的话，其中一方的挑战就是可预测的，能被重放。
- **两条域分隔前缀**（`orciny-hub-v1` / `orciny-agent-v1`）保证一方的签名不能被拿去冒充另一方。这是签名协议的标准防御，成本为零。
- nonce 由 `crypto/rand` 生成，32 字节，一次性使用，不入库。
- 握手有超时：hub 侧从 WS 升级成功到收到合法 `Auth` 限 10 秒，超时断开。防止半开连接堆积。

### 4.3 指纹

```
fingerprint = base64url(SHA256(agentPubKey))[:32]
```

**指纹是公钥的函数，不是硬件的函数。** beszel 用 `host.HostID()`（失败回退 `hostname + CPU型号` 的 SHA256），是因为它没有 agent 密钥、只能靠硬件特征做 TOFU。

我们既然有密钥对，指纹就该由公钥派生，理由有二：

1. 消除"指纹对但公钥错"这种需要额外处理的中间状态——两者天然一致。
2. 硬件 ID 在 VPS 和容器里重复率不低。beszel 源码里硬编码了一个 `knownBadUUID`（`03000200-0400-...`）专门绕过某个烂大街的 `product_uuid`，就是被这个坑过。

代价：重装系统或删掉 `~/.orciny/identity/` 后会生成新密钥对、变成一台新机器。这是**正确的行为**——密钥丢了本来就该重新建立信任。UI 上表现为需要重新 enroll，`install.sh` 会引导。

### 4.4 enroll 流程

```
Web UI「添加机器」
  → POST /api/orciny/enroll-tokens          (superuser 认证)
     hub 生成 32 字节随机 token
     只存 SHA256 哈希（明文仅在响应里返回一次）
     TTL 15 分钟、单次使用
  → UI 展示并可拷贝：
     curl -fsSL https://<hub>/install.sh | sh -s -- --hub <url> --token <t> [--hub-key <fp>]
     （附 15 分钟倒计时）

install.sh: 探测 OS/arch → 下载二进制 → 校验 checksum → 装服务单元 → 调 enroll

  → agent 生成 Ed25519 密钥对，私钥落 ~/.orciny/identity/agent.key (0600)
  → POST /api/orciny/enroll  { token, pubKey, hostname, os, arch, agentVersion }
  → hub 在单个事务内：校验 token 未过期未核销 → 核销 → 建/更新 machine 记录
                    → 响应 { hubPubKey, machineId }
  → agent 写 ~/.orciny/identity/hub.pub（TOFU 钉扎）
     若命令行给了 --hub-key，先比对指纹，不符则拒绝安装
  → agent 写 ~/.orciny/agent.yml → 启动服务 → WS 连接 → 走 §4.2 握手
```

**信任根落在那条 curl 命令上**：走 HTTPS、token 一次性、15 分钟有效。攻击者要冒充 hub 必须恰好在 enroll 那一刻做中间人且持有有效证书；钉扎完成后即免疫。

`--hub-key <fingerprint>` 给谨慎用户做带外校验（hub 的公钥指纹显示在设置页），实现成本只有几行，M0 就加上。

**重复 enroll 同一台机器**（指纹已存在）：更新该记录的公钥与主机信息并复用，记 `machine.re-enrolled` 事件。比拒绝友好——重装 agent 是常见操作。注意这条路径必须仍然要求有效的注册 token，否则任何人都能用伪造的公钥覆盖已有机器。

**enroll 响应丢失的重放**：agent 生成了密钥对、hub 也建好了记录，但响应在网络上丢了——agent 手里有私钥却没有 hub 公钥，无法握手，而 token 已被核销，重试会失败。这台机器就卡死了，只能由管理员手工删除记录并重发 token。

因此 enroll 接口必须是**幂等**的：token 已核销时，若请求携带的 `pubKey` 与该 token 核销时创建的机器记录中的公钥**完全一致**，视为同一次 enroll 的重放，正常返回 hub 公钥；不一致才拒绝。判据是公钥而非 token，所以窃取到已用 token 的攻击者无法借此注册自己的密钥。

`install.sh` 相应地在 enroll 失败时重试两次（间隔 2 秒），把瞬时网络问题挡在用户视线之外。

### 4.5 hub 密钥

Ed25519 密钥对，PEM 格式存 `pb_data/orciny_hub_key.pem`（0600），启动时不存在则生成。

它属于 PocketBase 的数据目录，因此**自动被 PocketBase 的备份机制覆盖**（产品文档 §3.4）。这一点必须写进运维文档：恢复备份时若丢了这个文件，所有 agent 都会因 hub 签名验证失败而拒绝连接，需要全部重新 enroll。

### 4.6 拒绝原因码

```go
const (
    CodeUnknownFingerprint uint8 = 1  // agent: 提示重新 enroll，5 分钟间隔重试
    CodeBadSignature       uint8 = 2  // agent: 视为异常，5 分钟间隔重试
    CodeVersionTooOld      uint8 = 3  // agent: 5 分钟间隔重试（等 hub 升级或 agent 更新）
    CodeMachineRemoved     uint8 = 4  // agent: 停止重试，需人工重新 enroll
)
```

原因码驱动 agent 的重试策略（§7.2）。给出明确原因而不是笼统的 401，是因为这三种情况的正确处置完全不同，而排查者手上往往只有 agent 的日志。

---

## 5. Hub 装配

### 5.1 与 PocketBase 的耦合方式

采用 beszel 验证过的模式：`Hub` 嵌入 `core.App`，子系统作字段，业务全部挂在 `OnServe` 上。

```go
type Hub struct {
    core.App
    machines *machines.Manager
    identity *identity.Store
    events   *events.Writer
}

func (h *Hub) Start() error {
    h.App.OnServe().BindFunc(func(e *core.ServeEvent) error {
        if err := h.identity.Load(); err != nil { return err }      // 加载/生成 hub 密钥
        if err := h.machines.ResetGhosts(); err != nil { return err } // 见 §6.4
        if err := routes.Register(e, h); err != nil { return err }
        h.machines.Start()
        return e.Next()
    })
    // record hook 只用于状态机联动，不放业务规则
    h.App.OnRecordAfterDeleteSuccess("machines").BindFunc(h.machines.OnRecordDeleted)
    return h.App.(*pocketbase.PocketBase).Start()
}
```

三条纪律（对应产品文档 §14 R4 "薄封装其 API，业务逻辑不渗入框架层"）：

1. **业务逻辑放自己的包**，`hub/internal/*` 各司其职，只在边界处调 PocketBase 的 API。
2. **record hook 只用于状态机联动**——例如删除机器时踢掉活跃连接。任何"计算"和"决策"都不写在 hook 里，因为 hook 难以单独测试。
3. **PocketBase 版本锁定**，升级作为独立任务处理。

### 5.2 数据访问的分工

| 路径 | 方式 | 理由 |
|---|---|---|
| 前端读机器列表 / 事件流 | PocketBase JS SDK + realtime 订阅 | 实时上下线零后端代码——这是选 PocketBase 的核心收益 |
| 前端触发动作 | 自定义路由 `/api/orciny/*` | 业务规则和校验集中在 Go 侧 |
| 前端删除机器 | PB SDK record delete + hook 联动 | 删除语义简单，hook 负责踢连接 |
| agent 通信 | 自定义路由 + WS | — |

前端因此绑定了 collection 结构和 PB SDK。这是有意识的取舍：换来的是机器上下线在面板上秒级跳动而无需自研推送通道。schema 改名会打到前端——用 TypeScript 类型定义集中声明 collection 结构，改动至少能被类型检查抓到。

### 5.3 认证模型

**M0 使用 PocketBase superuser 作为唯一身份**，不建 `users` collection。

- 前端用 `pb.collection('_superusers').authWithPassword()` 登录。
- 所有 collection 的 API rule 设为 `nil`（仅 superuser 可访问，PocketBase 中 superuser 绕过 rule）。
- 自定义路由用 `apis.RequireSuperuserAuth()` 中间件。

已知取舍：前端持有的 token 等价于完整数据库权限。产品文档 §1.5 明确"单管理员起步"，M0 场景下可接受，省掉一整套用户与角色代码。v2 做微团队时引入 `users` collection 并重写 API rule——届时前端的登录逻辑要改，但数据模型不受影响。

### 5.4 开发模式

前端 embed 在生产是对的，在开发是灾难——改一行 CSS 要重编译 Go。

按 beszel 的做法用 build tag 分离：

- `//go:build dev`：hub 把非 `/api/*` 请求反向代理到 `localhost:5173`（Vite dev server），支持 HMR。
- 默认（生产）：serve `hub/internal/site` 中 `//go:embed all:dist` 的静态文件。

`make dev` 同时起 Vite、hub（dev tag）和一个本地 agent。

---

## 6. 连接生命周期与机器状态

### 6.1 状态定义

`machines.status` 三态：`online` / `offline` / `paused`。

M0 只有前两态在跑。`paused` 的字段在 schema 中就位（M1 起表示"保持连接但不应用下发"），但 **M0 不提供切换入口**——没有下发行为时这个开关没有语义，做了就是死代码。

### 6.2 心跳与超时

| 参数 | 值 | 说明 |
|---|---|---|
| hub 主动 ping 间隔 | 30s | 对齐产品文档 §3.2 |
| WS read deadline（双侧） | 70s | 容忍两次连续丢失 |
| 握手超时 | 10s | 从 WS 升级到收到合法 Auth |
| 离线判定宽限 | 5s | 见 §6.3 |

任何收到的消息或 pong 帧都重置 deadline。agent 侧同样设 70s deadline——用于识别"hub 已消失但 TCP 未断"（NAT 超时、中间设备静默丢弃），到期即主动断开重连。

### 6.3 离线判定的 5 秒宽限

**直接在 WS 关闭时置 offline 会让面板在网络抖动时不停闪红。** beszel 在 `OnClose` 后 sleep 5 秒再置 down，这个技巧照搬，但实现方式换掉——beszel 用 `weak.Pointer` + goroutine sleep，绕得比较费解。

Orciny 的实现放在 `machines.Manager` 里：

```
连接关闭
  → 从注册表移除该连接（前提：注册表中该 fingerprint 的当前连接确实是它）
  → 启动 5s 定时器
  → 定时器到期：若注册表中该 fingerprint 仍无连接 → 置 offline + 写 last_seen + 记事件
                若已有新连接              → 什么都不做

新连接注册
  → 取消该 fingerprint 的待定定时器（若有）
  → 置 online
```

### 6.4 三个必须处理的边界情况

**（a）同一 fingerprint 重复连接。** agent 重启但 hub 还没检测到旧连接断开时会发生。策略：**新连接胜出**，旧连接以 `close(1000, "replaced")` 关闭。

关键是旧连接的关闭**不能触发离线逻辑**——它的 `OnClose` 会照常触发，而此时注册表里已经是新连接了。§6.3 中"前提：注册表中该 fingerprint 的当前连接确实是它"这个判断就是为此。这是最容易写错、也最容易在测试中漏掉的一处，必须有专门用例。

**（b）hub 重启后的幽灵 online。** 连接注册表在内存里，进程重启后为空，但 DB 里的 `status='online'` 还在。`OnServe` 阶段执行 `UPDATE machines SET status='offline' WHERE status='online'`，随后 agent 陆续重连转回 online。

**（c）机器被删除时的活跃连接。** `OnRecordAfterDeleteSuccess("machines")` hook 中先发 `AuthResult{OK:false, Code:CodeMachineRemoved}`（§3.3 的撤销通知用法）再关闭连接，agent 收到后停止重试（§7.2）。

### 6.5 last_seen 的写入频率

**不在每次 pong 时写 DB。** 50 台机器 × 30 秒 = 每秒 1.7 次写入，SQLite 扛得住但纯属浪费。

策略：内存中维护 lastSeen，仅在**状态变化时**持久化（上线时、判定离线时）。

UI 相应地调整语义：`online` 时显示"在线"而不显示秒级心跳时间；`offline` 时 `last_seen` 正是断开那一刻的值，准确。在线状态本身由 realtime 推送，不依赖 `last_seen` 的新鲜度。

---

## 7. Agent 设计

### 7.1 本地布局

```
~/.orciny/
├── agent.yml            hub 地址、machineId（安装时生成，一般不手改）
├── identity/
│   ├── agent.key        Ed25519 私钥 (0600)
│   └── hub.pub          钉扎的 hub 公钥 (0600)
└── logs/                JSON lines，保留 14 天
```

`state.json`（产品文档 §5.2）在 M0 无内容，M1 引入。

所有落盘走**临时文件 + rename 原子替换**，避免写到一半崩溃留下半个文件。

### 7.2 重连状态机

```
Disconnected ──connect──> Handshaking ──ok──> Connected
     ↑                         │                  │
     └──────── backoff ────────┴──── 断开 ────────┘
     
Compromised（终态，不再重试）：hub 签名验证失败
```

退避策略：初始 1 秒，每次 ×2，上限 60 秒，**叠加 ±20% 抖动**。抖动不是可选项——机队规模上去后，hub 重启会让所有 agent 在同一秒重连，抖动把这个尖峰摊平。连接成功后退避重置。

按 `AuthResult.Code` 分流：

| 情况 | 策略 |
|---|---|
| 网络错误、连接被拒 | 指数退避 |
| `CodeUnknownFingerprint` / `CodeBadSignature` / `CodeVersionTooOld` | 固定 5 分钟间隔重试，每次都记日志 |
| `CodeMachineRemoved` | 停止重试，日志提示需重新 enroll |
| **hub 签名验证失败** | 进入 `Compromised`，**永不重试**，写 fatal 日志 |

最后一条要特别强调：hub 签名不匹配意味着要么在遭受中间人攻击，要么 hub 换了密钥（恢复备份时丢了 `orciny_hub_key.pem`）。两种情况都需要人来判断，agent 自作主张继续重试只会掩盖问题。

### 7.3 CLI

M0 实现产品文档 §5.3 中的一个子集：

| 命令 | 作用 |
|---|---|
| `orciny-agent enroll --hub <url> --token <t> [--hub-key <fp>]` | 注册接入 |
| `orciny-agent status` | 连接状态、hub 地址、指纹、agent 版本 |
| `orciny-agent version` | 版本号 |
| `orciny-agent run` | 前台运行（服务单元调用的就是它） |

`sync` / `drift` / `pause` / `resume` / `update` 属于后续里程碑。

### 7.4 探测

- hostname、GOOS、GOARCH：标准库。
- Claude Code 版本：执行 `claude --version` 并解析，超时 3 秒，失败则留空不报错（机器上没装 Claude Code 是合法状态——产品文档 §4.2 允许"仅观测"的机器接入）。

**M0 的 agent 除此之外不读取用户的任何文件。** 尤其不碰 `~/.claude`。

### 7.5 日志

JSON lines 输出到 stdout，由 systemd / launchd 收集——这是默认且唯一开启的通道。`agent.yml` 中可另行开启文件日志写入 `~/.orciny/logs/`，按天切分、保留 14 天、由 agent 自行轮转（产品文档 §5.2）。

注册 token 在日志中只记前 8 位；私钥、hub 公钥内容一律不记。这条在 M0 就要立规矩——M1 引入凭据下发后，脱敏漏一处就是事故。

---

## 8. 数据模型

M0 只需三个 collection。

**`machines`**

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | text | PocketBase 主键 |
| `name` | text | 备注名，默认取 hostname，可改 |
| `fingerprint` | text, **unique** | 公钥 SHA256 派生 |
| `pub_key` | text | agent Ed25519 公钥（base64） |
| `hostname` / `os` / `arch` | text | 探测所得 |
| `agent_version` | text | — |
| `tool_versions` | json | `{"claude-code": "2.1.3"}` |
| `status` | select | `online` / `offline` / `paused` |
| `last_seen` | date | 仅状态变化时写（§6.5） |
| `created` / `updated` | autodate | — |

**`enroll_tokens`**

| 字段 | 类型 | 说明 |
|---|---|---|
| `token_hash` | text, **unique** | SHA256(token)，明文不入库 |
| `expires_at` | date | 签发 + 15 分钟 |
| `used_at` | date | 非空即已核销 |
| `machine` | relation → machines | 核销后回填 |

核销走**单事务 + 唯一约束**，保证同一 token 并发使用只有一个成功。过期 token 的清理：hub 每小时一次 cron 删除 `expires_at < now - 24h` 的记录。

**`events`**

| 字段 | 类型 | 说明 |
|---|---|---|
| `kind` | text | `machine.enrolled` / `machine.re-enrolled` / `machine.connected` / `machine.disconnected` / `machine.removed` / `token.issued` / `auth.failed` |
| `machine` | relation → machines, 可空 | — |
| `detail` | json | 结构随 kind 而异 |
| `created` | autodate | — |

`events` 是产品文档 §6 的"轻量事件流，非合规审计"。M0 不做保留期清理——事件产生速率很低。M1 加设置项。

**迁移形式**：单个 Go 迁移文件 `hub/internal/migrations/001_initial.go`，用 PocketBase 的 collection API 声明式创建。API rule 全部设 `nil`（§5.3）。

配置集、版本、指派、漂移、凭据、用量、采集器等实体是 M1/M2 的事，此处不预建——PocketBase 的迁移是增量的，届时新增迁移文件即可。

---

## 9. HTTP API 面

### 9.1 自定义路由

| 方法 | 路径 | 认证 | 说明 |
|---|---|---|---|
| POST | `/api/orciny/enroll-tokens` | superuser | 签发一次性 token，响应含明文 token 与过期时间 |
| POST | `/api/orciny/enroll` | token 本身 | agent 注册，响应含 hub 公钥与 machineId |
| GET | `/api/orciny/ws` | 握手（§4.2） | agent WebSocket 端点 |
| GET | `/install.sh` | 无 | 托管安装脚本，模板注入 hub URL |
| GET | `/api/orciny/hub-info` | superuser | hub 版本、公钥指纹（设置页展示，供带外校验） |

`/install.sh` 不需要认证：脚本本身不含秘密，token 由用户拼在命令行上。

### 9.2 PocketBase 原生接口

前端通过 JS SDK 直接使用 `machines` 与 `events` 的 list / view / update（改备注名）/ delete，以及两者的 realtime 订阅。

---

## 10. 前端

### 10.1 M0 范围

`mock/ui-mock.html` 已经定义了完整的设计语言（暖黑/暖纸双主题、9 个页面的布局）。M0 把设计语言落成真实组件体系，但只实现三个屏：

| 屏 | 内容 |
|---|---|
| 登录 | superuser 邮箱 + 密码 |
| 布局壳 | 侧栏 8 项导航（总览/机器/配置集/收件箱/用量/订阅/凭据/设置），未实现页显示"该功能将在 Mx 提供"占位；主题切换；语言切换 |
| 机器列表 | 表格：名称、状态、OS/arch、agent 版本、Claude Code 版本、最后心跳；realtime 订阅自动刷新；行内改备注名、删除 |
| 机器详情 | 基本信息卡 + 本机事件流；「配置对齐状态」「漂移」「机器变量」三块显示为占位区 |
| 添加机器 | 弹窗：调 `/enroll-tokens` → 展示可拷贝的一行安装命令 + 15 分钟倒计时；新机器上线时 realtime 自动关闭弹窗并高亮新行 |
| 设置 | 站点信息、hub 版本与公钥指纹、备份入口（跳 PocketBase admin）；agent 更新策略等显示为占位 |

"添加机器弹窗在新机器上线时自动响应"是 realtime 值得做的一处小体验——用户不需要猜什么时候刷新页面。

### 10.2 结构约定

- collection 的 TypeScript 类型集中在 `types/collections.ts`，是前端与 schema 的唯一契约点。
- realtime 订阅统一收在 `stores/` 下的 nanostores，组件不直接调 SDK。
- 未实现页面用统一的 `<Placeholder milestone="M1" />` 组件，避免各页面自己编空状态。

### 10.3 明示取舍

**M0 不写前端自动化测试。** 三个屏、逻辑集中在数据订阅上，写测试的成本远高于收益，靠真机验收覆盖。M1 引入配置编辑器（有真实的校验逻辑和状态机）时再建前端测试体系。

---

## 11. 打包与部署

### 11.1 构建

`Makefile` 目标：`build-web` / `build-hub` / `build-agent` / `build` / `test` / `lint` / `dev`。

`.goreleaser.yml` 产出：
- hub：`linux/amd64`、`linux/arm64`
- agent：`linux/amd64`、`linux/arm64`、`darwin/amd64`、`darwin/arm64`
- `CGO_ENABLED=0`，附 `checksums.txt`

体积目标（产品文档 §9）：单二进制各 < 30MB，docker 镜像 < 40MB。

### 11.2 hub 容器

多阶段：Node 构建前端 → Go 编译（embed dist）→ alpine 运行时。暴露 8090，数据卷 `/pb_data`。`supplemental/docker/docker-compose.yml` 给出含反向代理说明的示例。

### 11.3 agent 服务单元 —— 一处与 beszel 的关键差异

**beszel 的 agent 跑在专用系统用户下；Orciny 的 agent 不行。**

从 M1 起 agent 要读写 `~/.claude`，必须**以目标用户的身份运行**，否则文件属主全错。这个约束在 M0 就要在服务单元里定死，否则 M1 会推倒重来。

**Linux**：system unit + `User=` / `Group=` / `Environment=HOME=/home/<user>`。选 system unit 而非 user unit，是因为 user unit 需要 `loginctl enable-linger` 才能在用户未登录时运行，多一个容易漏掉的步骤。

**macOS**：LaunchAgent（`~/Library/LaunchAgents/`）+ `KeepAlive`，随用户登录启动，天然以该用户身份运行。

**已知限制**：headless 的 Mac mini 在无人登录时 LaunchAgent 不运行。M0 不解决，在文档中说明并给出两条出路（开启自动登录，或用带 `UserName` 键的 LaunchDaemon 变体）。这是 macOS 的固有特性，不值得在 M0 花代价绕。

### 11.4 install.sh

参照 beszel 的 `install-agent.sh` 但砍掉长尾（Alpine/OpenWrt/FreeBSD/SELinux 等 M3 再说）。M0 骨架：

1. 解析 `--hub` / `--token` / `--hub-key` / `--version` / `--download-base`
2. `uname` 探测 os/arch
3. 下载对应二进制 + 校验 sha256
4. 安装到 `/usr/local/bin/orciny-agent`
5. 写服务单元（按平台）
6. 调 `orciny-agent enroll`
7. 启动服务并验证进程存活

`--download-base` 用于开发期指向本地构建产物，避免每次都要发 release 才能测试安装路径。

---

## 12. 测试策略

### 12.1 脚手架

`internal/testsupport`，`//go:build testing`：

```go
func NewTestHub(t *testing.T) *TestHub   // 包 PocketBase 的 tests.TestApp，自带临时数据目录与迁移
func NewTestAgent(t *testing.T, hubURL string) *TestAgent
```

PocketBase 官方的 `tests.TestApp` 提供临时数据目录克隆与自动迁移，是"内存内 hub + agent 集成测"这条路成立的前提。`make test` = `go test -tags=testing ./...`。

参照量级：beszel 的 `agent_connect_test.go` 是 1813 行，被测的 `agent_connect.go` 只有 334 行。连接层就该这么测——它的失败模式全在时序和边界上，靠手工验证覆盖不到。

### 12.2 用例清单

**协议层**
- 各 Kind 的 Envelope 编解码往返
- 未知 Kind 被忽略且不断开连接
- `Data` 为 nil 时不产生该字段

**enroll**
- 正常路径：建记录、核销 token、返回 hub 公钥
- 过期 token 被拒
- 已核销 token 被拒
- 同一 token 并发两次请求，恰好一个成功
- 指纹已存在时更新而非重复创建（re-enroll），且仍要求有效 token
- 幂等重放：用已核销的 token + 相同 pubKey 重试 → 成功并返回 hub 公钥
- 幂等重放的边界：用已核销的 token + **不同** pubKey → 拒绝

**握手**
- 完整正确路径
- hub 签名错误 → agent 断开并进入 `Compromised`，不再重试
- agent 签名错误 → hub 拒绝、记 `auth.failed` 事件
- 未知 fingerprint → `CodeUnknownFingerprint`
- agent 版本低于门槛 → `CodeVersionTooOld`
- 重放：截获一次完整握手的 `Auth` 消息，重连时重放 → 必须失败（serverNonce 每次新生成）
- 握手超时：升级后 10 秒不发 `Auth` → 连接被关闭

**连接生命周期**
- 断开后 3 秒内重连 → 状态始终 online，不产生 disconnected 事件
- 断开后 6 秒无重连 → offline
- **同 fingerprint 二次连接 → 旧连接被踢，状态保持 online，不产生 disconnected 事件**（§6.4a，最易写错处）
- hub 重启 → 幽灵 online 被清理
- 删除机器 → 活跃连接被踢且收到 `CodeMachineRemoved`
- 三个 agent 并发接入同一 TestHub → 各自独立，互不干扰

**agent 重连**
- 指数退避序列符合预期（注入假时钟）
- 抖动落在 ±20% 内
- 连接成功后退避重置
- 各 `AuthResult.Code` 对应正确的重试策略

### 12.3 时钟

所有涉及超时与退避的代码通过注入的 `Clock` 接口取时间，测试用假时钟。**不允许在测试里 `time.Sleep`**——5 秒宽限、70 秒 deadline、60 秒退避上限，真睡的话测试套件会跑到几分钟。这条规矩在 M0 立好，M1 的漂移轮询、M2 的采集器定时都直接受益。

---

## 13. 验收标准（M0 Definition of Done）

对照产品文档 §10 的 M0 验收（"≥3 台真机接入，杀 agent/断网后状态与重连表现正确"），细化为：

| # | 标准 |
|---|---|
| 1 | `make build` 产出两个二进制，各 < 30MB |
| 2 | `docker compose up -d` 起 hub，浏览器可登录并看到空机器列表 |
| 3 | 3 台真机（≥1 Linux + ≥1 macOS）各自通过一行 `install.sh` 接入，单台耗时 < 2 分钟 |
| 4 | `kill -9` agent → 面板 5–10 秒内转 offline；服务自动重启 → 转回 online |
| 5 | 断网 60 秒 → 转 offline；恢复 → 自动重连转 online，无需人工干预 |
| 6 | 重启 hub → 三台机器全部自动重连，无残留幽灵 online |
| 7 | 篡改某台 agent 的 `hub.pub` → 该 agent 拒绝连接、日志明确报出、不再重试 |
| 8 | 在 UI 删除一台机器 → 该 agent 收到明确原因并停止重试 |
| 9 | `go test -tags=testing ./...` 全绿 |
| 10 | hub 常驻 < 64MB RAM，agent 常驻 < 30MB RAM，agent 空闲 CPU ≈ 0 |

---

## 14. 实现切分

供 writing-plans 展开为实现计划的建议顺序。每一步都应可独立验证。

1. **骨架**：go.mod、根包常量、`cmd/` 两个 main、Makefile、hub 能起 PocketBase、初始迁移建三个 collection、空前端 embed
2. **protocol 包**：Envelope、Kind、M0 全部消息、编解码 + 单元测试
3. **身份**：hub 密钥生成/加载、agent 密钥生成、指纹派生 + 测试
4. **enroll**：token 签发与核销路由、agent `enroll` 子命令、TOFU 钉扎 + 全部 enroll 用例
5. **WS 握手**：升级、双向挑战-应答、原因码 + 全部握手用例
6. **连接注册表与状态机**：注册/踢除、5 秒宽限、幽灵清理、删除联动 + 全部生命周期用例
7. **agent 重连状态机**：退避与抖动、按原因码分流、`run`/`status`/`version` 子命令 + 假时钟测试
8. **前端**：登录、布局壳与主题、机器列表（realtime）、机器详情、添加机器弹窗、设置页雏形
9. **打包分发**：Dockerfile、compose 示例、goreleaser、install.sh、systemd/launchd 单元、dev 模式代理

1–7 步全部有自动化测试覆盖；8–9 步靠 §13 的真机验收。

---

## 15. 相对 beszel 的偏离记录

beszel 是本设计的主要参照，以下是有意识的偏离及理由。将来若发现某处判断错误，这张表是回溯的起点。

| # | beszel 的做法 | Orciny 的做法 | 理由 |
|---|---|---|---|
| 1 | agent 顶层 / hub 在 `internal/`，边界靠约定 | hub 与 agent 各自的子包放进 `*/internal/`，`protocol/` 顶层共享 | 换取编译期强制，成本为零 |
| 2 | `HubRequest[T]` 与 `AgentResponse` 两个结构体，后者塞多个互斥可选字段 | 单一 `Envelope` + `Kind` 分派 | 加消息不动信封 |
| 3 | agent 身份 = 可窃取的 `X-Token` + 硬件派生指纹 TOFU | Ed25519 挑战-应答，指纹 = 公钥 SHA256 | 私钥永不上网；消除"指纹对但公钥错"状态；规避硬件 ID 在 VPS/容器中的重复（beszel 为此硬编码了 `knownBadUUID`） |
| 4 | SSH 密钥仅用于 agent 验证 hub | 双向：agent 验 hub + hub 验 agent | 两侧身份都需要；hub 侧尤其关键，因为 Orciny 的 agent 从 M1 起会向 `~/.claude` 写文件 |
| 5 | `OnClose` 后 goroutine sleep + `weak.Pointer` 延迟置 down | Manager 内的定时器 + 注册表身份校验 | 同样效果，逻辑更直白，且天然处理"旧连接被新连接顶替"的情况 |
| 6 | agent 跑在专用系统用户下 | agent 以目标用户身份运行（`User=` / LaunchAgent） | M1 起要读写 `~/.claude`，属主必须正确 |
| 7 | 每次数据更新都写 DB | `last_seen` 仅状态变化时持久化 | 省掉无意义的高频写；在线状态由 realtime 推送，不依赖 last_seen 新鲜度 |

未偏离、直接采纳的：单 Go module + 根包版本常量、CBOR + `keyasint`、`//go:embed all:dist` 内嵌前端、`Hub` 嵌入 `core.App` + `OnServe` 装配、Go 快照迁移定义 collection、前端直连 PB SDK 与 realtime、build tag 分离 dev/prod 的前端服务方式、`tests.TestApp` 测试脚手架、`install.sh` 的整体形态。

---

## 16. 留给 M1 的接口预留

只预留**改起来会痛**的地方，其余一律不预留（YAGNI）。

- **`protocol.Kind` 的编号分段**（§3.2）：加消息不需要重排。
- **`machines.status` 的 `paused` 态**：schema 中就位，M0 不给入口。
- **`~/.orciny/state.json`**：路径与权限约定写进文档，M0 不创建文件。
- **`hub/internal/` 的包划分**：M1 新增 `configsets/`、`revisions/`、`drift/`、`credentials/`，与现有包平级，不改动已有边界。

**不**预留：配置集相关的 collection（PocketBase 迁移是增量的，届时新增文件即可）、协议的向后兼容分支（没有真实旧版本，抽象必然抽错）、多用户模型（§5.3 已记录取舍）。
