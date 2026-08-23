# Orciny M1.6 · 双端点服务配置与凭据内联 —— 工程设计

| 文档信息 | |
|---|---|
| 版本 | v1（已实现） |
| 日期 | 2026-08-22 |
| 层次 | 工程设计（模块边界、协议、数据模型、测试策略） |
| 上位文档 | [产品设计文档 v0.1](../../PRODUCT-DESIGN.md) |
| 前置文档 | [M1 · 配置闭环](2026-07-31-m1-config-loop-design.md)、[M1.5 · 服务绑定](2026-08-21-provider-binding-design.md) |
| 覆盖范围 | 里程碑 M1.6。`~/.codex` 纳管与渲染注入另立 spec |
| 实现 | [实现计划](../plans/2026-08-22-provider-endpoints/00-overview.md) · [验收记录](../plans/2026-08-22-provider-endpoints/acceptance.md) |

本文档**改写** M1.5 spec 的 §2.1（数据模型）、§3.1（词法）、§7（发布校验）、§8.1 / §8.2（UI），并**废止** M1 spec 的凭据实体与 `{{cred.*}}` 占位符。凡未提及处，一律以 M1 / M1.5 为准。

---

## 1. M1.6 的目标与边界

### 1.1 要解决的两个问题

**一、provider 只认 Anthropic 协议口。** Codex CLI 走 OpenAI 协议，以后可能适配的 OpenCode 与 Pi 同理。而同一家平台（智谱、Kimi、火山方舟…）两个口都开——火山甚至是同一个域名下 `/api/coding` 与 `/api/v3` 之别。在现模型里这只能建成两条互不相干的 provider 记录：名字、图标、官网、取 key 地址各写一遍，以后 M2 的余额采集器还要再重复一遍。

平台是一个实体，协议口是它的两个面。模型应当这么长。

**二、凭据是一层没人要的中转。** 填一把 key 的实际路径是：去凭据页 → 建一条 → 给它起个名 → 回 AI 服务页 → 在下拉框里找到刚才那个名字。凭据实体提供的全部价值——加密落库、末四位回显、引用计数防误删——provider 自己完全能承担；而「起个名再回来选它」这一步的心智负担，每建一条服务配置都要付一次。

M1.5 spec §2.1 当初的理由是「key 不另存：服务配置里的 API key 就是一条 credential，复用加密落库、末四位显示、启动自检与引用计数防误删」。复用是对的，**把复用做成用户可见的一层实体**是错的。本期把复用降回代码层（`secretbox` 包），把实体收掉。

### 1.2 交付物

- `providers` 记录从「一个端点」变成「一家平台」：内含 `claude` / `openai` 两个端点子结构
- key 直接落在 provider 上（平台级一把，端点可单独覆盖），`credentials` collection 删除
- 占位符词法改为端点限定：`{{provider.claude.*}}` / `{{provider.openai.*}}`；`{{cred.*}}` 废止
- 发布校验新增一条：引用了某端点，但绑定的 provider 没配这个端点
- 导入向导的「抽取」从「抽成凭据」重定向为「抽成 provider 的 key」
- 迁移 `004`（破坏性）

### 1.3 明确不做

| 事项 | 推迟理由 |
|---|---|
| `~/.codex/**` 纳管与渲染注入 | manifest、导入向导、TOML 占位符、`auth.json` 的特殊形态、漂移扫描是一整块活，任何一块单独做都不产生可用价值。本期只把 provider 层建对，让那一块开工时不用回头改数据模型 |
| 配置集绑定 openai 端点 | 没有消费者——受管范围只有 `.claude/**`。绑定（`Binding`）本期恒指 claude 端点，结构不变 |
| openai 端点的 base_url 反查 / 绑定漂移 | 漂移源同样只有 `.claude/**`。给一个不会产生漂移的端点建反查链是纯粹的空转 |
| `{{cred.*}}` 的过渡期与兼容层 | 见 §6.3。破坏性迁移已获确认 |
| 端点级模型清单的在线探测 | 同 M1.5 §1.3，理由未变 |
| 平台级「两个端点共用一个模型清单」 | 两个协议口的模型 id 常常不同（ZenMux 的 `claude-opus-4-7` vs `openai/gpt-5.2` 就是同一条记录里的两种写法）。合并会逼出一个「这个 id 属于哪个口」的字段，比分开存更复杂 |

