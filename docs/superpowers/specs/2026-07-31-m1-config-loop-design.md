# Orciny M1 · 配置闭环 —— 工程设计

| 文档信息 | |
|---|---|
| 版本 | v1（待实现） |
| 日期 | 2026-07-31 |
| 层次 | 工程设计（模块边界、协议、数据模型、测试策略） |
| 上位文档 | [产品设计文档 v0.1](../../PRODUCT-DESIGN.md) —— 业务与产品层面的唯一依据 |
| 前置文档 | [M0 · 骨架工程设计](2026-07-28-m0-skeleton-design.md) 及其[验收记录](../plans/2026-07-28-m0-skeleton/acceptance.md) |
| 覆盖范围 | 里程碑 M1（产品文档 §10）。M2/M3 各自另立 spec |

本文档只描述 M1。凡产品层面的定位、非目标、术语、路线图，一律以上位文档为准；凡模块边界、协议演进机制、连接生命周期、信任根，一律以 M0 spec 为准。此处都不重复。

---

## 1. M1 的目标与边界

### 1.1 交付物

M0 让作者**看见**自己的机队，M1 让他**管住**它。

M1 结束时：主力机的 `~/.claude` 被采集成配置集 v1，敏感项已抽成凭据；其余机器指派同一配置集后自动对齐；在任意一台机器上顺手改的东西会浮到收件箱，一键收编成新版本并自动同步全机队；apply 失败自动回滚，机器不会被写坏。

具体到功能：

- 导入向导：采集本机 `~/.claude` → 勾选纳管范围 → 敏感项检测与凭据抽取 → 生成配置集 v1
- Web 编辑器：文件树 + 语法高亮 + JSON 校验 + 占位符补全与未定义引用告警
- 版本：草稿 / 发布 / 版本间 diff / 回滚
- 下发：hub 推送信号 → agent 拉取清单与缺失内容 → 渲染落盘 → 回执
- 漂移：fsnotify + 定时对账 → 收件箱 → 收编 / 恢复 / 忽略
- 凭据库与机器变量：加密存储、占位符注入、轮换不产生新版本
- apply 前快照与失败自动回滚

### 1.2 明确不做（推迟到 M2+）

| 事项 | 出处 | 推迟理由 |
|---|---|---|
| agent 自更新 | 产品 §4.1 | 与配置闭环正交；release 流水线与下载源已就位，随时可接 |
| 路径级自动收编规则 | 产品 §4.4 | 产品文档自己写了「默认全部手动，用户逐步放开」。先用手动收编跑一段时间，才知道哪些路径真的该自动 |
| 用量采集、订阅采集、告警通知 | 产品 §4.6 / §4.7 | M2 / v1 |
| 配置集继承与叠加层 | 产品 §4.2 | 产品文档已定 MVP 单层，差异用变量 + 凭据表达 |
| 多工具适配（OpenCode / Codex） | 产品 §7.2 | v2。但 manifest 的路径根为此留了位（§3.1） |

### 1.3 顺带清掉的 M0 尾巴

M0 验收记录「待办汇总」里归属 M1 的两项，在本里程碑一并处理：

1. **`hub.pub` 内容无法解析时 agent 走通用指数退避、无限重试**，而非进入 `Compromised` 终态。一个被替换成垃圾的钉扎公钥同样属于「需要人来判断」的情形，按 M0 spec §7.2 的精神应进终态。
2. **M0 DoD 第 5 条的「断网 60 秒转 offline」与 spec 自身的 70s 读超时不自洽**，改为「75 秒内」。不动超时参数——70 秒是为了容得下 30 秒心跳漏两拍，为凑验收数字去压它会让抖动误判增多。

### 1.4 为什么这些必须一次做对

M1 定的三样东西，返工代价按数量级递增：

- **Revision 的内容表示**（§4、§7.1）：改它等于全部历史版本重新物化。
- **凭据与漂移基线的口径**（§6）：错了会让每台机器永远显示漂移，或者更糟——收编时把密钥明文写进版本历史，而版本是不可变的，写进去就洗不掉。
- **apply 的失败语义**（§7.4）：这是 Orciny 唯一会写用户文件的地方。「宁可不动，不可写坏」如果没在第一版就成立，用户会在被写坏一次之后永久卸载。

---

## 2. 模块边界

### 2.1 新增包

全部与现有包平级，不改动 M0 已立的边界（M0 spec §16 的预留兑现）。

```
hub/internal/
├── blobs/          内容寻址存储：写入去重、按 hash 取、孤儿 GC
├── configsets/     配置集 CRUD、草稿、manifest 校验、指派
├── revisions/      发布、checksum、版本间 diff、回滚
├── credentials/    AES-GCM 加解密、引用计数、轮换广播
├── render/         占位符渲染（hub 侧只用于预览与校验）
├── drift/          收件箱、收编（组装新 Revision）、恢复、忽略
├── importer/       采集结果落库、敏感项检测规则
└── configsync/     下发编排：谁该收到 notify、快照组装、回执落库

agent/internal/
├── manifest/       manifest 展开为具体路径集、路径安全校验、恒排除 denylist
├── blobcache/      ~/.orciny/blobs/ 本地缓存
├── secrets/        ~/.orciny/secrets.json（0600）读写
├── render/         渲染与还原（agent 侧两个方向都要）
├── applier/        plan → 快照 → 原子落盘 → 回滚 → 回执
└── watcher/        fsnotify + 定时对账 + 去抖 + 节流
```

### 2.2 一处必须放进 protocol 的东西

占位符的**语法与转义规则**两侧必须逐位一致：hub 要用它做发布期校验与引用提取，agent 要用它做渲染与还原。按 M0 spec §2.2 的纪律，双方共享的东西只能放 `protocol/`。

`protocol/placeholder.go` 只放三样：占位符的词法（`Parse` 返回引用与字面段）、转义规则、以及从内容中提取引用集合的 `Refs()`。**不含**任何取值逻辑——值从哪来是 hub 与 agent 各自的事。

这与「protocol 包只放数据结构、编解码、以及两侧必须逐位一致的密码学格式」是同一条纪律的延伸：**凡是两侧必须字节级一致的格式，都在 protocol**。checksum 的拼装口径（§7.2）同理。

### 2.3 agent 的两个 HOME

M0 只有一个目录概念：`$ORCINY_HOME`（默认 `~/.orciny`），agent 自己的数据。M1 引入第二个：**被管理的 HOME**，即 `~/.claude` 所在的那个 home。

两者必须分开：测试要把 agent 数据放临时目录、同时把受管 HOME 也放另一个临时目录，否则一个集成测试会去动开发者本人的 `~/.claude`。

`agent.yml` 增加字段：

