# Orciny M1.5 · 服务绑定 —— 工程设计

| 文档信息 | |
|---|---|
| 版本 | v1（待实现） |
| 日期 | 2026-08-21 |
| 层次 | 工程设计（模块边界、协议、数据模型、测试策略） |
| 上位文档 | [产品设计文档 v0.1](../../PRODUCT-DESIGN.md) |
| 前置文档 | [M1 · 配置闭环](2026-07-31-m1-config-loop-design.md) 及其[验收记录](../plans/2026-07-31-m1-config-loop/acceptance.md) |
| 覆盖范围 | 里程碑 M1.5。M2 的用量与订阅另立 spec |

本文档只描述 M1.5。凡占位符语法、渲染/还原的两档承诺、下发链路、checksum 口径、apply 失败语义，一律以 M1 spec 为准，此处只描述**增量与改动**。

---

## 1. M1.5 的目标与边界

### 1.1 要解决的问题

M1 结束后，换一家 API 供应商是这样的：打开配置集 → 找到 `settings.json` → 手改 `ANTHROPIC_BASE_URL` → 手改四个模型变量 → 确认凭据引用还对 → 发布。每个配置集来一遍。

这是这类工具**最高频的操作**——cc-switch 整个产品就是为它存在的——而 M1 把它做成了最笨的那种。

M1.5 把「用哪家的哪个模型」从配置文件文本里提出来，变成一等实体：**AI 服务配置（Provider）**。配置集绑定一个 Provider + 模型，`settings.json` 里落占位符，真实值在 agent 落盘时注入。

### 1.2 交付物

- `providers` 实体：平台 / base_url / 凭据引用 / 模型清单 / 四槽默认值
- 内置预设表（编译进二进制）：选平台即带出 base_url、模型列表、鉴权字段名
- 配置集的**服务绑定**：`{provider, models{main,opus,sonnet,haiku}}`，冻结进 Revision
- 占位符新前缀 `{{provider.*}}`，六个内置名
- 改 Provider（换 base_url、轮换 key、加模型）→ 重注入全机队，**不产生新 Revision**
- 绑定漂移的识别与反查：机器上手改了 base_url → 收件箱认出是哪家 → 一键改绑定或新建服务配置
- 三条新发布校验

### 1.3 明确不做

| 事项 | 推迟理由 |
|---|---|
| 在线探测 `/v1/models` | 要 hub 主动外拨 + 各家不统一的 endpoint 与鉴权 + 缓存失效。M2 的 fetch 型采集器要建的正是这套出口层（产品 §4.7），到时候复用比现在单独造一遍划算。内置预设表已能消灭绝大多数手抄错误 |
| 订阅余额采集 | 见 §9。本期只**预留接缝**，不建 `collector_instances`、不查余额、不碰订阅页 |
| 一个配置集绑多个 Provider / 故障转移队列 | cc-switch 有 `inFailoverQueue`，但那是单机桌面端的心智。机队场景下"某台机器自动切备用供应商"会让配置状态不可预测，与"中台为准"冲突。真需要时再立 spec |
| 可编辑的预设表 | 见 §2.3 |
| agent 自更新 | 仍按原计划留在 M2。本期是第一个真正需要手工升级 agent 的功能，代价见 §10 |

### 1.4 为什么值得单独一个里程碑

它改协议、改 agent 渲染、改 Revision 结构——全是 M1 的地盘，跟 M2 的用量/订阅**没有代码交集**。混进 M2 会让两条独立的风险绑在一次验收上。

---

## 2. 数据模型

### 2.1 `providers`（新 collection）

