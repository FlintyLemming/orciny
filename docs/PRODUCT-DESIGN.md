# Orciny 产品设计文档

> 面向个人与微型团队的自托管 Coding Agent 机队中台。
> 集中管理多台机器上的 `~/.claude`，聚合 Token 用量与订阅余额。

| 文档信息 | |
|---|---|
| 版本 | v0.1（草案，待评审） |
| 日期 | 2026-07-21 |
| 作者 | Claude（调研与撰写）· mucr（决策） |
| 前置文档 | 《Orciny 项目构思报告》（2026-07-21） |
| 性质 | 产品设计（业务与产品层面，不含代码实现细节） |

## 决策基线（已拍板，本文档一律以此为前提）

| # | 决策 | 选择 |
|---|---|---|
| A | 与 leycode 的关系 | **A1** 独立新项目，协议对齐 |
| B | 技术底座 | **B1** Go 全家桶：PocketBase hub + Go agent |
| C | 配置同步模型 | **C1** 中台为准 + 漂移收编 |
| D | 用量数据采集 | **D1** 会话 JSONL 解析上报（MVP）；OTel 留 v1 |
| E | ai-plan-insight 合并路径 | **E1** 一次性 Go 重写，Python 退役 |
| F | 连接与安全模型 | **F1** agent 外拨 WebSocket（beszel 模式） |
| G | 开源与商业策略 | **G2** MIT 开源，社区驱动 |

> E1 与构思报告推荐（E3）不同，差异影响分析见附录 B。

---

## 1. 产品概述

### 1.1 一句话定位

**Orciny 是一个自托管的 Coding Agent 机队中台：一个 hub 管住你所有机器上的 Coding Agent 配置，顺带看清它们的用量和订阅余额。**

类比：beszel 之于服务器监控，Orciny 之于 Coding Agent 管理。

### 1.2 背景与要解决的问题

一个重度 Claude Code 用户通常拥有多台 Linux/macOS 机器（工作站、笔记本、家里的 Mac mini、云上 VPS），并同时持有多个 AI Coding 订阅（Claude、GLM、Kimi、火山方舟……）。由此产生三个长期痛点：

1. **配置漂移**：`~/.claude` 下的 settings、CLAUDE.md、skills、agents、MCP 声明在各台机器上各自演化，好用的改动留在一台机器上，坏掉的配置不知道何时引入。
2. **用量黑盒**：Token 消耗分散在每台机器的本地会话文件里，没有跨机器视角；哪台机器、哪个模型烧得多，全凭感觉。
3. **订阅分散**：多家 Coding Plan 的余额/配额要逐家登录查看；部分服务（Claude、Cursor 等）只能在装有本地凭据的机器上查询。

现状方案（rsync 脚本、Syncthing、git dotfiles、各家独立的 usage agent）都是单点的、无状态面板的、各管一段的。Anthropic 官方无原生多机同步（截至 2026-07 有至少 4 个 open feature request）。

### 1.3 设计哲学

**不接管 Agent 执行，只统一治理配置与观测。**

Coding Agent 仍然在每台机器本地运行；Orciny 决定的是：每台机器用什么配置、装什么 skills、数据汇总到哪里看。Orciny 永远不站在模型请求链路上，也不代理、不调度任何会话。

个人版特有的第二哲学：**漂移不是违规，是创作。** 企业中台把机器上的擅自改动视为需要纠正的漂移；个人在机器上顺手改配置、装插件、写 skill 是主要的创作方式。Orciny 的核心交互「收编」（把某台机器的本地改动一键提升为全机队新基线）就是为此设计的。

### 1.4 目标用户

| 层级 | 用户 | 需求特征 | 优先级 |
|---|---|---|---|
| P0 | 项目作者本人 | 多台 Linux/macOS + 多订阅，全部痛点的原型 | MVP 唯一服务对象 |
| P1 | 同类个人开发者 | self-hosted / homelab 人群 + 重度 Coding Agent 用户 | 开源发布目标 |
| P2 | 微型团队（2–10 人） | 共享 skills / CLAUDE.md 约定 / 配置基线，不需要 RBAC 与审计 | v2 |
| P3 | 企业 | 治理、合规、多租户 | 不承接，导流 leycode |

### 1.5 非目标（范围护栏）

以下明确**不做**，它们同时是与相邻赛道的分界线：

- **不做模型路由 / API 网关**——不站在请求链路上（claude-code-hub 等代理类产品的地盘）。
- **不做 GUI 客户端**——agent 无界面，systemd/launchd 常驻；一切交互在 hub Web UI。
- **不做会话执行编排**——不派任务、不开会话、不做任务看板（Multica / vibe-kanban 等的红海）。
- **不做企业治理**——无多租户 RBAC、无合规审计；单管理员起步。
- **Windows 缓做**——WSL 场景由 leycode-client 覆盖；Orciny 首发 Linux + macOS。