### 1.4 为什么现在做，而不是接 Codex 时一起做

M1.5 刚落地，`supplemental/docker/pb_data` 是自用测试库（已于 2026-08-22 备份），没有真实用户数据。provider 的形状一旦被真实配置集引用就很难再动——它冻结进 Revision，而 Revision 不可变。

趁引用面还只有一个测试库，把「一家平台两个口」与「凭据内联」一次做对，比以后带兼容层做两次便宜得多。这也是本期敢做破坏性迁移的全部依据；这个窗口在有第一个外部用户之后就关闭了。

---

## 2. 数据模型

### 2.1 `providers`：一条记录 = 一家平台

```
providers
  name            平台接入名，唯一
  preset          空 = 自定义平台
  note
  key_cipher      平台级 key，AES-GCM 密文
  key_last4       末四位，UI 回显用
  claude   JSON   { base_url, auth_field, key_cipher?, key_last4?, models[], defaults{main,opus,sonnet,haiku} }
  openai   JSON   { base_url, auth_field, key_cipher?, key_last4?, models[], default_model }
  created / updated
```

`name` 的唯一索引保留。`idx_providers_credential` 随 relation 一起删除。

### 2.2 「端点配没配」= `base_url` 是否为空

不加 `enabled` 布尔。一个没有 `base_url` 的端点本来就无从使用，两个字段表达同一件事只会产生「`enabled=true` 但 `base_url` 为空」这种需要额外校验、且没有正确处理方式的中间态。

判定函数 `Endpoint.Configured() bool { return e.BaseURL != "" }`，发布校验、UI 置灰、快照组装三处共用同一个判断。

### 2.3 key 的两级取值

取值顺序：端点的 `key_cipher` 非空则用它，否则回落到平台级 `key_cipher`。

校验规则是**「一个配置了 `base_url` 的端点，必须能解出一把 key」**——平台级或端点级，有一个就行。这条在 `providers.validate` 里把关，与「四槽要么全空要么全满」同级。

为什么允许覆盖而不是强制平台级一把：智谱 / 火山 / Kimi 的两个协议口确实共用同一把 key，但中转类平台不一定；而「需要两把 key 就建两条 provider」会把同一家平台重新拆成两条记录，正好抵消掉 §1.1 要解决的问题。

为什么不做成「只有端点级」：那样共用一把 key 的平台要粘贴两遍，轮换时要改两处——这是本期最常见的情形，不该为边缘情形付代价。

### 2.4 两个端点的字段刻意不对称

| | claude 端点 | openai 端点 |
|---|---|---|
| 鉴权字段 | `auth_field`：`ANTHROPIC_AUTH_TOKEN` / `ANTHROPIC_API_KEY` 二选一 | `auth_field`：自由文本，默认 `OPENAI_API_KEY` |
| 模型 | 四槽 `defaults{main,opus,sonnet,haiku}`，要么全空（透传）要么全满 | `models[] + default_model`（单个） |

四模型槽是 Claude Code 特有的概念——它会自己去要 haiku 做标题生成一类的轻量活（M1.5 spec §2.3 的实证依据）。Codex 没有这个机制。强行统一等于给 Codex 编造出它没有的 opus/haiku 概念，然后在 UI 上解释四个槽里有三个没用。

`auth_field` 的二选一枚举同理，是 Claude Code 的 `settings.json` env 键名问题（M1.5 spec §3.3）。openai 侧本期没有消费者，也就没有依据去定枚举；一个默认 `OPENAI_API_KEY` 的文本字段既够用，又不会在接 Codex 时挡路。

### 2.5 `credentials` 包拆成两个

`credentials` 这个包整体消失，里面两样东西必须留下，各自搬家：