| 字段 | 类型 | 说明 |
|---|---|---|
| `name` | text, unique, ≤200 | 显示名，如「智谱 GLM · 个人」 |
| `preset` | text, ≤64 | 内置预设 id（`zhipu`）。自定义留空 |
| `base_url` | text, required | 归一化后存储（§6.2） |
| `auth_field` | select | `ANTHROPIC_AUTH_TOKEN` / `ANTHROPIC_API_KEY` |
| `credential` | relation → `credentials`, required | key 本身仍在凭据库，这里只存引用 |
| `models` | json | 模型 id 候选列表，建时从预设复制，可增删 |
| `defaults` | json | `{main, opus, sonnet, haiku}`，四空 = 透传模式 |
| `note` | text, ≤2000 | |
| `created` / `updated` | autodate | |

API rule 一律 nil，仅 superuser 可访问，与 M0/M1 一致。

**key 不另存**。服务配置里的 API key 就是一条 credential——复用加密落库、末四位显示、`VerifyAll` 启动自检、引用计数防误删。不另起炉灶。

**`defaults` 从预设复制而非运行时 join。** 预设升级不会在背后改动已有 Provider（用户建的东西不该因为 hub 升级而变），也不需要每次渲染都查表。

### 2.2 绑定存在哪 —— 与凭据严格同构的划分

这是本设计的核心不变量，写在最前面：

| 动作 | 是否产生新 Revision |
|---|---|
| **换绑定**（配置集从智谱切到 Kimi、换模型） | **是** |
| **改 Provider 内部**（base_url、追加模型、轮换 key） | **否**，走重注入 |

理由与凭据完全一致（M1 spec §6.5）：**引用**进 Revision，**值**不进。绑定决定了这个版本渲染出什么，历史版本必须能解释自己；而 Provider 的当前值是活的，跟版本无关。

对应到表：

- `revisions` 新增 `binding` (json, nullable) = `{"provider": "<id>", "models": {"main": "...", "opus": "...", "sonnet": "...", "haiku": "..."}}`
- `config_sets` 新增 `draft_binding` (json, nullable)
- `config_sets` 新增 `head_provider` (relation → providers, nullable)，随 `head` 更新而更新
- `revisions.refs` 与 `config_sets.draft_refs` 的 `refsField` 从 `{creds, vars}` 扩成 `{creds, vars, provider_keys}`

**`provider_keys` 是必须的，不是可选优化。** §5.1 要"只下发被引用到的键"，hub 必须在**不读 blob 内容**的前提下知道 `{{provider.model_opus}}` 有没有被用到——这正是 M1 立 `refs` 字段的初衷（spec §6.5：不必全库扫描 blob 内容）。发布时与 `creds` / `vars` 一道扫出来存进去。

注意它与 `head_provider` 是两件事：`provider_keys` 记的是**哪几个内置名被引用**（值形如 `["base_url","auth_token","model"]`），`head_provider` 记的是**绑定指向哪条 Provider**。前者服务于下发裁剪，后者服务于重注入反查。

`head_provider` 是**冗余字段**，唯一目的是让"改了 Provider X，谁要重注入"变成一次索引查询，而不是 JSON 字段扫描 + 三次 join。这个冗余我认为值得；它的一致性维护点只有一处（发布时与 `head` 同写）。

**单数，不是数组。** 多绑定的位置留给以后的迁移，现在不预留结构——一个只可能有一个元素的 map 会诱使实现去支持它。

### 2.3 预设表：编译进二进制，只读

```go
// hub/internal/providers/presets.go
type Preset struct {
    ID         string      // "zhipu"
    Name       string      // "Zhipu GLM"
    BaseURL    string      // "https://open.bigmodel.cn/api/anthropic"
    AuthField  string      // "ANTHROPIC_AUTH_TOKEN"
    Models     []string    // ["glm-5.1", "glm-4.7", ...]
    Defaults   ModelSlots  // 四槽推荐值；零值 = 透传
    WebsiteURL string
    APIKeyURL  string
    Icon       string
    IconColor  string

    // —— 订阅域预留（§9），本期只填数据不消费 ——
    CollectorType string // "zhipu" | "kimi" | "volcengine" | ... | "" 表示无余额接口
    CollectorMode string // "fetch" | "local" | ""
}
```