```yaml
managed_home: ""        # 空 = os.UserHomeDir()；测试与容器场景显式指定
reconcile_interval: 5m  # 定时全量对账周期（§8.1）
```

安装脚本不写这两个字段（留空走默认）。测试脚手架必须显式设置 `managed_home`——**这条写进 `internal/testsupport` 的构造函数签名里，让人无法忘记**。

---

## 3. Manifest 与受管范围

### 3.1 路径根是 HOME，不是 `~/.claude`

`~/.claude.json` 在 `.claude/` 之外。用 `../.claude.json` 表达既丑又与「禁止 `..`」的路径安全规则正面冲突。因此 manifest 的路径一律**以 HOME 为根**：

```json
{
  "version": 1,
  "include": [
    {"path": ".claude/settings.json",    "mode": "file"},
    {"path": ".claude/CLAUDE.md",        "mode": "file"},
    {"path": ".claude/keybindings.json", "mode": "file"},
    {"path": ".claude/agents/**",        "mode": "tree"},
    {"path": ".claude/commands/**",      "mode": "tree"},
    {"path": ".claude/skills/**",        "mode": "tree"},
    {"path": ".claude.json",             "mode": "keys", "keys": ["mcpServers"]}
  ],
  "exclude": ["**/.DS_Store"]
}
```

顺带的收益：v2 引入 OpenCode / Codex 时，`.opencode/**`、`.codex/**` 直接加进同一个 manifest，不用动路径根。

三种 mode：

| mode | 语义 | 漂移比对口径 |
|---|---|---|
| `file` | 单个文件整体受管 | 渲染后全文 hash |
| `tree` | glob 匹配的整棵子树受管，**含新增文件** | 逐文件 hash + 集合差异 |
| `keys` | 只重写 JSON 顶层的指定键，其余键原样保留 | 受管键子树的规范化 JSON hash |

`tree` 模式的「含新增文件」是核心用例的支点：在某台机器上新写一个 `skills/foo/SKILL.md`，它不在任何 Revision 里，必须被识别为 `added` 漂移才谈得上收编。

### 3.2 恒排除不在 manifest 里

产品 §4.3 要求排除规则「恒定不可去除」。那它就不能出现在一个用户可编辑的字段里——否则「不可去除」只是 UI 上的一句话。

**恒排除硬编码在 agent 与 hub 双侧**，两处各有一份常量与一份测试：

```
.claude/projects/**        会话历史
.claude/todos/**
.claude/shell-snapshots/**
.claude/statsig/**
.claude/.credentials.json  OAuth 登录态，机器私有
**/.git/**
**/node_modules/**
```

判定顺序：**恒排除 > manifest.exclude > manifest.include**。任何 include 都无法把恒排除路径拉回来。

双侧各判一次是有意的纵深防御：hub 侧防止「发布了一个包含 `.credentials.json` 的版本」，agent 侧防止「一个被篡改的 hub 骗 agent 上传会话历史」。后者是 agent 唯一能自我保护的地方。

### 3.3 路径安全

manifest 展开与 apply 落盘两处都要校验，任一不通过即整体拒绝：

- 拒绝绝对路径、拒绝任何 `..` 段
- 展开后的真实路径必须仍在 `managed_home` 之下（`filepath.EvalSymlinks` 之后再判一次，防符号链接逃逸）
- 拒绝非常规文件（符号链接、FIFO、设备文件）——遇到时跳过并记入 `Skipped`
- 单文件上限 **512 KiB**（见 §5.4）

---

## 4. 数据模型

`hub/internal/migrations/002_configsets.go`，追加式，不动 `001_initial.go`（M0 spec §8 的纪律）。八个 collection，API rule 全部保持 `nil`（仅 superuser 可访问），与 M0 一致。

### 4.1 Collections

**`blobs`** —— 内容寻址存储

| 字段 | 类型 | 说明 |
|---|---|---|
| `hash` | text，唯一索引 | 内容的 sha256（hex） |
| `size` | number | 字节数 |
| `content` | file | 落在 `pb_data/storage`，不进 SQLite 行 |

**`config_sets`**

| 字段 | 类型 | 说明 |
|---|---|---|
| `name` | text，唯一索引 | |
| `note` | text | |
| `manifest` | JSON | 当前草稿的 manifest |
| `paused` | bool | 「暂停下发」（产品 §4.2） |
| `head` | relation → `revisions` | 当前已发布的最新版本；导入中的草稿态为空 |
| `draft` | JSON | `[{path, hash, size, mode, keys?}]`，与 `revisions.files` 同形状 |
| `draft_refs` | JSON | `{creds: [], vars: []}`，草稿引用到的占位符名 |

**`revisions`** —— 不可变

| 字段 | 类型 | 说明 |
|---|---|---|
| `config_set` | relation | |
| `seq` | number | 配置集内递增；`(config_set, seq)` 唯一索引 |
| `files` | JSON | `[{path, hash, size, mode, keys?}]` |
| `manifest` | JSON | **随版本冻结**，否则历史版本无法解释 |
| `checksum` | text | 见 §7.2 |
| `refs` | JSON | `{creds: [], vars: []}`，下发时按它过滤凭据（§5.3） |
| `note` | text | |
| `source` | select | `publish` / `adopt` / `rollback` / `import` |

**`assignments`** —— 一机一配置集

| 字段 | 类型 | 说明 |
|---|---|---|
| `machine` | relation，**唯一索引** | MVP 的「一机一配置集」由这个唯一索引强制 |
| `config_set` | relation | |
| `mode` | select | `apply` / `survey`（§7.6） |
| `state` | select | `pending` / `applying` / `aligned` / `failed` / `degraded` / `paused` |
| `applied_revision` | relation → `revisions` | |
| `applied_at` | date | |
| `last_error` | text | |

**`credentials`**

| 字段 | 类型 | 说明 |
|---|---|---|
| `name` | text，唯一索引 | 字符集 `[A-Za-z0-9_-]+`，与占位符语法一致 |
| `cipher_value` | text | AES-GCM 密文（base64） |
| `last4` | text | UI 只显示这个 |
| `note` | text | |

**`variables`** —— 机器变量

| 字段 | 类型 | 说明 |
|---|---|---|
| `machine` | relation | |
| `key` | text | `(machine, key)` 唯一索引 |
| `value` | text | 非秘密；秘密请用凭据 |

**`drift_events`** —— 收件箱