---

## 2. 术语表

| 术语 | 英文 | 定义 |
|---|---|---|
| 机器 | Machine | 接入 hub 的一台主机（安装了 agent） |
| 配置集 | ConfigSet | 一组受管配置文件的逻辑单元，对应 leycode 的 Profile |
| 版本 | Revision | 配置集的一次不可变发布快照，带 checksum |
| 指派 | Assignment | 机器与配置集的绑定关系（MVP 为一机一配置集） |
| 受管清单 | Manifest | 定义配置集管理 `~/.claude` 下哪些路径（含排除规则） |
| 漂移 | Drift | 机器本地受管文件与已应用版本之间的差异 |
| 收编 | Adopt | 把某台机器的漂移提升为配置集新版本的动作（个人版核心交互） |
| 恢复 | Restore | 丢弃本地漂移、回到已发布基线的动作 |
| 凭据 | Credential | API key / token 等秘密，独立于配置版本管理，下发时注入 |
| 机器变量 | Variable | 机器级键值对，配置文件中以占位符引用，落盘时替换 |
| 用量点 | UsagePoint | 一条聚合后的 Token 用量记录（日期 × 机器 × 工具 × 模型） |
| 采集器 | Collector | 订阅余额的抓取单元，分 fetch（hub 拉）/ local（agent 抓）/ push（外部推）三型 |
| 订阅卡片 | SubscriptionCard | 一个订阅实例的余额/配额展示单元（沿用 ai-plan-insight 概念） |
| 收件箱 | Inbox | 漂移与告警的统一待处理队列 |

---

## 3. 系统形态与架构

### 3.1 组件

**Hub（`orciny`）**
- Go 单二进制，PocketBase 内核（内嵌 SQLite、认证、realtime、admin、备份），Web UI（React）编译内嵌。
- 职责：配置集存储与版本管理、下发调度、漂移收件箱、凭据库、用量与订阅数据聚合、fetch 型采集器、Web UI、（v1）告警。
- 部署：`docker run` 一行，或单二进制直跑；数据 = 一个目录（SQLite + 上传文件）。

**Agent（`orciny-agent`）**
- Go 单二进制，无界面，systemd（Linux）/ launchd（macOS）常驻；提供最小 CLI。
- 职责：注册与心跳、接收并落盘配置、apply 回执、漂移侦测与上报、会话 JSONL 解析上报、local 型订阅采集、自更新。

### 3.2 连接模型（决策 F1）

- agent **主动外拨** hub 的 WebSocket 端点，长连接保持；断线指数退避重连。
- 首次接入：管理员在 Web UI 生成**一次性注册 token**（限时、单次），`orciny-agent enroll --hub <url> --token <t>` 完成注册；agent 生成密钥对，**指纹绑定**机器身份，后续连接凭指纹+密钥认证，token 即焚。
- 在线状态：长连接存续即在线；心跳周期 30s（可配）。
- 下发：hub 通过长连接**即时推送**「有新版本」信号，agent 拉取快照并应用——机器秒级对齐。
- TLS：hub 假定部署在反向代理之后（Caddy/Nginx/Traefik）；文档提供标准配置。
- 预留：HTTP 长轮询降级通道（协议形状与 leycode 对齐）不进 MVP，接口层留位。

### 3.3 数据流（四条）

1. **配置下行**：Web 编辑 → 发布 Revision → hub 推送信号 → agent 拉取快照 → 落盘（凭据/变量注入）→ apply 回执上行。
2. **漂移上行**：agent 监视受管路径（fs 事件 + 定时哈希对账）→ 差异摘要上报 → hub 收件箱 → 用户「收编 / 恢复 / 忽略」→ 收编则物化为新 Revision 并向其余机器下发。
3. **用量上行**：agent 增量解析本地会话 JSONL → 聚合为 UsagePoint 批量上报（含首次历史回灌）。
4. **订阅采集**：fetch 型由 hub 定时拉取各家 API；local 型由 agent 在本机完成（本地凭据不出机器，只上报结果）；push 型由外部进程向 hub HTTP 端点推送。

### 3.4 部署形态

| 场景 | 形态 |
|---|---|
| 推荐 | 内网或 VPS 上 `docker compose up -d`（hub）+ 各机器一行安装脚本（agent） |
| 最小 | hub 单二进制直跑（个人内网，SQLite 文件即全部状态） |
| 备份 | PocketBase 自带备份（本地目录 / S3 兼容存储），一键恢复 |

---

## 4. 功能规格