**为什么不入库。** 可编辑的预设表会立刻撞上"hub 升级时内置更新 vs 用户改动怎么合并"这个经典难题——三方合并、冲突 UI、用户改了又想要新版的默认值。而它换来的唯一能力（自定义平台）用「建一条 `preset` 为空的 Provider」就完全覆盖了。一个难题被 YAGNI 掉，代价是零。

**种子数据的取材依据。** 对 cc-switch 的 69 个 Claude 预设做了统计：

- 69 个都设 `ANTHROPIC_BASE_URL`
- 65 个用 `ANTHROPIC_AUTH_TOKEN`，9 个用 `ANTHROPIC_API_KEY`（有重叠，即少数预设两个都设）
- **34 个**设模型变量，且是 `ANTHROPIC_MODEL` + `DEFAULT_OPUS` + `DEFAULT_SONNET` + `DEFAULT_HAIKU` **四个一起设**（各 34 次，完全同现）；其余 35 个一个模型变量都不设

规律：**中转官方 Claude 的透传，不碰模型；有自家模型的必须四槽全钉死**。原因是 Claude Code 会自己去要 haiku 做标题生成一类的轻量活，只钉主模型会让它拿 `claude-haiku-*` 去打人家的 endpoint。这是本设计采用四槽而非单槽的**实证依据**，不是猜测。

进一步：那 34 个预设的四个槽填的是**同一个值**（`glm-5.1` ×4、`ark-code-latest` ×4）。所以默认交互是「选一个模型 → 四槽同填」，分开设置放进高级（§8.2）。

初始种子覆盖作者自用的平台即可（智谱国内/国际、Kimi、火山方舟、ZenMux、MiniMax，加若干中转），长尾随用随加——它就是一个 Go slice，加一条是一行。

---

## 3. 占位符与协议扩展

### 3.1 新前缀与内置名白名单

```
{{provider.base_url}}
{{provider.auth_token}}
{{provider.model}}
{{provider.model_opus}}
{{provider.model_sonnet}}
{{provider.model_haiku}}
```

`Ref.Name` 是**字段名，不是 Provider 名**。绑定在配置集里唯一，不需要指名。这正好复用 `machine.*` 那套内置名白名单的既有模式（`protocol.MachineKeys`），`parseRef` 加一个对称的 case：

```go
case "provider":
    for _, k := range ProviderKeys { if k == name { return Ref{Kind: RefProvider, Name: name}, nil } }
    return Ref{}, fmt.Errorf("%w: provider.%s 不是内置名（只有 %v）", ErrBadPlaceholder, name, ProviderKeys)
```

### 3.2 为什么是 `{{provider.auth_token}}` 而不是 `{{cred.zhipu_key}}`

这是本节唯一需要论证的选择。

若 `settings.json` 里写死 `{{cred.zhipu_key}}`，那么把绑定从智谱换到 Kimi 时，`base_url` 跟着绑定走了、key 却没跟着走——机器上会用智谱的 key 去打 Kimi 的 endpoint。故障现象是 401，而 401 离"我在 Web 上换了个下拉框"这个现场非常远。

让 key 也走绑定，两者**永远同进同退**，这类故障在结构上不可能发生。

代价：`{{provider.auth_token}}` 在下发和还原时都必须按凭据处理（不落 Revision、Safe 语义），而不能因为它顶着 `provider.` 前缀就当成普通值。这一点在 §4 明确。

### 3.3 `auth_field` 的键名问题 —— 一处不藏的粗糙

`auth_field` 决定的是 env 的**键名**（`ANTHROPIC_AUTH_TOKEN` vs `ANTHROPIC_API_KEY`），占位符替的是**值**，替不了键名。所以换绑到 `auth_field` 不同的 Provider 时，`settings.json` 里那行键名对不上。

**方案：发布校验报错 + 一键修复**（§7 第 3 条）。不做自动改写——那会让用户的文件在背后被动过，违背 M1 立下的"宁可不动，不可写坏"。让用户看见那一行、点一下、知道自己改了什么。