| 现在 | 去处 | 内容 |
|---|---|---|
| `hub/internal/credentials/crypto.go` | `hub/internal/secretbox` | AES-GCM `Encrypt`/`Decrypt`、`LoadMasterKey`、`secret.key`、`ORCINY_SECRET_KEY` |
| `hub/internal/credentials/variables.go` | `hub/internal/variables` | 机器变量（`{{var.*}}`，不加密、按机器） |

主密钥文件名与环境变量名**都不变**（`KeyFileName` / `EnvKeyName` 常量随包搬走），存量密文继续解得开——这是 §6 迁移能「直接搬密文、不用解密」的前提。

机器变量当初与凭据同包，理由是「两者共同构成占位符的取值来源」。凭据没了之后这个理由不成立，而机器变量与秘密加密**没有任何共享代码**，留在一起只会让 `secretbox` 这个安全敏感的包混进不相干的东西。

`MinValueLen = 8`（key 长度下限）跟着搬进 `secretbox`。它的理由与安全强度无关，是**还原的可靠性**——一个 6 字符的「密钥」在文件里到处误匹配的风险远大于它的价值（M1 spec §6.4）。provider 的 key 一样要被 agent 还原，这条约束原样适用。

### 2.6 启动自检必须继承

`credentials.Store.VerifyAll`（启动时逐条解密自检，解不开则拒绝启动）改成扫 `providers` 的三处密文字段（平台级 + 两个端点级）。

**这条不能丢。** 它防的是「从备份恢复到新机器时忘了带 `secret.key`」——最可能的翻车场景。宁可开不了机，也不能让用户以为一切正常，然后把空值下发到全机队。错误信息里的「若是从备份恢复，请把原机器的 secret.key 或 ORCINY_SECRET_KEY 一并带过来」原样保留，只把「凭据 %q」换成「服务配置 %q 的 %s 端点」。

### 2.7 删除保护

provider 仍受「被任何配置集的 `head_provider` 或 `draft_binding` 指向时不许删」保护，`Store.BoundBy` 与 `idx_config_sets_head_provider` 都不动。

`credentials` 的引用计数保护（M1.5 spec §5.3、`Store.UsingCredential`）整体消失——key 现在就在 provider 里，删 provider 就是删 key，没有第二个实体可以被误删。

---

## 3. 占位符词法与协议

### 3.1 端点限定名

`protocol.ProviderKeys` 从六个名字变成两组：

```go
ProviderKeys = []string{
    "claude.base_url", "claude.auth_token",
    "claude.model", "claude.model_opus", "claude.model_sonnet", "claude.model_haiku",
    "openai.base_url", "openai.api_key",
    "openai.model",
}
```

写法：`{{provider.claude.base_url}}`、`{{provider.openai.api_key}}`。旧写法 `{{provider.base_url}}` **不保留别名**——本期是破坏性迁移，留一个只在过渡期有意义的别名，代价是它会一直活到有人专门去删它。

顺序即 UI 展示顺序，也是 agent 还原的 `rankOf` 定序依据（M1.5 spec §4.2），不要重排。

### 3.2 为什么是显式前缀，而不是按文件路径推断

被否掉的方案是：占位符仍写 `{{provider.base_url}}`，渲染时看文件属于哪个工具（`.claude/**` → claude 端点，`.codex/**` → openai 端点）。

三条理由：

1. **发布校验表达不出来。** 本期要做的校验是「文件里引用了 claude 端点，绑定的 provider 配了 claude 端点吗」。隐式方案下，hub 必须先有一套「这个路径属于哪个工具」的推断才能报错——而那套推断属于 `~/.codex` 纳管那一整块活，本期不做。显式前缀下这是一行判断。
2. **同一个字符串在不同文件里意思不同**，这是 M1 立占位符词法时就避开的东西：hub 做发布期校验、agent 做渲染与还原，两侧必须逐位一致（M1.5 spec §3.1 的「词法词汇表」）。让语义依赖上下文，就是让两侧一致这件事从「比较字符串」退化成「比较两套推断实现」。
3. **还原会出错。** agent 的 `restore.go` 把 provider 的值分成秘密档与尽力档，秘密档靠 key 名判断（`auth_token`）。两个端点的 key 不同时，同一个 `provider.auth_token` 在不同文件里指向不同的密文——还原时只能靠路径猜，猜错就是把 A 平台的 key 还原成 B 平台的占位符。