### 4.1 机器管理

**接入流程**
1. Web UI「添加机器」→ 生成一次性注册 token（默认 15 分钟有效）+ 拷贝式安装命令（`curl -fsSL https://<hub>/install.sh | sh -s -- --token <t>`）。
2. 安装脚本探测 OS/arch → 下载 agent → 写入 systemd/launchd 单元 → `enroll` → 上线。
3. agent 上线即上报：hostname、OS/arch、agent 版本、探测到的 Coding Agent 工具及版本（MVP：Claude Code）。

**机器列表（页面）**
- 列：名称（可改备注名）、在线状态、指派的配置集与版本对齐状态（已对齐 / 待应用 / 漂移 n 项 / 应用失败）、agent 版本、Claude Code 版本、最后心跳。
- 行内操作：立即同步、查看漂移、暂停管理（agent 保持连接但不应用下发）、移除（吊销指纹，agent 侧保留本地配置不清理）。

**机器详情（页面）**
- 基本信息与状态；apply 回执历史（时间、版本、结果、失败原因）；本机漂移记录；机器变量编辑；本机用量速览。

**agent 自更新**
- hub 持有目标 agent 版本；agent 收到更新通知后自替换二进制并重启（beszel 同款机制）；可全局关闭。

### 4.2 配置集与版本

**配置集（ConfigSet）**
- 内容 = 受管清单（manifest）+ 文件树（受管文件的内容）+ 凭据/变量引用。
- MVP 结构为**单层**（无继承/叠加）；多机差异通过「机器变量 + 凭据引用」表达；配置集可**克隆**。继承/叠加层留 v2。
- 指派：一台机器同一时间指派**一个**配置集；一个配置集可指派给多台机器。
- 下发开关：配置集级「暂停下发」（对齐 leycode `distribution_enabled` 语义）。

**版本（Revision）**
- 发布 = 生成不可变快照 + checksum；历史版本可浏览、可 diff（文件级 + 行级）、可一键回滚（回滚即发布一个内容等于旧版的新版本，历史不可变）。
- 草稿：编辑在草稿态进行，发布前可预览与已发布版本的 diff。

**导入向导（个人版关键路径）**
- 首台机器接入后引导：「把这台机器现有的 `~/.claude` 采集为初始配置集」。
- 流程：agent 按默认 manifest 采集 → 上传 → Web 呈现文件清单让用户勾选纳管范围 → **敏感项检测**（识别 settings env 中的 API key 等，引导抽取为凭据占位符）→ 生成配置集 v1 并标记该机器已对齐。
- 后续机器接入时：选择「应用现有配置集」或「保持本机现状（仅观测，不管配置）」——后者允许纯用量/订阅场景的机器接入。

**服务绑定（M1.5）**
- 「用哪家的哪个模型」是一等实体，不是配置文件里的一段文本。配置集绑定**一个** AI 服务配置（Provider）+ 四个模型槽（主/opus/sonnet/haiku）。
- `settings.json` 里只落 `{{provider.*}}` 占位符（`base_url`、`auth_token` 与四个模型槽，共六个内置名），真实值在 agent 落盘时注入；blob 与 Revision 里永远只有占位符。
- 核心不变量：**换绑定**（配置集从 A 切到 B、换模型）产生新 Revision；**改 Provider 内部**（base_url、追加模型、轮换 key）**不产生**新 Revision，走重注入通知，因此也不产生漂移。
- 四槽而非单槽：Claude Code 会自己去要 haiku 做标题生成一类的轻量活，只钉主模型会让它拿 `claude-haiku-*` 去打人家的 endpoint。四槽全空 = 透传模式。
- 内置平台预设编译进二进制、只读；自定义平台由「preset 留空的 Provider」完全覆盖。
- 绑定漂移：机器上手改 `ANTHROPIC_BASE_URL` 会被识别出来，**禁用收编**（收编会把占位符拍平成硬编码字面值，绑定当场失效），并按 base_url 反查三档给出「改绑定」/「新建服务配置」/兜底说明。

**编辑器（Web）**
- 文件树 + 等宽文本编辑；JSON 文件做语法校验；`settings.json` 提供 permissions/hooks/env 的表单辅助（MVP 可先纯文本）。
- 占位符提示：`{{cred.<name>}}`、`{{var.<name>}}`、`{{provider.<key>}}` 的自动补全与未定义引用告警。
- 发布校验：JSON 合法性、凭据引用可解析、路径安全（禁 `..`/绝对路径/符号链接逃逸）；服务绑定三条校验（引用了但没绑 = 错误、绑了但没用 = 警告、`auth_field` 键名不符 = 错误 + 一键修复）。