### 3.4 协议改动

`ConfigSnapshot` 追加一个字段：

```go
type ConfigSnapshot struct {
    // ... 0..9 不动
    Provider map[string]string `cbor:"10,keyasint,omitempty"` // 六个内置名 → 真实值
}
```

`omitempty` + 新 keyasint 键，且解码器未开 `ExtraDecErrorNone` 之外的严格模式（`protocol/codec.go` 只设了 `DupMapKey` 与容量上限），因此**旧 agent 收到会静默忽略**该字段——wire 层是兼容的。

但 wire 兼容不等于功能兼容：旧 agent 的 `parseRef` 遇到 `provider` 前缀会返回 `ErrBadPlaceholder` → 渲染失败 → apply 失败 → 走 M1 已有的失败回执 + 自动回滚。这**已经是安全行为**（不会落坏配置），但错误信息烂得没法归因。版本门槛见 §10。

---

## 4. 渲染、还原与漂移基线

### 4.1 渲染：agent 侧，几乎不用改代码

`render.Render` 的签名是 `func(content []byte, look func(protocol.Ref) (string, bool))`——lookup 已经是通用的。agent 只需要在构造 `look` 的地方多接一张 provider 值表。**`render.go` 本身一行不动。**

这是选择"agent 侧展开"的一个额外收益。另外两条路都是假选项：

- *hub 侧展开*：`protocol.Checksum` 的口径是对 blob 内容哈希的，hub 一展开 blob 就变了、checksum 就变了，等于物化出一份新内容——偷偷违背了 §2.2 的核心不变量。
- *发布时烘焙进 Revision*：直接退化成"一次性生成脚手架"，改 Provider 要挨个配置集重发布，正是 M1.5 要消灭的东西。

### 4.2 还原：六个值分两档喂进现成机制

M1 spec §6.4 定了两档承诺：凭据必须还原（失败即 `Truncated`，不上报内容），变量尽力而为（失败标 `RestorePartial`，受 `MinVarLen=4` 约束）。provider 的六个值**分派到这两档，不新增第三档**：

| 值 | 档位 | 理由 |
|---|---|---|
| `auth_token` | 凭据桶 | 是秘密，泄露不可逆 |
| `base_url` | 变量桶 | 不是秘密；长度远超 `MinVarLen`，且在 `settings.json` 里通常只出现一次，还原可靠 |
| `model` / `model_opus` / `model_sonnet` / `model_haiku` | 变量桶 | 可能很短（假想 `k2` 只有 2 字符）。`MinVarLen` 会让短 id 自动降级为 `RestorePartial` 让人复核——这正是我们要的行为，不需要额外代码 |

四个槽经常是同一个值（§2.3），还原时按值去重即可，`Restore` 现有的"按值长度降序替换"逻辑天然处理。

### 4.3 漂移基线：不需要改

M1 spec §6.3 的 `state.json` 已经同时记 `blob`（渲染前 hash）与 `rendered`（渲染后 hash），对账只比 `rendered`。所以：

- 改 Provider 的 base_url → 重注入 → agent 重渲染落盘 → 更新 `rendered` → **不产生漂移**

与凭据轮换完全同一条路径，一行新代码都不需要。这条是 M1 设计正确性的红利。

---

## 5. 下发与重注入

### 5.1 快照组装

`configsync` 组装 `ConfigSnapshot` 时，若 `revision.binding` 非空：

1. 按 `binding.provider` 查 `providers`
2. 取 `base_url`、`auth_field`、`credential`（解密）
3. 合并 `binding.models` 四槽
4. 填进 `Provider map[string]string` 六个键

**只发用得到的那些**：与 M1 spec §5.3 的凭据策略一致——按 `revisions.refs.provider_keys`（§2.2）裁剪，没引用 `{{provider.model_opus}}` 就不下发该键。不读 blob 内容。少一个键少一处泄露面，`auth_token` 尤其。