| 字段 | 类型 | 说明 |
|---|---|---|
| `machine` | relation | |
| `config_set` | relation | 收编时要知道往哪个配置集里合 |
| `path` | text | |
| `kind` | select | `added` / `modified` / `deleted` |
| `base_hash` | text | 基线版本里该路径的 blob hash；`added` 为空 |
| `current_blob` | relation → `blobs` | 机器现状的还原后内容；`deleted` 与 `truncated` 为空 |
| `mode` | number | 文件权限位 |
| `diff` | text | **hub 算的** unified diff，供 UI 直接渲染（§8.2） |
| `restore_partial` | bool | 有变量未能还原（§6.4） |
| `truncated` | bool | 超限或凭据还原失败，不带内容 |
| `state` | select | `open` / `adopted` / `restored` / `ignored` / `superseded` |
| `resolved_revision` | relation → `revisions` | |
| `resolved_at` | date | |

索引：`(machine, path)` 的**部分唯一索引**，条件 `state = 'open'`。同一路径同一时刻只能有一条待处理漂移——重复检测到就更新那条，不新建。PocketBase 的 `AddIndex` 第四个参数正是 where 子句。

**`ignore_rules`**

| 字段 | 类型 | 说明 |
|---|---|---|
| `machine` | relation，可空 | 空 = 全局规则 |
| `path` | text | 支持 glob |
| `note` | text | |

### 4.2 两个值得单独说的设计

**草稿与发布共用一套形状。** `config_sets.draft` 与 `revisions.files` 是同一个 JSON 形状，于是「草稿 vs head」的 diff 与「v3 vs v1」的 diff 是**同一个函数**，前端也只有一种数据形状要处理。编辑器里改一个字符 → 新内容立刻落 blob → 更新 draft 清单。发布 = 把 draft 冻结成一条 revision。

**blob 永不 GC，除非删除整个配置集。** Revision 不可变、不可删，引用计数天然只增；只有删配置集时才扫一次孤儿。这条让 `blobs` 层薄到只剩 `Put / Get / GCOrphans` 三个函数。

---

## 5. 协议扩展（Kind 10–19）

M0 spec §3.2 给 M1 预留了 10–19 号段，M1 恰好用满。若将来 M1 范围内需要更多消息，**从 40 起新开一段**，不侵占 M2 的 20–29。

| Kind | 名称 | 方向 | 语义 |
|---|---|---|---|
| 10 | `ConfigNotify` | hub → agent | 「你的指派有变化，来拉」。不携带内容 |
| 11 | `ConfigPull` | agent → hub | 请求当前指派的完整快照 |
| 12 | `ConfigSnapshot` | hub → agent | 清单 + manifest + 凭据值 + 变量 + 忽略清单 |
| 13 | `BlobRequest` | agent → hub | 按 hash 批量索取本地缺失的内容 |
| 14 | `BlobData` | hub → agent | 一条一个 blob |
| 15 | `ApplyAck` | agent → hub | 应用结果（逐路径） |
| 16 | `DriftReport` | agent → hub | 漂移批量上报，**携带还原后的完整内容** |
| 17 | `DriftCommand` | hub → agent | `restore` / `ignore` |
| 18 | `CollectRequest` | hub → agent | 导入向导：按给定 manifest 采集本机现状 |
| 19 | `CollectResult` | agent → hub | 采集结果，分批 |

### 5.1 为什么 `ConfigNotify` 不带内容

hub 只发信号、agent 主动拉，是 M0 就定下的形状（产品 §3.3）。M1 保持它，多一个理由：**凭据轮换可以复用同一条消息**。轮换后 hub 发一次 `ConfigNotify`（Revision 没变），agent 拉回来发现 revision ID 相同但 secrets 变了，只重渲染受影响的文件。不需要为轮换新增消息，也不产生新 Revision（产品 §4.5 的硬要求）。

### 5.2 消息结构

```go
// Kind 10
type ConfigNotify struct {
    ConfigSetID string `cbor:"0,keyasint"`
    RevisionID  string `cbor:"1,keyasint,omitempty"` // 空 = 仅 secrets 变更
    Seq         uint32 `cbor:"2,keyasint,omitempty"`
    Reason      string `cbor:"3,keyasint,omitempty"` // published/adopted/rotated/assigned
}

// Kind 11
type ConfigPull struct {
    Have string `cbor:"0,keyasint,omitempty"` // 本地已应用的 RevisionID，供 hub 记日志
}

// Kind 12
type ConfigSnapshot struct {
    ConfigSetID string            `cbor:"0,keyasint"`
    RevisionID  string            `cbor:"1,keyasint"`
    Seq         uint32            `cbor:"2,keyasint"`
    Manifest    []byte            `cbor:"3,keyasint"` // 原样 JSON，与发布时冻结的一致
    Files       []FileEntry       `cbor:"4,keyasint"`
    Checksum    string            `cbor:"5,keyasint"`
    Credentials map[string]string `cbor:"6,keyasint,omitempty"`
    Variables   map[string]string `cbor:"7,keyasint,omitempty"`
    IgnorePaths []string          `cbor:"8,keyasint,omitempty"`
    Mode        uint8             `cbor:"9,keyasint,omitempty"` // 0=apply 1=survey（§7.6）
}

type FileEntry struct {
    Path string   `cbor:"0,keyasint"`
    Hash string   `cbor:"1,keyasint"` // keys 模式为受管键子树的规范化 hash
    Size uint32   `cbor:"2,keyasint"`
    Mode uint32   `cbor:"3,keyasint"` // 0600 / 0644
    Keys []string `cbor:"4,keyasint,omitempty"`
}

// Kind 13 / 14
type BlobRequest struct {
    Hashes []string `cbor:"0,keyasint"`
}
type BlobData struct {
    Hash    string `cbor:"0,keyasint"`
    Content []byte `cbor:"1,keyasint"`
    Missing bool   `cbor:"2,keyasint,omitempty"` // hub 侧找不到，agent 据此中止 apply
}

// Kind 15
type ApplyAck struct {
    RevisionID string        `cbor:"0,keyasint"`
    OK         bool          `cbor:"1,keyasint"`
    Results    []ApplyResult `cbor:"2,keyasint,omitempty"`
    Error      string        `cbor:"3,keyasint,omitempty"`
    RolledBack bool          `cbor:"4,keyasint,omitempty"`
    DurationMs uint32        `cbor:"5,keyasint,omitempty"`
}

type ApplyResult struct {
    Path   string `cbor:"0,keyasint"`
    Action uint8  `cbor:"1,keyasint"` // 1=skip 2=create 3=overwrite 4=delete 5=merge
    Error  string `cbor:"2,keyasint,omitempty"`
}

// Kind 16
type DriftReport struct {
    Items []DriftItem `cbor:"0,keyasint"`
    Final bool        `cbor:"1,keyasint,omitempty"`
    Full  bool        `cbor:"2,keyasint,omitempty"` // survey 模式的全量对账
}

type DriftItem struct {
    Path           string `cbor:"0,keyasint"`
    Kind           uint8  `cbor:"1,keyasint"` // 1=added 2=modified 3=deleted
    BaseHash       string `cbor:"2,keyasint,omitempty"`
    Content        []byte `cbor:"3,keyasint,omitempty"` // 已还原为占位符
    Mode           uint32 `cbor:"4,keyasint,omitempty"`
    RestorePartial bool   `cbor:"5,keyasint,omitempty"` // §6.4
    Truncated      bool   `cbor:"6,keyasint,omitempty"`
}

// Kind 17
type DriftCommand struct {
    Op    string   `cbor:"0,keyasint"` // restore / ignore
    Paths []string `cbor:"1,keyasint"`
}

// Kind 18 / 19
type CollectRequest struct {
    Manifest []byte `cbor:"0,keyasint"`
    Token    string `cbor:"1,keyasint"` // 本次采集的标识，随结果回传，防串批
}
type CollectResult struct {
    Token   string          `cbor:"0,keyasint"`
    Files   []CollectedFile `cbor:"1,keyasint,omitempty"`
    Skipped []SkippedFile   `cbor:"2,keyasint,omitempty"`
    Final   bool            `cbor:"3,keyasint,omitempty"`
    Error   string          `cbor:"4,keyasint,omitempty"`
}

type CollectedFile struct {
    Path    string `cbor:"0,keyasint"`
    Content []byte `cbor:"1,keyasint"`
    Mode    uint32 `cbor:"2,keyasint"`
}
type SkippedFile struct {
    Path   string `cbor:"0,keyasint"`
    Reason string `cbor:"1,keyasint"` // too_large / not_regular / always_excluded / unreadable
}
```