### 4.3 受管范围与 Manifest

默认 manifest（用户可增删，排除规则恒定不可去除）：

| 类别 | 路径 | 策略 |
|---|---|---|
| 纳管（默认） | `settings.json` | 整文件受管，env 中敏感值以凭据占位符表达 |
| 纳管（默认） | `CLAUDE.md`、`agents/**`、`commands/**`、`skills/**`、`keybindings.json` | 整文件受管 |
| 纳管（键级） | `~/.claude.json` 中的 `mcpServers` | **键级受管**：该文件混有运行时状态，agent 只重写受管键，其余键原样保留 |
| 恒排除 | `projects/`（会话历史）、`todos/`、`shell-snapshots/`、`statsig/`、缓存目录 | 永不采集、永不下发、永不上传 |
| 恒排除 | `.credentials.json`（OAuth 登录态） | 机器私有；local 采集器仅本机读取，内容不上传 |

设计原则：**宁可少管，不可误管**。未在 manifest 中的路径 agent 一概不触碰。

### 4.4 漂移与收编（决策 C1，本产品的灵魂交互）

**检测**
- agent 对受管路径做文件系统监视 + 周期性哈希对账（默认 5 分钟兜底），与「已应用版本」比对。
- 漂移摘要上报：路径、变更类型（增/删/改）、内容 diff（脱敏后）；恒排除路径永不比对。

**收件箱（页面）**
- 全局待处理队列：每条漂移显示来源机器、文件、diff 预览。
- 三个动作：
  - **收编（Adopt）**：以该机器的现状生成配置集新 Revision，自动下发给其余机器。支持多条漂移合并收编、跨文件打包收编。
  - **恢复（Restore）**：指令该机器丢弃本地改动，回到基线。
  - **忽略（Ignore）**：将该路径加入该机器的忽略清单（局部放行，不再提示）。
- 冲突提示：若两台机器对同一文件各有漂移，收编任一台会覆盖另一台——UI 必须显式警告并展示三方对比（基线 / 机器 1 / 机器 2）。

**自动收编规则**
- 路径级规则：如 `skills/**` 设为「自动收编」——本机新写的 skill 直接成为新版本同步全机队；`settings.json` 设为「必须手动确认」。
- 默认规则：全部手动，用户逐步放开。

### 4.5 凭据与机器变量

**凭据库（Credential Store）**
- 用途：API key、订阅 token 等秘密，**永不进入配置版本**。
- 引用：配置文件内写 `{{cred.<name>}}`；agent 落盘时向 hub 换取真实值并注入；落盘文件权限 0600。
- 存储：hub 侧加密落库（AES-GCM，主密钥来自环境变量或密钥文件）；Web UI 只显示凭据名与末四位。
- 轮换：更新凭据值**不产生**配置新版本；hub 通知受影响机器重注入。
- 引用计数：凭据页显示被哪些配置集/机器引用，防误删。

**机器变量（Variables）**
- 机器级键值对（如 `workspace_root`、`preferred_model`），配置中以 `{{var.<name>}}` 引用；内置变量：`{{machine.name}}`、`{{machine.os}}`。
- 用途：同一配置集覆盖多机时表达小差异，避免为 10% 的差异克隆整个配置集。

### 4.6 用量观测（决策 D1）

**采集（agent 端）**
- 解析本地 Claude Code 会话 JSONL（`~/.claude/projects/**`）：增量读取（记录文件位点）、消息级去重（uuid）、compact/重排容错——管道逻辑移植自 leycode-client sessionlog。
- 首次接入做**历史回灌**（全量扫描既有会话文件）。
- MVP 只上报**聚合数据**（UsagePoint），不上传会话内容本体。

**数据模型**
- UsagePoint：`date × machine × tool × model` → input / output / cache_read / cache_write tokens（+ 可选 reasoning）。
- 成本估算：内置常见模型定价表（可编辑）；标注为「等效估算」，订阅制下仅供横向比较，不承诺与账单对账。

**面板（页面）**
- 总览趋势（7/30/90 天）、按机器堆叠、按模型分布、成本估算开关；机器详情页含本机专属视图。
- 对账基准：与本机 ccusage 的统计口径对齐（同源解析，数字应一致）。

**扩展预留（v1，非 MVP）**
- OTel 通道：agent 内置轻量 OTLP 接收端，Claude Code 原生遥测（活跃时长、工具调用次数等）经 localhost 汇入；所需 env（`CLAUDE_CODE_ENABLE_TELEMETRY` 等）恰好由配置集下发注入——形成自闭环。

### 4.7 订阅余额（决策 E1：全量 Go 化）