若 `binding` 为空但 Revision 里有 `{{provider.*}}` 引用 → hub 侧拒绝下发并置 assignment 为 `failed`。这种状态本应被发布校验（§7）挡住，此处是纵深防御。

### 5.2 重注入的触发与反查链

改 Provider 的 `base_url` / `models` / `defaults` → 不产生新 Revision，走 `ConfigNotify`（`RevisionID` 留空 = 仅 secrets 变更，M1 已有的语义）。

反查链靠 §2.2 的冗余字段收敛成两跳：

```
providers.id
  → config_sets WHERE head_provider = id      （索引查询）
  → assignments WHERE config_set IN (...)     （已有索引 idx_assignments_set）
  → SendTo(machine)
```

轮换 Provider 的 `credential` 值 → 落在**现有**的凭据轮换通道上，不需要新代码，但有一处必须改：见下。

### 5.3 引用计数必须认得 Provider（否则会误删凭据）

`credentials.Store` 现在的 `ErrInUse` 判定只看 `revisions.refs` 与 `config_sets.draft_refs`。Provider 引用凭据的方式是 relation 字段，不在这两处——**不改的话，一条正在被 Provider 使用的凭据可以被删掉**，然后全机队在下次重注入时拿到空值。

判定加一条：被任何 `providers.credential` 引用的凭据不允许删除。凭据页的"被谁引用"也相应显示 Provider。

这条是本设计里唯一一处**会造成数据面损坏**的遗漏点，实现时必须有测试覆盖（§11）。

---

## 6. 绑定漂移与反查

### 6.1 识别

场景：ssh 上某台机器，把 `ANTHROPIC_BASE_URL` 从智谱改成 Kimi。agent 的 `Restore` 试图把磁盘内容替回占位符，但磁盘上是 Kimi 的 URL、与绑定的 base_url 对不上，替不回去。于是 `DriftItem.Content` 里那一行是字面 URL，而基线里是 `{{provider.base_url}}`。

hub 侧计算 diff 时扫这个模式——**基线侧是 `{{provider.*}}` 占位符、现状侧是字面值**——命中即标记 `binding_drift`。识别逻辑完全在 hub，agent 不需要知道"绑定"这个概念。

### 6.2 「收编」在绑定漂移上禁用

默认收编语义是"把机器现状写进配置集"，作用在绑定漂移上会把占位符拍平成硬编码字面值——**绑定当场死掉，而且是静悄悄地死**。下一次改 Provider 时这个配置集不再跟着走，没有任何提示。

所以绑定漂移的卡片上，「收编」置灰，hover 说明原因，并给出下面的替代动作。「恢复」「忽略」照常可用。

### 6.3 反查三档

hub 拿漂移里的字面 base_url 做归一化匹配（去尾斜杠、host 小写），依次：

| 命中 | 卡片提供的动作 |
|---|---|
| **已有 Provider** | 「这台机器改用了『Kimi 官方』。把配置集的绑定改成它？」→ 改的是**绑定**，产生新 Revision（§2.2），绑定活着，全机队跟着走 |
| **内置预设** | 「识别为 Kimi。新建服务配置？」→ 打开新建向导，预填平台与 base_url；把机器上手写的那个 key 抽成凭据（复用 importer 的敏感项抽取，产品 §4.2）→ 建成后回到上一行的动作 |
| **都不命中** | 只给「恢复」「忽略」，卡片写明"无法识别这个 base_url 属于哪个平台" |

第三档正是"绑定行禁止收编"的兜底行为——**它是前两档的子集而不是竞品**。实现顺序上先做第三档的骨架（禁用收编 + 说明文案），反查作为其上的增量，不返工。

这个交互的价值在于它匹配了真实用法：「我在某台机器上试了个新中转，好用」——没有它，这个发现要手工搬回 Web 重做一遍；有了它，一键变成全机队的决定。这与产品 §4.4 把漂移收编称作"灵魂交互"是同一个立场。