### 3.3 `Ref` 的表示与 `validName` 的放宽

`Ref` 结构不变，`Ref.Name` 承载 `"claude.base_url"` 这样的两段名。

`parseRef` 现在按第一个 `.` 切分，`prefix="provider"`、`name="claude.base_url"`，而 `validName` 只允许 `[A-Za-z0-9_-]+`，会拒掉中间的点。改法：**provider 分支不走 `validName`，直接拿整个 name 去 `ProviderKeys` 白名单里查**。

这是收紧不是放宽——白名单本来就比字符集严格。`cred` / `var` 分支的 `validName` 不动（`var` 是用户自定义名，仍需字符集校验）。

`ConfigSnapshot.Provider` 这个 `map[string]string` 的键名跟着变成两段名。agent 侧 `render.Values.Provider` 按 `Ref.Name` 查表，改动透明。

### 3.4 快照裁剪与 agent 还原的分档

裁剪口径不变：只发 `refs.provider_keys` 里出现过的键，少一个键少一处泄露面（M1.5 spec §5.1）。

`configsync.providerValues` 的 `all` 表从六项变成九项，值的来源随端点走：

```
claude.base_url      → prov.claude.base_url
claude.auth_token    → 端点 key 或平台 key（§2.3）
claude.model*        → binding.Models 四槽（不变）
openai.base_url      → prov.openai.base_url
openai.api_key       → 端点 key 或平台 key
openai.model         → prov.openai.default_model
```

注意 `openai.model` 取的是 provider 上的 `default_model`，不是 binding——binding 本期恒指 claude 端点（§1.3）。这是一处刻意的不对称，接 Codex 时会连同「配置集怎么绑 openai 端点」一起重新设计。

agent 侧 `render/restore.go` 的 `authTokenKey` 单个常量变成集合 `{"claude.auth_token", "openai.api_key"}`：**两个都是秘密档**。秘密档的语义是「必须还原，还原不了就拒绝上传」，尽力档是「尽力替回、替不掉也放行」（M1 spec §7）。一把 API key 落进尽力档就等于有机会被明文上传，两个端点在这一点上没有区别。

`render.Values.Creds` 字段与 `secrets.json` 的 `creds` map 随 `{{cred.*}}` 一起删除（§3.5）。

### 3.5 `{{cred.*}}` 的废止面

`protocol.RefCred` 常量与 `parseRef` 的 `cred` 分支删除。连带要拆的：

| 位置 | 改动 |
|---|---|
| `configsets.Refs.Creds` | 删字段。存量 JSON 里多出的 `creds` 键反序列化时自然忽略，不清洗（§6.2） |
| `configsets.Service.collectRefs` | 不再收集 cred 引用 |
| `configsets.Validate` 的 `known map[string]bool` | 参数删除——它原本装的是「已定义的凭据名」 |
| `ProblemUndefinedRef` | 保留。它现在只服务于 `{{var.*}}`（未定义的机器变量仍要报） |
| `configsync` 的 creds 装配 | 删除 `Deps.Creds`，快照不再带 `creds` map |
| `protocol.ConfigSnapshot.Creds` | 删字段 |
| agent `secrets.json` 的 `creds` | 删字段。老 agent 写过的文件里多一个键，反序列化忽略 |
| `importer.Service.Extract` | 重定向，见 §5.5 |
| `drift.Service.ExtractKey` | 重定向，见 §5.5 |
| `routes` 的 `/credentials`、`/credentials/{id}/rotate`、`DELETE /credentials/{id}` | 删除 |
| `hub/internal/site` 的凭据页、store、api 封装 | 删除，见 §5.4 |