**采集器框架（Go 重写）**
- 统一接口：`Collector` = 实例配置（类型、凭据、标签）→ 标准化卡片数据（余额/配额/百分比/重置时间）。
- 三种执行位置：
  - **fetch 型（hub 执行）**：GLM/bigmodel（含国际版）、Kimi、火山方舟、Sub2API/Codex 中转、ZenMux、华为云、AIPing、Antigravity（API 模式）。hub 每 30s 并发刷新，单实例失败不影响其他（沿用 ai-plan-insight 的容错语义：连续失败前两次沿用旧值）。
  - **local 型（agent 执行）**：Claude 订阅（读取本机 OAuth 凭据调用官方 usage 接口）、Cursor、Grok、MiMo、Antigravity（本地模式）。**本地凭据不上传**，agent 本机调用后仅上报结果数据。在哪台机器执行可指定或自动（有凭据者执行）。
  - **push 型（外部推送）**：保留 HTTP 上报端点，**wire 格式兼容 ai-plan-insight 现有 payload**——既是移植期的桥（现有 *-usage-agent 不改一行可继续推），也是长期生态接口（未装 agent 的来源、第三方自写脚本）。
- 模型别名归并（`model_aliases`）与来源存活告警（24h 未上报）照搬原语义。
- 与服务绑定共享凭据：采集实例与 AI 服务配置（4.2）引用的是同一份 `credentials`，且内置平台预设同时携带 `base_url`/模型（喂服务配置）与采集器类型/模式（喂采集器）。M2 做采集器时给采集实例加一个**可选**的 Provider 关联即可，不必回头返工服务配置。

**移植策略（E1 的风险控制）**
- 按「你 config.json 里实际启用的实例」排序移植，先自用后长尾；每移植一家以 ai-plan-insight 现网数据对照验收。
- ai-plan-insight 保持运行直到自用实例全部移植完成（M2 验收），随后退役归档；push 兼容端点长期保留。

**面板（页面）**
- 卡片墙（沿用「服务名 · 标签」卡片心智）+ 30 天趋势；阈值设置（如 7 日窗口 > 80% 标黄）；来源健康（沉默告警）。

### 4.8 Web UI 信息架构

| 页面 | 内容要点 | 里程碑 |
|---|---|---|
| 总览 Dashboard | 机器状态条、今日/7 日 token、订阅卡片精简版、最近事件流 | M2 |
| 机器 Machines | 列表 + 详情（见 4.1） | M0/M1 |
| 配置集 ConfigSets | 列表、编辑器、版本历史与 diff、指派管理 | M1 |
| 收件箱 Inbox | 漂移待处理队列（v1 起并入告警） | M1 |
| 用量 Usage | 趋势/按机器/按模型/成本估算 | M2 |
| 订阅 Subscriptions | 卡片墙、阈值、来源健康 | M2 |
| AI 服务 Providers | 服务配置列表、新建/编辑（预设网格 + 自定义）、被引用计数 | M1.5 |
| 凭据 Credentials | 凭据列表、引用计数（含被 AI 服务配置引用）、轮换 | M1 |
| 设置 Settings | 站点、备份、agent 更新策略、定价表、（v1）通知渠道 | M0 起渐进 |

UI 语言：中/英双语内置（面向国际 self-hosted 社区发布），其余语言社区众包。

---

## 5. Agent 设计（无界面）

### 5.1 职责边界

只做五件事：**连接与心跳、配置落盘与回执、漂移侦测、本地数据采集（JSONL + local 采集器）、自更新**。不开端口对外服务（除可选的 localhost OTLP 接收端，v1）。

### 5.2 本地布局

```
~/.orciny/
├── agent.yml        # hub 地址、机器标识（安装时生成，一般不手改）
├── identity/        # 密钥对与指纹（0600）
├── state.json       # 已应用版本、文件位点、忽略清单（0600）
└── logs/            # JSON lines，保留 14 天，token 一律脱敏
```

### 5.3 CLI 子命令

| 命令 | 作用 |
|---|---|
| `orciny-agent enroll --hub <url> --token <t>` | 注册接入 |
| `orciny-agent status` | 连接状态、已应用版本、待处理漂移 |
| `orciny-agent sync` | 立即拉取并应用 |
| `orciny-agent drift` | 本机漂移清单（含 diff） |
| `orciny-agent pause / resume` | 暂停/恢复配置管理（数据采集不停） |
| `orciny-agent version / update` | 版本与手动更新 |

### 5.4 行为准则

- **宁可不动，不可写坏**：apply 前快照备份（本地保留最近 N 版），失败自动回滚并上报。
- 所有落盘操作幂等；agent 崩溃/断网不影响本机 Coding Agent 正常使用（Orciny 不在关键路径上）。
- 资源目标：常驻内存 < 30MB，空闲 CPU ≈ 0，二进制 < 30MB。