### 6.4 匹配口径

归一化 = 去尾斜杠 + host 小写 + 去默认端口。**先精确匹配，失败再退到 host 匹配**（同一平台的 `/api/anthropic` 与 `/api/coding` 是不同产品线，精确匹配优先能区分开；host 匹配作为模糊提示，UI 上措辞降级为"可能是"）。

不采用 cc-switch 的 `url.contains()` 子串判断——它在 `bigmodel.cn` 这类既有个人版又有团队版、base_url 完全相同的情形下本来就区分不了（cc-switch 自己在 `codingPlanProviders.ts` 的注释里承认了这点，靠显式字段绕过）。我们有 `preset` 字段可以显式记录，不需要靠猜。

---

## 7. 发布校验（新增三条）

发布校验链路 M1 已有（产品 §4.2：JSON 合法性、凭据引用可解析、路径安全）。追加：

| 条件 | 级别 | 文案要点 |
|---|---|---|
| 有 `{{provider.*}}` 引用但 `draft_binding` 为空 | **错误** | 与"未定义凭据引用"同级——引用不可解析，发布出去必然渲染失败 |
| `draft_binding` 非空但全文找不到任何 `{{provider.*}}` | 警告 | 绑了但没用。可能是用户刚绑完还没插 env 片段，不该阻断发布 |
| 绑定 Provider 的 `auth_field` 与 `settings.json` 里实际的 env 键名不符 | **错误** + 一键修复 | §3.3。修复动作是把那一行的键名替换掉，改动在草稿上可见、可撤销 |

---

## 8. Web UI

### 8.1 新页面「AI 服务」

侧边栏位置紧挨「凭据」——它们是同一类东西：**被配置集引用的资源**，不是配置本身。

- 列表：卡片式，显示 图标 / 名称 / 平台 / base_url / 凭据末四位 / 「被 N 个配置集引用」
- 新建：选平台（预设网格 + 「自定义」）→ 自动带出 base_url、模型列表、`auth_field` → 填 key（新建凭据 或 选已有凭据）→ 保存
- 编辑：改 base_url / 增删模型 / 换凭据。保存前明确提示「这会立即重注入到 N 台机器，不产生新版本」——让"不产生新版本"这件事对用户可见，而不是一个隐藏语义

### 8.2 配置集详情页的「服务绑定」区

置于文件树上方：

```
服务绑定  [ 智谱 GLM · 个人  ▾ ]  [ glm-5.1 ▾ ]  [ 高级 ▾ ]  [ 插入 env 片段 ]
```

- 默认只显示主模型下拉，**选定即四槽同填**（§2.3 的实证依据：34 个预设全是这么干的）
- 「高级」展开 opus / sonnet / haiku 三个槽，以及「透传模式」开关（= 四槽清空，对应那 35 个中转预设）
- 「插入 env 片段」把占位符形态的 env 块写进草稿的 `settings.json`。只在首次绑定、且文件里还没有 `{{provider.*}}` 时出现
- 改绑定 → 落进 `draft_binding` → 走正常的草稿/发布流程 → 新 Revision

### 8.3 编辑器提示

`{{provider.*}}` 加入现有的占位符自动补全与未定义引用告警（M1 已有该机制），六个内置名进补全列表。

---

## 9. 与订阅域的接缝（M2 预留）

产品 §4.7 的订阅采集与本期的服务配置**有交集但不同构**：

| | AI 服务配置 | 订阅余额来源 |
|---|---|---|
| 目的 | 下发给机器，让 Claude Code 连上去 | 观测，出卡片 |
| 需要 | 平台 + base_url + 凭据 + 模型 | 平台 + 凭据（仅 fetch 型） |

交集是 fetch 型采集器要的 `(平台, key)` 二元组——与服务配置完全同一份。cc-switch 就是靠这个吃饭的（从 provider 的 base_url 反推 `codingPlanProvider`，用同一把 key 查余额）。