`{{machine.*}}` 与 `{{var.*}}` 完全不受影响。

---

## 4. 发布校验

M1.5 spec §7 立了三条，本期改写其中两条、新增一条。

### 4.1 改写：`binding_missing` / `binding_unused`

判断依据从「文件里有没有 `{{provider.*}}`」变成「文件里有没有**任一** `provider.*` 引用」——词法变了，逻辑不变。两条的错误文案里的占位符示例更新为端点限定写法。

### 4.2 改写：`auth_field_mismatch` 只管 claude 端点

`authFieldMismatch` 找的是 `settings.json` 的 `env` 里承载 API key 占位符的键名。字面常量从 `{{provider.auth_token}}` 变成 `{{provider.claude.auth_token}}`，比对目标从 `prov.auth_field` 变成 `prov.claude.auth_field`。

`FixAuthField`（一键修复，改草稿不改用户文件）同步更新，行为语义不变。

openai 端点**不做这条校验**：它的 `auth_field` 是自由文本，没有「二选一选错了」这种可判定的错误形态；而 `.codex/config.toml` 的结构也不是 `settings.json` 的 `env` 对象。接 Codex 时再立。

### 4.3 新增：`endpoint_missing`

```
Kind:   "endpoint_missing"
Detail: 文件 X 引用了 {{provider.openai.*}}，但绑定的服务配置「Y」没有配置 OpenAI 端点。
        到「AI 服务」页给它补上 base_url，或改引用另一侧端点。
```

阻断级，不是警告。这正是 §3.2 说的那行判断：从 `refs.provider_keys` 里取出被引用的端点集合，逐个查绑定 provider 的对应 `Endpoint.Configured()`。

`configsync.providerValues` 里加同样的检查作为纵深防御（与 `ErrBindingMissing` 同一档：本应被发布校验挡住，走到这里说明有路径绕过了它）。新错误 `ErrEndpointMissing`，一并进 `isBindingFault` —— 它属于「绑定相关、需要归因」的那一类，指派置 `failed` 并写明原因，不置 `degraded`（机器上什么都没被改过）。

---

## 5. Web UI

### 5.1 「AI 服务」页

列表项从「一个端点」改成「一家平台两行」：

```
┌────────────────────────────────────────────────────────┐
│ 智谱 GLM                              [zhipu]  ✎  🗑   │
│   Claude   https://open.bigmodel.cn/api/anthropic       │
│            ANTHROPIC_AUTH_TOKEN · ····a1b2 · 4 个模型   │
│   OpenAI   未配置                                        │
│   被 2 个配置集引用                                       │
└────────────────────────────────────────────────────────┘
```

未配置的端点显示为置灰的「未配置」行，而不是整行不显示——用户要能一眼看出「这条还有另一个口没填」，那正是本期新增的可操作项。

「被 N 个配置集引用」的算法不变（`head_provider` 或 `draft_binding` 命中）。

### 5.2 新建 / 编辑对话框

结构从「一列字段」变成「平台信息 + 两个端点分区」：

```
平台      [预设网格：智谱 / Z.ai / Kimi / 火山 / ZenMux / MiniMax / Anthropic / 自定义]
名称      [____________]
API key   [············]  ← 平台级，两个端点默认都用它
备注      [____________]

▸ Claude 端点                                        已配置 ●
    base_url    [____________]
    鉴权字段    [ANTHROPIC_AUTH_TOKEN ▾]
    模型清单    [tag 输入]
    默认四槽    [主模型 ▾]  ▸高级（分开设置 / 透传）
    单独的 key  [············]  ← 留空则用平台级

▸ OpenAI 端点                                        未配置 ○
    base_url    [____________]
    鉴权字段    [OPENAI_API_KEY]
    模型清单    [tag 输入]
    默认模型    [▾]
    单独的 key  [············]
```