`MachineInfo`（Kind 5）追加两个字段（CBOR keyasint 加字段向后兼容，老版本解码时忽略）：

```go
LocalPaused bool   `cbor:"5,keyasint,omitempty"` // orciny-agent pause
ManagedHome string `cbor:"6,keyasint,omitempty"` // 面板上显示「管的是哪个 home」
```

### 5.3 凭据只发用得到的那些

`ConfigSnapshot.Credentials` **只包含该 Revision 实际引用到的凭据**，不是整个凭据库。引用集合在发布时就算好并存进 `revisions.refs`（§4.1），下发时按它过滤。

这是最小权限，也是一条实际的防线：一台被攻陷的机器不该因为连着 hub 就拿到所有订阅的 key。

### 5.4 分批规则与单文件上限

hub 侧 WS 的 `ReadMaxPayloadSize` 是 1 MiB（M0 已设）。M1 引入了三种可能很大的消息，都要有明确规则：

- **单个受管文件上限 512 KiB**。超限：采集时跳过并记入 `Skipped`，发布时校验拒绝，漂移时只报路径并置 `Truncated`。
  理由：受管的是配置——settings、CLAUDE.md、skills、agents。没有一个正常的配置文件接近 512 KiB。给出明确上限，好过让它在某天以「WS 连接莫名断开」的形式暴露。
- **携带内容的消息按累计 256 KiB 分批**，最后一批置 `Final`。适用于 `DriftReport`、`CollectResult`、`BlobRequest`。
- **`BlobData` 一条一个 blob**，由 512 KiB 的文件上限保证不越界。
- agent 侧的 gws 也要显式设 `ReadMaxPayloadSize = 1 MiB`——M0 只在 hub 侧设了（`hub/internal/ws/handler.go`），agent 侧走的是库默认值。

---

## 6. 凭据、变量与「还原」

这一节是 M1 最容易写错的地方。核心矛盾：**Revision 里存的是 `{{cred.x}}`，落到机器上的是真实密钥**，于是 agent 手里的基线与磁盘内容天生不一致。

### 6.1 占位符语法

```
{{cred.<name>}}       凭据，值来自 hub 加密库
{{var.<name>}}        机器变量
{{machine.name}}      内置：备注名
{{machine.hostname}}
{{machine.os}}
{{machine.arch}}
```

- `<name>` 字符集 `[A-Za-z0-9_-]+`
- 转义：字面量 `{{` 写作 `{{{{`。CLAUDE.md 里讲模板语法时会出现 `{{`，不给转义就没法管理这类文件
- 未定义引用：发布期校验拒绝（hub）；运行期遇到（agent，例如凭据被删）→ **拒绝 apply 该文件并回执报错，绝不落一个含字面 `{{cred.x}}` 的配置给 Claude Code**

**往返律**：`render(restore(x)) == x`，对任意内容与任意值集合成立。这条写成 property test（§10.1），是整个凭据机制的正确性支点。

### 6.2 agent 缓存凭据明文

`~/.orciny/secrets.json`，0600，内容为已注入的凭据与变量值。

理由不是图省事，是三个功能的前提：

1. **漂移 diff 不泄密**：上报前把磁盘里的真实值替回 `{{cred.x}}`，得有值才能替
2. **hub 离线时仍能自愈**：重渲染基线、恢复、回滚都不必等 hub
3. **对账不依赖网络**：5 分钟一次的对账如果每次都要向 hub 换取凭据，hub 一断整个漂移检测就瞎了

安全上这不增加实质暴露面：这些值本来就以明文形态躺在同一台机器、同一个用户、同样权限的 `~/.claude/settings.json` 里。真正的边界是「机器被攻陷」，而那时 `settings.json` 已经先失守了。

### 6.3 漂移基线用「渲染后 hash」

`state.json` 为每个受管路径同时记两个 hash：

| 字段 | 含义 | 用途 |
|---|---|---|
| `blob` | 渲染**前**的内容 hash（= Revision 清单里的 hash） | 判断本地 blob cache 是否命中、要不要向 hub 索取 |
| `rendered` | 渲染**后**落盘内容的 hash | 漂移比对的唯一依据 |

对账只比 `rendered`。凭据轮换后 agent 重写文件并更新 `rendered`，因此**轮换不产生漂移**。

### 6.4 还原：凭据必须成功，变量尽力而为

只有在**确认漂移之后**、需要生成 `DriftItem.Content` 时才做还原。两档承诺，写进 UI 文案：

**凭据——必须还原，失败即不上报内容。**

- 按值长度降序替换（防短值是长值的子串时替错）
- 凭据值长度 < 8 拒绝创建（UI 校验）。一个 6 字符的「密钥」在文件里到处误匹配的风险，远大于它作为密钥的价值
- 还原后再扫一遍：若任一已知凭据值仍能被搜到 → 判定还原失败 → **该条漂移只报路径，`Truncated=true`，不带内容**，UI 提示「该文件含未能安全脱敏的凭据，请在 Web 上手工处理」

**变量——尽力还原，失败则标记。**