但**三块不重叠，且不可强行合并**：

1. **local 型**（Claude OAuth 订阅、Cursor、Grok、MiMo）：凭据在机器本地、永不上传、要指定哪台机器执行。它不可能是一条"服务配置"
2. **push 型**：hub 手里连凭据都没有
3. **双向孤儿**：有订阅但不当 Claude Code 后端用的平台（华为云、AIPing）；有服务配置但没有余额接口的中转

强行合并成一张表会让它变成 union type——一半字段对一半记录无意义。

**采用的方案：共享凭据 + 可选关联。**

- 两个实体各自保留，但**都指向同一条 `credentials` 记录**。key 只录一次，轮换一次全生效，引用计数如实显示"被 1 个服务配置 + 1 个采集实例引用"
- **预设表是公共接缝**：一个平台条目同时携带 `BaseURL`/`Models`（喂服务配置）和 `CollectorType`/`CollectorMode`（喂采集器）。一张表喂两个子系统
- M2 做采集器时：`collector_instances` 加可选 `provider` relation；建服务配置时若预设的 `CollectorType` 非空，勾选框「同时纳入订阅监控」；订阅卡片反向标注「来自 XX 服务配置」
- local / push 型采集实例完全独立，不受影响

**本期只做一件事**：`Preset` struct 里把 `CollectorType` / `CollectorMode` 两个字段**填上正确的值**。不消费、不建表、不查余额。

选这条路的决定性理由是**它不阻塞**：服务配置现在就能落地，M2 真做采集器时只是给一张新表加一个 relation 字段，不用回头返工服务配置。

---

## 10. agent 版本门槛

**不动全局 `MinAgentVersion`。** 它在握手层拦截（`hub/internal/handshake/server.go`），一抬就把所有低版本 agent 挡在门外——包括那些指派的配置集根本没用绑定的机器。惩罚面远大于问题面。

**改为定向检查**：`configsync` 组装快照前，若 `revision.binding` 非空（或 Revision 引用了 `{{provider.*}}`），检查目标机器的 `agent_version`：

- 低于门槛 → 不下发，assignment 置 `failed`，`last_error` 写明「agent 版本过低（v0.1.x < v0.2.0），请升级后重试」，UI 在机器行与配置集页都显示
- 达标 → 正常下发

这比"发下去让它渲染失败再回滚"好：失败回滚是兜底，不是主路径，而且回滚的错误信息（"占位符语法错误"）离真实原因（"agent 老了"）很远。

**已知代价**：agent 自更新仍在 M2（M1 spec §1.2 明确推迟），所以 M1.5 是第一个需要**手工升级 agent** 的功能。考虑到目标场景是 ≤50 台、作者自用 3 台起步，可以接受。不为了这个功能把自更新提前——那会让 M1.5 的验收面翻倍。

---

## 11. 测试策略

沿用项目现有的 TDD 风格与 `internal/testsupport` 的 rig。

**`protocol`**
- `parseRef` 识别 `provider` 前缀；白名单外的名字被拒绝（与 `machine.*` 的既有测试对称）
- **往返律** `render(restore(x)) == x` 的 property test 扩展到含 provider 值的情形——这是整个占位符机制的正确性支点（M1 spec §6.1）
- `ConfigSnapshot` 加字段后旧编码仍可解出（wire 兼容）

**`agent/internal/render`**
- 六个键的 lookup；任一取不到值即整体失败、不落含字面占位符的文件
- `Restore`：`auth_token` 走凭据档（替不回去 → `Truncated`、不上报内容）；短 model id 触发 `RestorePartial`；四槽同值时按值去重不重复替换

**`hub/internal/providers`（新包）**
- CRUD；`base_url` 归一化；精确匹配优先于 host 匹配
- 预设表种子数据自检：`AuthField` 取值合法、`Defaults` 要么四空要么四满