- 选预设时**两个端点一起带出**（预设表同时给两组值，见 §5.6）
- 端点分区默认折叠，`base_url` 非空的展开。右侧的「已配置 / 未配置」是**状态显示，不是开关**——清空 `base_url` 就是取消配置（§2.2）
- 「单独的 key」是次要项，收在端点分区底部并注明「留空则用平台级」——§2.3 说的常见情形只填一次，边缘情形不被堵死
- 编辑态的「保存后立即重注入到 N 个配置集所属的机器，不产生新版本」提示保留（M1.5 spec §8.1）
- 凭据的「选已有 / 新建」双模切换整块删除，换成一个密码框

**key 的回显：** 编辑既有 provider 时密码框显示为空，占位文案是「留空则不修改，填写即替换」，末四位在右侧以 `····a1b2` 灰字展示。密文永不回传前端——这是 M1 就立下的规矩，本期不因为字段搬了家就松口。

### 5.3 配置集详情页的「服务绑定」区

只有一处变化：模型下拉的取值来源从 `provider.models` 变成 `provider.claude.models`，「插入 env 片段」写出的六个键名改用端点限定占位符（`lib/binding.ts` 的 `insertEnvSnippet` 与 `hasProviderRefs`）。

provider 下拉里**没配 claude 端点的记录置灰**，悬停说明「这条服务配置还没有 Claude 端点」。理由与 M1.5 给绑定漂移置灰收编同构（spec §6.2）：让不可能成功的操作在点下去之前就说明原因，而不是点完弹一个发布校验错误。

### 5.4 拆掉凭据页

- `pages/Credentials.tsx`、`stores/credentials.ts`、`lib/api.ts` 的三个凭据函数删除
- `Sidebar.tsx` 的 `credentials` 项与 `KeyRound` 图标删除
- `router.tsx` 的 `'credentials'` 路由与联合类型成员删除
- `MachineDetail.tsx` 的机器变量编辑器（`VariablesEditor`）**保留**——它管的是 `{{var.*}}`，与凭据无关

### 5.5 导入向导与漂移的「抽取」重定向

两处「抽成凭据」的流程合并成同一条：**抽成 provider 的 key**。

`importer.Service.Extract(setID, path, location, credName)` → `Extract(setID, path, location, providerID, endpoint)`：

1. 取 `location` 处的明文值，长度下限校验不变（`secretbox.MinValueLen`）
2. 反查该文件里的 `base_url`（`providers.Store.MatchBaseURL`，M1.5 spec §6.3 的三档反查原样复用），命中则预选那条 provider，未命中则让用户新建
3. 把值写进目标 provider 的对应端点（或平台级）
4. 把文件里**每一处**该值替成该端点的 key 占位符——claude 侧是 `{{provider.claude.auth_token}}`，openai 侧是 `{{provider.openai.api_key}}`（两侧的 key 名不同，见 §3.1）

第 4 步的「每一处」是要紧的：同一个 key 常常同时出现在 env 与某段说明文字里，漏一处就等于没脱敏，而 Revision 不可变，写进去就洗不掉（M1 spec §1.4）。

`drift.Service.ExtractKey` 本来就是喂给 `Rebind` 的，改成写进 provider 后与上面是同一条代码路径，两处合并。

**非 AI 类的 key 没有抽取去处了**——扫描仍然报警告，但用户只能手动处理（改成机器变量、或接受明文）。这是废止 `{{cred.*}}` 的直接代价，记在 §9。

### 5.6 预设表：一条平台，两组端点

```go
type Preset struct {
    ID, Name              string
    Claude, OpenAI        PresetEndpoint  // BaseURL 为空 = 该平台没有这个口
    WebsiteURL, APIKeyURL string
    Icon, IconColor       string
    CollectorType, CollectorMode string   // M2 预留，不动
}
```

七条现有预设的 claude 端点原样搬入。openai 端点按 2026-08 各平台文档逐条核填；核不到的留空（`BaseURL == ""`），UI 上就显示「该平台未提供 OpenAI 端点」，用户仍可手填。

火山方舟那条注释必须跟着搬并且更新——它现在同时描述两个端点了：`/api/coding` 是 Anthropic 协议口，`/api/v3` 是 OpenAI 协议口，用错会走计费不同的通道。