- 变量不是秘密，值常常很短（`main`、`1`）
- 规则：仅当值长度 ≥ 4，且它在文件中的出现次数与渲染时一致，才替回占位符
- 否则放弃还原该变量，置 `RestorePartial=true`。收编前 UI 强制人工复核这类条目——diff 里会显示 `main` vs `{{var.workspace}}`，用户看得懂，且不泄密

这个不对称是有意的：泄密不可逆，变量替错只是难看。

### 6.5 引用计数与删除保护

发布时把该 Revision 引用到的名字存进 `revisions.refs`，草稿同理存 `config_sets.draft_refs`。

- 凭据页的「被哪些配置集 / 哪些版本引用」= 查这两个字段，不必全库扫描 blob 内容
- 被任何 **head revision 或 draft** 引用的凭据不允许删除。历史版本的引用只警告不阻止——历史不可变，但也不会再被下发

### 6.6 主密钥

AES-GCM，主密钥来源优先级：

1. 环境变量 `ORCINY_SECRET_KEY`（32 字节的 base64）
2. `pb_data/secret.key`（0600，首次启动自动生成）

启动时若库里有凭据但主密钥解不开 → **拒绝启动**并给出明确错误，不静默把凭据当成损坏数据。「备份恢复到新机器时忘了带密钥文件」是最可能的翻车场景，宁可开不了机，也不能让用户以为一切正常然后把空值下发到全机队。

---

## 7. 配置存储、下发与 apply

### 7.1 内容寻址

一份内容全库只存一次，按 sha256 索引（schema 见 §4.1）。写入前先算 hash，已存在则直接返回。

### 7.2 checksum 口径

两侧必须一致，写进 `protocol` 的文档注释：对每个文件取 `path\x00hash\x00mode\n`，按 path 字典序拼接后整体 sha256。

**回滚**不删任何东西：把旧版的清单复制成一条新 Revision（seq+1，`source=rollback`），note 自动填「回滚自 v1」。历史不可变（产品 §4.2）。

### 7.3 下发链路

```
hub: 发布 / 收编 / 轮换 / 指派变更
  → configsync 算出该收到通知的机器集合
  → ConfigNotify（在线的立即推；离线的等它上线时在握手后补发）
agent:
  → ConfigPull
  → ConfigSnapshot（清单 + manifest + 凭据 + 变量 + 忽略清单）
  → 比对 blobcache，BlobRequest 缺失的 hash → BlobData
  → 生成 plan → 快照 → 落盘 → 更新 state.json
  → ApplyAck
hub: 回执落库，assignment.state 更新，写 events
```

离线补发不需要队列：agent 上线后 hub 无条件发一次 `ConfigNotify`，agent 拉到的快照若与 `state.json` 里的 revision 一致且 secrets 未变，plan 全是 `skip`，零写入。**幂等是补发机制的替代品**。

### 7.4 apply 的 plan 与失败语义

对 manifest 展开的每条路径，plan 给出五种动作之一：

| 动作 | 条件 |
|---|---|
| `skip` | 磁盘 rendered hash == 期望 hash |
| `create` | 期望存在，磁盘不存在 |
| `overwrite` | 都存在但 hash 不同 |
| `delete` | 上一版有、本版无，且磁盘存在 |
| `merge` | `keys` 模式 |

**apply 前快照**：把所有将被写 / 删的路径的当前内容原样（含真实凭据值）存进 `~/.orciny/snapshots/<unix>-<seq>/`，目录 0700 / 文件 0600，附 `manifest.json` 记录路径与原权限。保留最近 5 次。

**落盘**：复用 `internal/atomicfile`（写临时文件 + rename）。含凭据引用的文件 0600，其余 0644。

**失败三级处置**：

1. 任一路径失败 → 逆序还原本次已改动的全部路径 → `ApplyAck{OK:false, RolledBack:true}`
2. 还原本身也失败 → `ApplyAck{OK:false, RolledBack:false}` + agent 进入 **`degraded`**：**不再自动 apply 任何后续版本**，只上报，直到人工在 UI 上确认解除。面板红色告警
3. hub 侧记 `apply.failed` / `apply.rollback_failed` 事件

第 2 条是「宁可不动，不可写坏」的兜底：一次写坏之后继续按新版本去写，只会把现场破坏得更彻底。

### 7.5 `keys` 模式：`~/.claude.json` 的合并

这是 apply 里唯一的合并路径，也是唯一要处理竞写的地方——Claude Code 自己也在写这个文件（记录 project 状态等运行时数据）。

**只改受管键、其余字节原样保留。** 用 `tidwall/sjson` 的 `SetRaw` 做原地编辑，而不是 `json.Unmarshal` 到 map 再 Marshal 回去——后者会丢掉键顺序与格式，让用户每次打开文件都发现被整个重排了。

**竞写缓解**（无法根治，Claude Code 自己不用原子写）：

- 读之前记 mtime + size，写之前再查一次；变了就重读重试，最多 3 次
- 写仍走 `atomicfile`（rename 是原子的）
- 已知限制写进运维文档：Claude Code 在 agent 写入的同一毫秒内重写该文件时，agent 的写入可能被覆盖。下一次对账（≤5 分钟）会发现并重新应用

**漂移比对口径**：只比 `mcpServers` 子树的规范化 JSON（键排序、无空白）hash。用户或 Claude Code 改动其他键完全不算漂移——这正是 `keys` 模式存在的理由。

### 7.6 `survey` 模式：不写盘，只对账

指派配置集给一台机器时，UI 强制二选一：

- **应用配置集**：正常下发，会覆盖本机受管文件（UI 先展示「将影响 N 个文件」）
- **先看看**（`survey`）：`ConfigSnapshot.Mode=1`，agent 拉取但**不落盘**，只做一次全量对账，把所有差异作为漂移上报（`DriftReport.Full=true`）。用户在收件箱里逐条决定收编还是恢复

`survey` 复用漂移路径，不需要任何新协议，却解决了三个问题：

1. 第二台机器接入时不会被无声覆盖（产品 §4.2 要求的「保持本机现状」）
2. 「用户在本机的改动比中台的基线更好」是个人版的常态，第一时间就能被收编，而不是先被覆盖再让人去恢复
3. **`state.json` 丢失时的自愈**：agent 上报状态丢失，hub 把 assignment 打回 `survey`，全量对账进收件箱。绝不因为「我不知道基线是什么」就直接覆盖用户的文件

### 7.7 apply 撞上未解决的漂移

若某路径本机有 open 漂移，而新 Revision 也改了它——覆盖会吞掉用户的本地改动。

规则：**照常覆盖（决策 C1 中台为准），但绝不静默丢数据。** 该路径的当前内容已经作为 `current_blob` 存在 hub 里（§8.2），apply 时把对应 drift_event 置为 `superseded` 而非删除。用户在收件箱的「已被覆盖」筛选里仍能看到它、看 diff、并把它重新收编成新版本。