**`hub/internal/credentials`**
- **被 Provider 引用的凭据不可删除**（§5.3，这是唯一会造成数据面损坏的遗漏点）
- 凭据的"被谁引用"包含 Provider

**`hub/internal/configsets`**
- 三条新发布校验各自的正反用例
- 发布时 `head_provider` 与 `head` 同步更新（冗余字段的一致性）
- 发布时 `refs.provider_keys` 扫得准：只列真正出现的内置名，转义的 `{{{{provider.x}}}}` 不算

**`hub/internal/configsync`**
- **核心不变量**：改 Provider 的 base_url → 触发重注入 → **不产生新 Revision**、Revision 的 checksum 不变
- 只下发被引用到的 provider 键
- 版本门槛：低版本 agent 不收到含绑定的快照，assignment 置 `failed` 且错误信息可归因

**`hub/internal/drift`**
- 绑定漂移的识别（基线占位符 vs 现状字面值）
- 收编在绑定漂移上被禁用
- 反查三档各自的判定

**端到端（`internal/testsupport`）**
- 建 Provider → 绑定 → 发布 → 落盘验证 → 改 Provider 的 base_url → 重注入 → 落盘内容变了而 Revision 没变
- 机器上手改 base_url → 漂移上报 → 反查命中已有 Provider → 改绑定 → 新 Revision → 其余机器跟着对齐

---

## 12. 落地顺序

按"每一步都能独立跑通并验收"切：

1. **协议与渲染**：`RefProvider` + `ProviderKeys` + `ConfigSnapshot.Provider` + agent 侧 lookup 接线 + 往返律测试。此时还没有 UI，用测试驱动
2. **`providers` 包与预设表**：collection、CRUD、种子数据、凭据引用计数修补（§5.3）
3. **绑定与发布**：`binding` / `draft_binding` / `head_provider` / `refs.provider_keys`、三条发布校验、快照组装
4. **重注入与版本门槛**：反查链、`ConfigNotify`、定向版本检查
5. **Web UI**：AI 服务页、配置集的绑定区、编辑器补全
6. **绑定漂移**：识别 + 禁用收编 + 说明文案（第三档骨架）
7. **反查增量**：前两档（已有 Provider / 内置预设）

1–4 是数据面，可以在没有任何 UI 的情况下用端到端测试验收。6 的骨架先于 7 落地，7 是纯增量。

---

## 13. 风险与已知粗糙处

| 风险 | 应对 |
|---|---|
| **`auth_field` 键名换不了**（§3.3） | 发布校验报错 + 一键修复。不自动改写用户文件。这是有意的粗糙，不是遗漏 |
| **凭据误删导致全机队拿到空值**（§5.3） | 引用计数必须认 Provider。列为强制测试项 |
| **`head_provider` 冗余字段不一致** | 唯一写入点在发布路径，测试覆盖。若将来发现第二个写入点，说明设计需要重看 |
| **手工升级 agent**（§10） | 目标场景 ≤50 台，可接受。不为此提前 M2 的自更新 |
| **预设种子数据过时**（模型 id 变了、base_url 变了） | 它是编译期常量，跟 hub 版本走；用户可用自定义 Provider 随时绕过。不做在线更新——那是另一个子系统 |
| **反查误判**（host 匹配把个人版认成团队版） | 精确匹配优先；host 匹配的 UI 措辞降级为"可能是"，且动作仍需用户确认，不自动执行 |

---

## 14. 与上位文档的差异

本 spec 引入了产品设计文档 §4 未描述的实体（`providers`）与页面（AI 服务）。产品文档 §4.2 原本的心智是"配置集里手写 env + 凭据占位符"，M1.5 在其上加了一层抽象。

建议在本里程碑验收后，回填产品设计文档：

- §4.2 增补「服务绑定」小节
- §4.8 信息架构表增加「AI 服务」页，里程碑标 M1.5
- §6 数据模型表增加 `providers` 行
- §4.7 增补一句：订阅采集实例与服务配置共享凭据、可选关联（本 spec §9）