---

## 6. 迁移 `004`（破坏性）

### 6.1 步骤

```
1. providers 加字段：key_cipher / key_last4 / claude(JSON) / openai(JSON)
2. 逐条搬运：
     base_url / auth_field / models / defaults  →  claude 子结构
     credential 指向的那条凭据的 cipher_value   →  key_cipher（直接搬密文）
     该凭据的 last4                             →  key_last4
3. 删 providers.credential relation 与 idx_providers_credential
4. 删旧字段 base_url / auth_field / models / defaults
5. 删 credentials collection
```

第 2 步**直接搬密文、不解密**：同一把主密钥、同一套 AES-GCM，`secretbox` 只是换了包名。这是 §2.5「主密钥文件名与环境变量名都不变」换来的。

### 6.2 存量引用不清洗

- `config_sets.draft_refs` / `revisions.refs` 里的 `creds` 键留在 JSON 里，`Refs` 结构体去掉字段后反序列化自然忽略
- 存量 blob 里的 `{{cred.X}}` 与 `{{provider.base_url}}` 字面量**不改写**。它们下次发布时会被词法拒绝，报「未知前缀 cred」/「provider.base_url 不是内置名」——这是预期行为
- 已发布的 Revision 若引用过它们，下次下发会渲染失败并置 `failed`。测试库可接受

### 6.3 没有 down 迁移

`down004` 直接返回错误。凭据删了之后无处还原——第 2 步是有损的（多条凭据可能被同一条 provider 引用过，反向拆不回去）。写一个假装能回滚的 down 比没有更危险。

---

## 7. 测试策略

沿用 M1 / M1.5 的分层，不新增测试基础设施。

**protocol（纯函数，最高密度）**
- `{{provider.claude.base_url}}` 等九个名字全部 parse 通过
- `{{provider.base_url}}`（旧写法）报「不是内置名」
- `{{cred.X}}` 报「未知前缀」
- `{{provider.claude.temperature}}` 报「不是内置名」
- `Refs` 提取对两个端点混用的内容去重排序正确

**providers store**
- 端点级 key 覆盖平台级；两级都空 + `base_url` 非空 → 校验失败
- 只配 claude 端点的 provider `Configured()` 判定正确
- 四槽半填仍然拒绝（约束继承）
- `MatchBaseURL` 只扫 claude 端点（openai 端点不参与反查）

**configsets 发布校验**
- 引用 `provider.openai.*` + 绑定的 provider 没配 openai 端点 → `endpoint_missing`，阻断
- `auth_field_mismatch` 与 `FixAuthField` 在端点限定占位符下仍然工作
- `Validate` 去掉 `known` 参数后，未定义 `{{var.*}}` 仍报 `undefined_ref`

**configsync 快照**
- 九个键的裁剪：只发 `refs.provider_keys` 出现过的
- 端点缺失 → `ErrEndpointMissing`，指派置 `failed` 且不置 `degraded`
- 改 provider 的任一端点 → 重注入全机队、不产生新 Revision（M1.5 的端到端测试改词法后保留）

**agent render/restore**
- `claude.auth_token` 与 `openai.api_key` **都**落进秘密档
- 其余七个键落进尽力档
- 两个端点的 key 相同时，还原后留下的是 `ProviderKeys` 里靠前的那个

**secretbox 启动自检**
- 库里有 provider、主密钥不匹配 → 拒绝启动，错误信息含「服务配置 X 的 claude 端点」与备份提示

**迁移**
- 在一个含「provider + 被它引用的凭据」的库上跑 `004`，验证密文搬运后仍解得开、旧字段与 collection 消失

**前端（vitest + testing-library）**
- `ProviderDialog`：选预设带出两组端点；端点级 key 留空则不覆盖；编辑态密码框为空且提示「留空则不修改」
- `BindingBar`：没配 claude 端点的 provider 在下拉里置灰
- `insertEnvSnippet` 写出端点限定占位符

**i18n**：`npm run extract` / `npm run compile`（`hub/internal/site`）流程不变，新增文案走 `zh.po` / `en.po`。