内容在 blob 里，追得回来——这是「漂移上报直接带全文」这个设计的第二处红利。

### 7.8 agent 本地状态

```
~/.orciny/
├── agent.yml              + managed_home / reconcile_interval
├── identity/
├── status.json            M0 已有：进程连接状态
├── state.json             M1 新增，0600
├── secrets.json           M1 新增，0600
├── blobs/<xx>/<hash>      M1 新增：本地内容缓存
├── snapshots/<ts>-<seq>/  M1 新增：apply 前快照，保留 5 份
└── logs/
```

`state.json`：

```json
{
  "config_set": "<id>", "revision": "<id>", "seq": 7,
  "checksum": "…", "applied_at": "2026-07-31T…",
  "mode": "apply",
  "health": "ok",
  "files": {
    ".claude/settings.json": {
      "blob": "…", "rendered": "…", "mode": 384, "size": 1234
    },
    ".claude.json": {
      "blob": "…", "rendered": "…", "mode": 420, "keys": ["mcpServers"]
    }
  },
  "ignored": [".claude/skills/scratch/**"],
  "paused": false
}
```

`health` 取 `ok` / `degraded`（§7.4）。

### 7.9 CLI 补全（产品 §5.3）

| 命令 | M1 行为 |
|---|---|
| `orciny-agent sync` | 触发一次立即 pull + apply，打印 plan 与结果 |
| `orciny-agent drift` | 打印本机漂移清单，含本地算出的简易 diff（凭据已脱敏） |
| `orciny-agent pause` / `resume` | 本地暂停 / 恢复配置管理，写 `state.json` 并上报 hub |
| `orciny-agent status` | 补充显示：已应用版本、待处理漂移数、health |

`drift` 子命令自行做简易 diff，是 §8.2「diff 由 hub 计算」的唯一例外——离线排查时人得能在机器上看到发生了什么，而这份 diff 不进任何数据结构，只打到终端。

---

## 8. 漂移与收编

### 8.1 检测

- **fsnotify** 监视 manifest 展开后的目录集合。`.claude.json` 在 HOME 根下，监视 HOME 目录但只对这一个文件名响应（噪音在 agent 内部过滤，不产生 IO）
- **去抖 2 秒**：编辑器保存往往触发多个事件
- **定时全量对账**，默认 5 分钟（`agent.yml` 的 `reconcile_interval`）。兜底 fsnotify 漏事件的场景：网络文件系统、容器 bind mount、inotify 队列溢出
- **上报节流**：同一路径 30 秒内不重复上报
- inotify watch 数：`skills/**` 递归通常几十个目录，远低于默认 8192 上限。写进运维文档，不做特殊处理

### 8.2 agent 不算 diff

`DriftReport` 只带**还原后的完整内容**，diff 由 hub 计算（hub 手里同时有 base blob 与 current blob），算完存进 `drift_events.diff`。

- agent 更瘦（M1 之后它还要背 M2 的采集器，每一克都要省）
- diff 算法只有一份实现，前后端口径天然一致
- **收编退化成纯 hub 侧操作**：用已有的 blob 组装新 Revision 就完事，不需要「通知机器上传内容」的第二次往返，三方对比也立即可用

代价是每次漂移上报多传几 KB。配置文件都很小，这个代价划算。

### 8.3 收编

1. 选中 N 条 open drift（可跨文件；可跨机器，但必须同一配置集）
2. 以 head Revision 为基，逐条把 `current_blob` 覆盖进清单（`added` → 新增条目，`deleted` → 移除条目）
3. 生成新 Revision（`source=adopt`），note 自动填「收编自 `<machine>` 的 N 项改动」
4. 把同样的逐条覆盖再落一遍到 `config_sets.draft`——面板的「配置」页读的是草稿，草稿不跟着走就是「收编了却看不见」，而且用户下一次发布会拿旧草稿把刚收编的内容推回去，漂移原地复活。只盖收编涉及的路径，草稿里别的路径上未发布的编辑照旧保留（与 §M1.5 改绑定同一取舍）
5. 这些 drift_event 置 `adopted`，记 `resolved_revision`
6. 向指派该配置集的**所有**机器发 `ConfigNotify`，**包括来源机器**——它 apply 后 rendered hash 与磁盘一致，plan 全是 skip，天然幂等。不给来源机器开特例，就少一条会腐烂的分支

**冲突**：若选中的多条 drift 来自不同机器且路径相同，UI 直接阻止提交，要求先看三方对比（基线 / 机器 A / 机器 B）再选一个。产品 §4.4 要求「必须显式警告」，这里做成硬阻止而不是警告——两台机器对同一文件的改动，静默取其一是最容易让人丢工作成果的操作。

**含 `restore_partial` 的条目**在收编前强制人工确认一次（§6.4）。

### 8.4 恢复与忽略

- **恢复**：`DriftCommand{Op:"restore", Paths}` → agent 按 `state.json` 基线重写这些路径（走完整的快照 + 原子写 + 回滚流程）→ `ApplyAck` → hub 置 `restored`
- **忽略**：写 `ignore_rules`（机器级，或留空表示全局），随下次快照下发；agent 侧直接跳过这些路径。drift_event 置 `ignored`

### 8.5 事件流

`events.kind` 新增取值，供机器详情与总览的事件流使用：

```
configset.published   configset.rolled_back   assign.changed
apply.ok   apply.failed   apply.rollback_failed
drift.reported   drift.adopted   drift.restored   drift.ignored   drift.superseded
credential.created   credential.rotated   credential.deleted
import.completed
```

---

## 9. 导入向导

产品 §4.2 称它为「个人版关键路径」——它是新用户从零到有配置集的唯一入口，卡在这里就等于卡在门口。

### 9.1 流程

1. 首台机器上线后（或机器详情页手动触发）→ 「把这台机器的 `~/.claude` 采集为配置集」
2. hub 发 `CollectRequest{Manifest, Token}`（默认 manifest）
3. agent 展开、读取、跳过恒排除与超限文件 → 分批 `CollectResult`，含 `Skipped[{Path, Reason}]`
4. hub 落 blob，**直接建一个草稿态 config_set**（`head` 为空，`draft` = 采集结果）。不需要单独的 `import_sessions` collection——草稿本来就是这个形状
5. Web 呈现文件树，勾选纳管范围；未勾选的从 draft 移除
6. **敏感项检测**跑在 draft 内容上，逐条引导抽取为凭据
7. 发布 → Revision 1（`source=import`）→ 指派给该机器（`apply` 模式）→ 正常下发