---

## 6. 数据模型（业务级实体）

| 实体 | 关键字段 | 说明 |
|---|---|---|
| machines | name, fingerprint, os/arch, agent_version, tool_versions, last_seen, status | 机器注册表 |
| config_sets | name, manifest, paused, draft_binding, head_provider | 配置集；head_provider 是重注入反查用的冗余字段 |
| revisions | config_set_id, seq, files[], checksum, binding, note, created_at | 不可变版本；binding 冻结绑定**引用**，值不进版本 |
| assignments | machine_id, config_set_id, applied_revision, applied_at, state | 一机一配置集（MVP） |
| drift_events | machine_id, path, kind, diff, state(open/adopted/restored/ignored), binding_drift, binding_url | 漂移收件箱；绑定漂移禁用收编 |
| providers | name, preset, base_url, auth_field, credential, models[], defaults | AI 服务配置（M1.5）；key 不另存，引用 credentials |
| credentials | name, cipher_value, last4, updated_at | 加密凭据；被 providers 引用时不可删除 |
| variables | machine_id, key, value | 机器变量 |
| usage_points | date, machine_id, tool, model, tokens{4类}, source | 用量聚合点 |
| collector_instances | type, mode(fetch/local/push), label, config, machine_id? | 订阅采集实例 |
| sub_snapshots | instance_id, payload, fetched_at | 卡片快照（含历史，供趋势） |
| events | actor, kind, target, detail, at | 操作与系统事件流（轻量，非合规审计） |
| alerts (v1) | rule, target, state, notified_at | 告警 |

存储形态：PocketBase collections；单库 SQLite；设计容量上限 50 台机器 / 千万级 usage_points（超出即非目标场景）。

---

## 7. 协议与 leycode 对齐（决策 A1）

### 7.1 对齐原则

- **概念与语义对齐，wire 不强求一致**：状态机（draft → publish → apply → ack → drift）、幂等语义、checksum 口径与 leycode 相同；传输载体（WS vs 长轮询）与字段细节允许不同，命名统一 camelCase。
- 目的：保留远期 A3（Orciny 内核 + leycode 企业外壳）与数据互通的可能性；两边开发时心智可复用。

### 7.2 概念映射表

| leycode | Orciny | 备注 |
|---|---|---|
| Profile（base/group 两层） | ConfigSet（单层） | Orciny 用变量+凭据表达差异，暂无层级 |
| Revision + checksum + 发布物化 | Revision + checksum | 语义一致 |
| agent_device_bindings（多绑定） | Assignment（一机一集） | Orciny 有意简化 |
| distribution_enabled 下发开关 | ConfigSet「暂停下发」 | 语义一致 |
| 注册码 + 设备指纹 | 一次性 token + 密钥指纹 | 语义一致 |
| apply 回执 / 审计留痕 | apply 回执 / events 事件流 | Orciny 弱化为事件流 |
| 凭据下发注入（newapiToken） | `{{cred.*}}` 注入 | Orciny 泛化为通用占位符 |
| AgentAdapter（Claude/OpenCode） | Tool adapter（MVP 仅 Claude） | 接口形状对齐，v2 引入多工具 |
| 会话日志（块八） | v2 会话检索（可选上传） | 上传协议届时对齐块八 append/replace 语义 |
| 漂移告警（纠正视角） | 漂移收件箱 + 收编（创作视角） | **有意不同**：个人版新增「收编」动作 |

### 7.3 主要交互（业务级消息）

| 方向 | 消息 | 语义 |
|---|---|---|
| agent → hub | hello / heartbeat | 上线、心跳与基本信息 |
| hub → agent | config.notify | 有新版本（推送信号） |
| agent → hub | config.pull | 拉取指派快照（含凭据换取） |
| agent → hub | apply.ack | 应用结果（成功/失败/部分，幂等可重放） |
| agent → hub | drift.report | 漂移摘要（批量） |
| hub → agent | drift.restore | 指令恢复基线 |
| agent → hub | usage.batch | UsagePoint 批量上报（携带位点游标，断点续传） |
| agent → hub | collector.report | local 采集结果 |
| hub → agent | agent.update | 版本更新通知 |
| 外部 → hub | push API（HTTP） | 兼容 ai-plan-insight payload 的订阅/用量推送 |

---

## 8. 安全与隐私

### 8.1 威胁模型（简表）