---

## 8. 落地顺序

每一步结束时 `go test -tags=testing ./...` 与前端测试都应绿。

1. **`secretbox` / `variables` 拆包** —— 纯搬迁，不改行为。先做这步，后面所有改动才有地方落脚
2. **protocol 词法** —— 九个端点限定名、删 `RefCred`。协议是两侧的公共依赖，先定死
3. **迁移 `004` + `providers` store 双端点** —— 数据模型落地，含两级 key 取值与 `Configured()`
4. **`configsets` 发布校验** —— `endpoint_missing`、`auth_field_mismatch` 改写、删 `known` 参数
5. **`configsync` 快照九键** —— 含 `ErrEndpointMissing` 纵深防御
6. **agent render/restore 两个秘密键** —— 删 `Creds`
7. **删除 credentials 的服务端残留** —— 路由、`Deps.Creds`、启动自检改扫 providers
8. **预设表双端点** —— 七条平台的 openai 口逐条核填
9. **前端** —— AI 服务页与对话框重做、拆凭据页、`BindingBar` 置灰、`insertEnvSnippet`
10. **导入向导与漂移的抽取重定向** —— 两处合并成一条路径
11. **文案 extract / compile + 验收记录**

1–2 与 3–7 之间存在硬依赖，不可并行。8 与 9 可以并行。

---

## 9. 风险与已知粗糙处

| 风险 | 判断 |
|---|---|
| **非 AI 类的明文 key 没有安全去处** | 废止 `{{cred.*}}` 的直接代价。导入向导仍会报「这里有明文密钥」，但只能手动处理。机器变量是不加密的，不能推荐给密钥。若日后真出现这个需求，正确的回答是把秘密做成配置集内的就地字段，而不是把全局凭据实体请回来 |
| **存量 Revision 会渲染失败** | 已知且接受（§6.2）。仅限自用测试库；有外部用户之后这个窗口关闭 |
| **openai 端点本期无人消费** | 它是「记下来的配置」，页面上可建可管但不产生任何注入。UI 文案要说清楚，否则用户会以为填了就生效。对话框的 OpenAI 分区加一行灰字说明 |
| **预设表的 openai base_url 会过时** | 与 claude 侧同样的处境（M1.5 spec §2.3）：编译期常量、跟 hub 版本走，过时了用自定义平台绕过，不做在线更新 |
| **`openai.model` 取 provider 的 `default_model` 而不是 binding** | §3.4 的刻意不对称。接 Codex 时会连同「配置集怎么绑 openai 端点」重新设计，届时这个取值来源大概率要改 |
| **一次改动横跨 protocol / hub / agent / 前端** | 与 M1.5 同样的形状，靠 §8 的顺序把它切成十一个各自可验证的步骤。agent 版本门槛（M1.5 spec §10）已经在位：`MinProviderAgentVersion` 要跟着抬一档，老 agent 认不得端点限定名 |

---

## 10. 与前置文档的差异

| M1 / M1.5 的说法 | 本期改为 | 理由 |
|---|---|---|
| M1 §6：凭据是一等实体，`{{cred.NAME}}` | 废止 | §1.1 第二条 |
| M1.5 §2.1：provider 存 credential 引用 | key 内联在 provider 上 | §1.1 第二条 |
| M1.5 §2.1：一条 provider = 一个端点 | 一条 provider = 一家平台，含两个端点 | §1.1 第一条 |
| M1.5 §3.1：`{{provider.*}}` 六个内置名 | 端点限定，九个内置名 | §3.1 / §3.2 |
| M1.5 §5.3：凭据的引用计数防误删 | 删除（provider 自己的删除保护保留） | §2.7 |
| M1.5 §8.1：AI 服务页一条一个端点 | 一条两行，未配置端点置灰显示 | §5.1 |
| 产品设计 §183 / §222 / §359：`{{cred.<name>}}` | **已回填** | 见 §4.2 编辑器、§4.5「API key 与机器变量」、§10 对照表 |