第 7 步没有特例路径：内容就是从这台机器采的，占位符渲染回去与磁盘一致，plan 几乎全是 `skip`，只重写被抽取了凭据的那几个文件（渲染后内容其实一模一样）。**用一条正常路径完成一件看起来特殊的事**，比写一个「导入后直接标记已对齐」的捷径要可靠——后者会让 `state.json` 从未被真正写过一次，第一次真正的 apply 会在几周后以一种没人预期的方式发生。

### 9.2 敏感项检测规则

`hub/internal/importer`，三层：

**结构化位置**（准确率最高，优先展示）

- `.claude/settings.json` 的 `env` 对象所有键值对
- `.claude.json` 的 `mcpServers.*.env`

**键名特征**：`(?i)(key|token|secret|password|passwd|credential|auth)`

**值特征**

- 已知前缀：`sk-`、`sk-ant-`、`ghp_`、`gho_`、`github_pat_`、`xoxb-`、`AKIA`、`AIza`、`glpat-`
- 或：长度 ≥ 32 且 Shannon 熵 ≥ 3.5 的 `[A-Za-z0-9+/_=-]+` 串

**全文兜底**：所有文本文件跑一遍前缀正则——CLAUDE.md 里粘了一个 key 也要能抓到。

每条给出：文件、位置、键名、值的掩码（前 4 后 4）、由键名派生的建议凭据名（`ANTHROPIC_AUTH_TOKEN` → `anthropic_auth_token`）。

三个动作：**抽取为凭据** / **保留明文**（二次确认，警告「该值将进入不可变的版本历史」）/ **把该文件移出纳管范围**。

误报可接受，漏报不可接受——所以规则宁滥勿缺，交互上让用户一键跳过。

---

## 10. 测试策略

### 10.1 Go 侧

沿用 M0 的规矩：注入时钟，**不允许 `time.Sleep`**（漂移去抖 2 秒、对账 5 分钟、节流 30 秒，真睡的话测试套件会跑到几分钟）。

**protocol / placeholder**

- 词法：合法名、非法名、未闭合、嵌套
- 转义：`{{{{` ↔ `{{` 双向
- **往返 property test：`render(restore(x)) == x`**，随机内容 × 随机值集合
- `Refs()` 提取完整且不含重复

**blobs**：去重（同内容两次写只落一份）、并发写同 hash、按 hash 取、孤儿 GC 只删无引用者

**revisions**：checksum 口径固定（写死期望值，防止哪天改了拼装顺序而无人察觉）、seq 并发递增不重复、回滚生成新版本而非改历史、版本间 diff（增 / 删 / 改）

**manifest**：三种 mode 的展开、恒排除不可被 include 覆盖、`..` 与绝对路径被拒、符号链接逃逸被拒、非常规文件被跳过、超限文件被跳过

**credentials**：加解密往返、主密钥缺失或更换时拒绝启动、引用计数正确、被引用者不可删

**render / restore（agent 侧）**

- 凭据按值长度降序替换
- **还原后的内容中不含任何已知凭据值**——这是安全测试，独立成例并在 CI 里显式命名
- 变量短值放弃还原并置 `RestorePartial`
- 未定义引用 → 拒绝 apply 该文件

**applier**（临时 managed home）

- 五种动作各一例
- 失败回滚：注入一个写失败，断言全部路径回到 apply 前状态
- 回滚失败 → `degraded`，后续 apply 被拒
- 幂等重放：同一 revision apply 两次，第二次全 skip、零写入
- 权限位：含凭据 0600、其余 0644
- `keys` 合并：保留未受管键与原始格式；mtime 变化触发重试；3 次失败后放弃并报错

**watcher**：事件 → 去抖 → 上报；定时对账兜底（假时钟推进）；忽略清单过滤；30 秒节流；`tree` 模式识别新增文件

**drift / 收编**：单条、多条、跨文件；跨机器同路径冲突被阻止；收编后所有指派机器收到 notify；`superseded` 不删除记录

**集成（`internal/testsupport`）**：一条端到端链路——导入 → 发布 → 指派 → 下发 → 落盘 → 回执 → 改文件 → 漂移 → 收编 → 新版本 → 第二台机器对齐。这条测试绿了，M1 的主干就是通的。

`testsupport` 的构造函数必须同时接受 agent 目录与 managed home 两个临时目录（§2.3）。

### 10.2 前端

M0 spec §10.3 明示「M1 引入编辑器（有真实的校验逻辑和状态机）时再建前端测试体系」，本里程碑兑现。

**Vitest + React Testing Library**，只测纯逻辑：

- 占位符解析、未定义引用告警、补全候选
- JSON 校验与错误定位
- 草稿状态机：干净 / 脏 / 发布中 / 发布失败 / 丢弃
- diff 结果到渲染分组的映射
- 三方对比的冲突判定
- 敏感项检测结果的渲染与掩码（**断言掩码后的值不含完整密钥**）

**不测** realtime 订阅与 PB SDK 交互——那是 M0 已定的取舍，靠真机验收覆盖。

---

## 11. Web UI

| 页面 | M1 内容 |
|---|---|
| 配置集列表 | 名称、指派机器数、head 版本、暂停下发开关、克隆、删除 |
| 配置集详情 | 文件树 + CodeMirror 编辑器；草稿 vs head 的 diff；发布对话框（note + 影响机器预览）；版本历史与任意两版 diff；回滚 |
| 指派 | 在配置集详情选机器，或在机器详情选配置集；强制二选一的 `apply` / `survey` 模式（§7.6） |
| 收件箱 | 漂移队列，按机器 / 文件分组；diff 预览；收编 / 恢复 / 忽略；多选合并收编；冲突三方对比；「已被覆盖」筛选 |
| 凭据 | 列表（只显示末四位）、新增、轮换、引用计数、删除保护提示 |
| 机器详情 | 补上：配置对齐状态、apply 回执历史、机器变量编辑、本机漂移；`degraded` 红色告警与解除按钮 |
| 导入向导 | 采集 → 勾选纳管 → 敏感项抽取 → 发布，四步 |

**选型**：CodeMirror 6（模块化按需引入，JSON / Markdown 语法 + lint，gzip 后约 150–250 KB）+ 一个纯算法 diff 库（约 10 KB）算差异，渲染自己写。

不选 Monaco 的理由：3–5 MB 打包体积会顶到 hub 二进制 < 30 MB 的硬目标，worker 加载配置繁琐，主题要单独适配暖黑 / 暖纸双主题，而三方对比这种非标准 UI 它也帮不上忙——最贵的那部分仍然要自己写。

沿用 M0 已立的结构约定：collection 类型集中在 `types/collections.ts`，realtime 订阅统一收在 `stores/`，未实现页面用 `<Placeholder milestone="M2" />`。

---

## 12. 验收标准（M1 Definition of Done）