| 威胁 | 对策 |
|---|---|
| 注册 token 泄露 | 一次性 + 15 分钟 TTL + 使用即焚；Web 可撤销未用 token |
| agent 身份冒充 | 密钥对指纹绑定；吊销即断 |
| hub 数据库泄露 | 凭据 AES-GCM 加密，主密钥不在库中；会话历史等敏感内容根本不采集（MVP） |
| 传输窃听 | 反向代理 TLS；文档默认给出 HTTPS 配置，HTTP 仅限内网明示 |
| agent 写坏本机配置 | manifest 白名单 + 路径安全校验 + apply 前快照 + 失败回滚 |
| 凭据在下游泄露 | 落盘 0600；日志全链路脱敏；Web 只显末四位 |

### 8.2 隐私立场（写进 README 的承诺）

- **数据不出你的 hub**：无任何厂商遥测、无 phone-home；版本检查可关。
- **最小采集**：会话内容 MVP 不上传（仅聚合数字）；v2 会话检索为显式 opt-in，且支持路径级排除。
- **本地凭据不动**：`.credentials.json` 等 OAuth 登录态永不上传，local 采集器只在本机使用。
- 开源发布时附《安全模型》文档（本节展开）。

---

## 9. 非功能要求

| 维度 | 目标 |
|---|---|
| 资源 | hub 常驻 < 64MB RAM；agent < 30MB RAM、空闲 CPU ≈ 0 |
| 体积 | hub / agent 单二进制各 < 30MB；docker 镜像 < 40MB |
| 规模 | 1–50 台机器为设计区间；SQLite 单库 |
| 可靠性 | agent 离线不影响本机工具使用；hub 宕机仅失去面板与下发，机器照常工作 |
| 升级 | hub 原地升级（PocketBase 迁移自动跑）；agent 由 hub 驱动自更新；升级零数据丢失 |
| 备份 | PocketBase 备份（本地/S3），恢复演练写进文档 |
| 平台 | hub：linux/amd64+arm64（docker 优先）；agent：linux/amd64+arm64、darwin/amd64+arm64 |
| i18n | Web UI 中/英内置 |
| 无障碍/主题 | 深浅主题；键盘可达 |

---

## 10. MVP 范围与里程碑

MVP = 四件事：**机器清单与在线状态 · 配置闭环（导入/下发/漂移收编）· Token 用量聚合 · 订阅余额面板**。

| 里程碑 | 内容 | 验收标准（对作者本人） |
|---|---|---|
| **M0 · 骨架** | hub 可跑（docker+二进制）、enroll、WS 心跳、机器列表与在线状态、设置页雏形 | ≥3 台真机接入，杀 agent/断网后状态与重连表现正确 |
| **M1 · 配置闭环** | 导入向导（含敏感项抽取）、编辑器、发布/diff/回滚、下发落盘回执、漂移收件箱（收编/恢复/忽略）、凭据库与变量、快照回滚 | 主力机导入成配置集；全机队对齐；在任一机器改 CLAUDE.md/新增 skill 能被收编并同步到其余机器；apply 失败可自动回滚 |
| **M2 · 数据面** | JSONL 解析上报+历史回灌、用量页；采集器框架 + 自用 provider 全部移植 + push 兼容端点；订阅卡片页 | 用量数字与本机 ccusage 一致；ai-plan-insight 面板可下线（自用实例 100% 移植并对照验收） |
| **M3 · 毕业与发布** | 全机队迁移完毕、旧方案下线；安装体验打磨（脚本/brew/docker）；双语 README、安全模型文档、截图与 demo | 连续 30 天全机队由 Orciny 管理且无需手工 ssh 改配置 → 公开发布 |

里程碑按序不按期；每个里程碑末尾以真实机队做验收。

---

## 11. 版本路线图

| 版本 | 主题 | 内容 |
|---|---|---|
| v0（MVP） | 自用毕业 | 上表 M0–M3 |
| v1 | 告警与遥测 | 通知渠道（ntfy / Telegram / Bark / SMTP / Webhook）+ 规则（机器离线、apply 失败、订阅阈值、来源沉默、agent 版本过旧）；OTel 接收端（D2）；收件箱并入告警 |
| v2 | 检索与多工具 | 会话日志可选上传 + 跨机器全文检索（对齐 leycode 块八协议）；Tool adapter 引入 OpenCode / Codex；配置集叠加层（base + overlay）；微团队（邀请成员、只读/可编辑两档、共享 skills 库） |
| v3 | 生态与互通 | leycode 互通/迁移通道（评估 A3 启动）；Windows 原生 agent 评估；插件市场对接 |

---

## 12. 开源与社区计划（决策 G2）