对照产品 §10 的 M1 验收（「主力机导入成配置集；全机队对齐；在任一机器改 CLAUDE.md / 新增 skill 能被收编并同步到其余机器；apply 失败可自动回滚」），细化为：

| # | 标准 |
|---|---|
| 1 | 主力机跑完导入向导 → 生成配置集 v1；敏感项被抽成凭据；**直接查库确认 blob 内容里不含任何明文密钥** |
| 2 | 第二台机器以 `apply` 模式指派同一配置集 → 落盘成功 → 两台机器受管文件内容一致（凭据与变量渲染差异除外） |
| 3 | 第三台机器以 `survey` 模式指派 → 不写盘，本机与基线的全部差异出现在收件箱 |
| 4 | 在任一机器新增 `skills/foo/SKILL.md` → 60 秒内出现在收件箱 → 收编 → 其余机器自动落盘 |
| 5 | 改 CLAUDE.md → 漂移 diff 正确 → 恢复 → 文件回到基线 |
| 6 | 改 `settings.json` 里的密钥值 → 上报的漂移内容中**不含明文**（查库确认），条目已正确还原或被标为 `truncated` |
| 7 | 手动往 `.claude.json` 加一个非受管键 → apply 后该键仍在，且键顺序未被重排 |
| 8 | 制造 apply 失败（目标目录只读）→ 自动回滚 → 文件回到 apply 前状态 → 面板显示失败原因 |
| 9 | 凭据轮换 → 不产生新 Revision → 受影响机器重注入 → 文件更新 → 不产生漂移 |
| 10 | 发布 v3 → 回滚到 v1 → 生成 v4（内容 == v1）→ 全机队对齐；v1–v3 历史仍在 |
| 11 | 两台机器对同一文件各有漂移 → 收件箱强制三方对比，不允许直接收编 |
| 12 | 删除 `state.json` 后重启 agent → 进入 `survey` 全量对账，**不覆盖任何用户文件** |
| 13 | `go test -tags=testing ./...` 全绿；前端 `vitest run` 全绿 |
| 14 | agent 常驻 < 30 MB RAM（含 fsnotify 与 blob cache）、空闲 CPU ≈ 0；hub < 64 MB |
| 15 | M0 尾巴：`hub.pub` 解析失败进 `Compromised`；M0 DoD 第 5 条改为 75 秒 |

---

## 13. 实现切分

供 writing-plans 展开。两阶段，各自末尾有一次真机验收。

**阶段一 · 下发闭环**

1. `migrations/002_configsets.go`：八个 collection + 索引
2. `blobs` 层
3. `protocol`：Kind 10–19 消息、`placeholder.go`、checksum 口径 + 单元测试
4. `credentials` + `variables` + 主密钥 + 引用提取
5. `configsets` / `revisions`：草稿、发布、checksum、diff、回滚
6. `manifest` 展开与路径安全（agent 侧实现，hub 侧同规则校验）
7. agent `blobcache` + `secrets` + `render`（渲染方向）
8. agent `applier`：plan、快照、原子写、回滚、`keys` 合并、`state.json`
9. 下发全链路：`configsync` + Notify / Pull / Snapshot / BlobRequest / BlobData / ApplyAck + 集成测试
10. 导入向导后端：`CollectRequest` / `CollectResult` + `importer` 敏感项检测
11. 前端：配置集列表 / 编辑器 / 发布 / diff / 版本历史 / 回滚、凭据页、变量、指派、导入向导；**同步建立 Vitest 体系**

→ 真机验收 DoD 1、2、7、8、9、10、13、14

**阶段二 · 漂移闭环**

12. agent `render` 还原方向 + 安全测试
13. agent `watcher`：fsnotify、去抖、定时对账、节流
14. `DriftReport` 链路 + hub 侧 diff 计算 + `drift_events`
15. 收件箱后端：收编 / 恢复 / 忽略 / 冲突判定 / `superseded`
16. `survey` 模式与状态丢失自愈
17. 前端：收件箱、三方对比、机器详情补全
18. CLI：`sync` / `drift` / `pause` / `resume`
19. M0 尾巴修正（§1.3）

→ 真机验收 DoD 3、4、5、6、11、12、15

---

## 14. 留给 M2 的接口预留

只预留改起来会痛的，其余一律不预留。

- **`protocol.Kind` 20–29 段**保持空置给 M2 数据面；M1 若需溢出，从 40 起
- **`events.kind`** 是自由文本，M2 加事件类型不需要迁移
- **agent 的定时器框架**：`watcher` 的对账定时器与注入时钟，M2 的采集器直接复用同一套，不再造第二个调度器
- **`blobs` 层**：M2 若要存采集器快照或会话摘要，内容寻址存储可直接复用

**不**预留：多工具的 manifest 抽象（M1 只有 Claude Code，抽象必然抽错）、配置集叠加层的 schema、OTel 相关字段。

---

## 15. 相对 M0 的新增取舍记录

延续 M0 spec §15 的做法：将来若发现某处判断错误，这张表是回溯的起点。

| # | 决定 | 理由 | 代价 |
|---|---|---|---|
| 1 | manifest 路径根改为 HOME | `~/.claude.json` 在 `.claude/` 之外，用 `../` 与路径安全规则冲突 | 无；顺带为 v2 多工具留位 |
| 2 | 漂移上报直接携带完整内容 | 收编退化为纯 hub 侧操作，三方对比立即可用，被覆盖的内容可追回 | 每次多传几 KB |
| 3 | agent 缓存凭据明文 | 脱敏上报、离线自愈、对账不依赖网络三者的共同前提 | 多一处明文落盘（但同机同用户下 `settings.json` 本就是明文） |
| 4 | diff 由 hub 计算 | agent 更瘦；算法只有一份实现 | `drift` 子命令的终端输出需自带一份简易 diff（§7.9） |
| 5 | 恒排除硬编码双侧 | 「不可去除」不能只是 UI 上的一句话 | 增删恒排除项需要发版 |
| 6 | `survey` 模式复用漂移路径 | 一条机制同时解决首次指派、保持现状、状态丢失自愈 | `ConfigSnapshot` 多一个 `Mode` 字段 |
| 7 | 单文件上限 512 KiB | 明确的上限，好过某天以「WS 莫名断开」的形式暴露 | 极端情况下有文件不能纳管（会明确报出） |
| 8 | 回滚失败进 `degraded` 并停止自动 apply | 写坏之后继续写只会破坏得更彻底 | 需要人工解除 |
| 9 | 跨机器同路径冲突硬阻止而非警告 | 静默取其一是最容易让人丢工作成果的操作 | 多一步点击 |
| 10 | 收编不给来源机器开特例 | 幂等已经保证它是空操作；特例分支会腐烂 | 来源机器多一次无写入的 apply |