- **许可**：MIT。仓库：monorepo（hub + agent + install 脚本 + docs）。
- **发布渠道**：GitHub Releases（二进制）、Docker Hub / GHCR（镜像）、Homebrew tap（agent，后续）。
- **README**：英文主文 + 中文对照；核心截图/动图；「beszel for coding agents」一句话讲清形态。
- **宣发点**（M3 后）：r/selfhosted、Hacker News（Show HN）、V2EX / 少数派 / 阮一峰周刊自荐；中文独占卖点（GLM/Kimi/火山订阅聚合）单独成文。
- **社区边界**：不承诺 SLA；Collector 适配、i18n、平台移植标记为 good-first-issue 引导共建。
- **商业让渡**：页脚一行「Need teams & governance? → leycode」，是 Orciny 全部的商业动作。

---

## 13. 成功指标

| 类别 | 指标 |
|---|---|
| 自用（北极星前置） | 连续 30 天：全部机器由 Orciny 管理、零手工 ssh 改配置；ai-plan-insight 与所有同步脚本下线 |
| 产品健康 | 新机接入 ≤ 2 分钟；收编操作从发现到全机队同步 ≤ 1 分钟；升级零数据丢失 |
| 社区（发布后 12 个月，参照 beszel 曲线保守取值） | ≥ 2,000 star；≥ 10 名外部贡献者；≥ 5 个社区贡献的 Collector；安装类 issue 占比持续下降 |

---

## 14. 风险登记册

| # | 风险 | 等级 | 对策 |
|---|---|---|---|
| R1 | Anthropic 上线官方配置同步 | 中 | 重心可滑向观测+订阅+多工具；自托管隐私立场恒在；官方只会同步自家 |
| R2 | E1 一次性重写量大、战线拉长 | 中高 | 按自用实例排序移植；push 兼容端点让旧 agent 在过渡期继续供数；M2 验收未过则 ai-plan-insight 不下线 |
| R3 | JSONL / settings schema 随版本漂移 | 中 | 解析层独立成包 + 契约测试 + 版本探测；社区共摊（ccusage 生态先例） |
| R4 | PocketBase v0.x API 变动 | 低中 | 锁定版本、薄封装其 API；业务逻辑不渗入框架层 |
| R5 | 三线作战精力不足 | 高 | MVP 卡死四件事；M2 完成即净减一个在维护项目（ai-plan-insight）；leycode 客户端收尾与 Orciny M0 错峰排期 |
| R6 | 凭据集中保管的安全事故 | 影响高/概率低 | §8 全套；开源前过一遍外部 review；安全文档随首发 |
| R7 | 做着做着长出编排功能 | 中 | §1.5 非目标清单为红线；任何「远程开会话」类需求一律指向相邻项目 |

---

## 15. 附录

### 附录 A · 命名与品牌

- **Orciny**（/ˈɔːr.sɪ.ni/）：China Miéville《The City & The City》中传说存在于双城缝隙间的隐形第三城，据说在暗中治理两座可见之城——「一个不可见的层，治理你所有可见的机器」。与 beszel（同书城市 Besźel）同宇宙致敬；六字母、无 GitHub/npm 重名（2026-07 查证）。
- 命名体系：hub 二进制 `orciny`，agent 二进制 `orciny-agent`，agent 本地目录 `~/.orciny/`。
- 彩蛋保留字：`unsee`（书中「视而不见」术语）——留给数据脱敏/隐私开关类特性。
- 备选（弃用记录）：Uqbar（区块链重名）、Armada（常用词）、Erewhon（发音不直观）、Breach（安全语境负联想，一票否决）。

### 附录 B · 与构思报告的差异记录

| 项 | 报告推荐 | 拍板 | 影响与处理 |
|---|---|---|---|
| 决策 E | E3 协议先行、分阶段吸收 | **E1 一次性 Go 重写** | 移植总量前移。处理：① Collector 按自用实例优先排序；② push 兼容端点仍然实现（它同时是 ai-plan-insight 的产品功能与生态接口，不因 E1 取消），客观上保留了过渡期桥梁；③ M2 验收门槛「自用实例 100% 移植 + 现网对照一致」不过则 Python 不下线——把 E1 的风险封在 M2 内部 |
| 决策 D | D1+D3 进 MVP | **仅勾选 D1** | D3（订阅采集）实质随 E1 并入 MVP（§4.7），与拍板不冲突；OTel（D2）维持 v1 |

### 附录 C · 参考资料

- 《Orciny 项目构思报告》（2026-07-21）：需求验证、竞品格局、选型论证与来源链接。
- leycode-hub `docs/IMPLEMENTATION-STATUS.md`：可复用协议与实现台账。
- leycode-client `README.md`：daemon 管道（applier / sessionlog / poller）。
- ai-plan-insight `README.md`：Collector 类型、payload 协议与容错语义。
- beszel（henrygd/beszel）：连接模型、agent 自更新、部署形态参照。
