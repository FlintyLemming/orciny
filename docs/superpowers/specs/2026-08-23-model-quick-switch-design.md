# Orciny M1.7 · 模型能力归位与一键快切 —— 工程设计

| 文档信息 | |
|---|---|
| 版本 | v1（待实现） |
| 日期 | 2026-08-23 |
| 层次 | 工程设计（数据模型、迁移、交互流程、测试策略） |
| 上位文档 | [产品设计文档 v0.1](../../PRODUCT-DESIGN.md) |
| 前置文档 | [M1.5 · 服务绑定](2026-08-21-provider-binding-design.md)、[M1.6 · 双端点](2026-08-22-provider-endpoints-design.md) |
| 覆盖范围 | 里程碑 M1.7。用量、订阅、Codex 纳管一律不在本期 |

本文档**改写** M1.6 spec 的 §2.4（端点字段的不对称）与 §5.2（provider 编辑对话框），
**改写** M1.5 spec 的 §8.2（配置集详情页的服务绑定区），并给 §7（发布校验）新增一条警告。
凡未提及处，一律以 M1.6 / M1.5 为准。

---

## 1. M1.7 的目标与边界

### 1.1 要解决的问题：最高频的操作走了最长的路

Orciny 最典型的使用场景，是**切换机队上 Claude Code 所用的模型**——换一家更便宜的、
换一个更强的、某家挂了换备用。这件事一天可能做好几次，而配置文件本身几个月不动一次。

但现在做这件事要走全套配置编辑流程：

```
配置集列表 → 点进某个配置集 → 上方 BindingBar 选主模型 → 点「发布」
          → 发布弹窗（跑校验）→ 填/跳过版本说明 → 再点一次「发布」
```

**2 次导航 + 4 次点击 + 1 个弹窗**，而实际变更只有 `draft_binding.models` 一个字段，
文件一个没动。这条路径是按「编辑配置文件」设计的，而高频操作根本不是编辑文件。

### 1.2 挡在前面的数据模型缺陷：能力与声明混在一个字符串里

把快切做成「列表里一个下拉」时，`[1m]` 立刻挡路。

`[1m]` 是 Claude Code 的模型名语法（它按 `/\[1m\]/i` 匹配后把上下文窗口按 `1e6` 计，
见 `presets.go:45`）。M1.6 把它从模型清单里赶了出去，只留在绑定的槽位值上——那一步是对的，
但只做了一半：

| | 是什么 | 该住在哪 | M1.6 之后住在哪 |
|---|---|---|---|
| **能力**（can） | 这个模型支不支持 1M 窗口 | provider 端点的模型清单 | **无处安放** |
| **声明**（want） | 这次绑定要不要开 | binding 的槽位值 | 正确 |

能力这一层缺失，UI 就只能在每次编辑时**问一个它本来可以自己知道答案的问题**。
而在一个「选中即发布」的快切下拉里，这个问题无处可问，只剩三种处理，全是坏的：

- **跟随继承**——从 `glm-5.2[1m]` 切到 `glm-4.7` 自动变成 `glm-4.7[1m]`：给一个可能不支持
  1M 的模型声明了百万窗口，Claude Code 会按 `1e6` 算 auto-compact 阈值。配出来的是一个
  「看着配好了、实际是坏的」状态，且没有任何提示。**最危险的一种。**
- **静默丢弃**——切完 1M 没了，用户不知道。
- **行内再挂一个开关**——不危险，但每次换模型点两下、发两条 revision、下发两次。

三条都是在用交互补数据模型的洞。本期把洞补上：**能力落到模型定义上，声明留在绑定上。**

补上之后快切退化成一个朴素下拉——选 `glm-5.2` 就发布 `glm-5.2[1m]`（它支持，
且这是该模型的标准用法），选 `glm-4.7` 就发布 `glm-4.7`。用户不再被问，
因为系统已经知道。

### 1.3 交付物

- `ClaudeModel{Name, OneM}` 取代 claude 端点的 `models []string`；`Models` 从共享的
  `Endpoint` 结构里搬出，两端各持一份（§2）
- 迁移 `006`：从现有 `claude.defaults` **自动反推**能力，用户无需任何人工标注（§3）
- 配置集列表的行内模型快切：选中即发布，零弹窗（§4）
- 撤销 toast 取代发布确认弹窗（§4.3）
- 四条「绝不静默破坏」护栏（§5）
- 发布校验新增 `one_m_unsupported` 警告（§6）
- provider 编辑对话框的模型清单每行加「支持 1M」勾（§7.1）
- 机器列表只读「模型」列（§7.4）
- 修复 `draftState` 初值缺陷（§5.4）

### 1.4 明确不做

| 事项 | 推迟理由 |
|---|---|
| 在线探测模型能力 | `/v1/models` 不返回上下文窗口能力，各家返回体也不统一。探到的模型一律 `one_m=false`，在编辑页勾一下即可——这是一次性动作，不是高频路径 |
| 除 1M 外的其它模型能力（thinking / effort / vision…） | 没有消费者。ZenMux 预设注释里提到 Claude 系别名「能开 effort 控制」，但当前渲染路径不产出任何 effort 相关的 env，加了也是死字段 |
| openai 端点的模型能力 | `[1m]` 是 Claude Code 的解析语法，Codex 没有这个机制。给 openai 侧编一个 `one_m`，等于重犯 M1.6 §2.4 警告过的「给它编造 opus/haiku 槽」 |
| 机器级模型覆盖 | 配置集是共享的（一个配置集指派给多台机器）。「这台机器单独用别的模型」需要在渲染层引入一层机器级覆盖，是独立的一块活，且当前没有需求 |
| 快切支持四槽分设 | 分设是罕见操作，详情页已经能做。快切遇到分设过的绑定直接让路（§5.2） |
| 给 revision 加 `source="quickswitch"` | 版本历史展示的是 `note`，快切自动写的 note 已经能说明来源。加一个只在历史里显示的枚举值不值得改路由契约 |

---

## 2. 数据模型

### 2.1 `Models` 从共享 `Endpoint` 搬出

```go
// 只留两端共有的三个字段
type Endpoint struct {
    BaseURL   string `json:"base_url"`
    AuthField string `json:"auth_field"`
    KeyLast4  string `json:"key_last4,omitempty"`
}

// ClaudeModel 是 claude 端点模型清单的一项：模型名 + 能力。
//
// OneM 是**能力**（这个模型支不支持 1M 窗口），不是**声明**（这次绑定要不要开）。
// 声明仍然是绑定槽位值上的 `[1m]` 后缀，见 §2.2。
type ClaudeModel struct {
    Name string `json:"name"`
    OneM bool   `json:"one_m,omitempty"`
}

type ClaudeEndpoint struct {
    Endpoint
    Models   []ClaudeModel `json:"models"`
    Defaults ModelSlots    `json:"defaults"`
}

type OpenAIEndpoint struct {
    Endpoint
    Models       []string `json:"models"`
    DefaultModel string   `json:"default_model"`
}
```

**为什么搬出来，而不是在 `ClaudeEndpoint` 里加一个字段。** 技术上：Go 的结构体嵌入
没法覆盖字段，`ClaudeEndpoint` 里再声明一个 `Models` 会与嵌入的 `Endpoint.Models`
同时参与 marshal，产出两个 `models` 键。设计上：这正是 M1.6 §2.4 已经立过的规矩——
两个端点的字段**刻意不对称**，共有的才放进 `Endpoint`。模型清单的形状两端本来就不同
（claude 侧带能力、openai 侧是裸串），它不属于共有部分。

`Configured()` 仍在 `Endpoint` 上，判据不变（`base_url` 是否为空）。

`PresetEndpoint` 同理拆成两个类型，claude 侧的 `Models` 变成 `[]ClaudeModel`。

### 2.2 能力与声明的分工

两者都存在，且不能互相替代：

- **能力**决定 UI 允不允许开、开了要不要报警。它是平台/模型的客观属性，
  与用哪个配置集无关。
- **声明**决定这一版渲染出什么。它必须留在 binding 上并冻结进 Revision——
  历史版本要能解释自己（M1.5 spec §2.2）。若把 1M 改成「渲染时查能力自动加」，
  那么修改 provider 的一个勾就会**改变所有历史版本的语义**，Revision 的不可变性就破了。

因此渲染路径**完全不变**：`configsync/provider.go:93` 仍然原样吐 `b.Models.Main`，
`[1m]` 后缀怎么存的就怎么下发。能力字段从不过协议（§2.4）。

### 2.3 不变式：两处知识必须钉在一起

能力这条知识现在同时出现在两个地方——模型清单的 `one_m` 与 defaults 槽位值的 `[1m]`
后缀。它们必须一致，否则预设自己就是自相矛盾的。M1.6 已有的不变式测试
（`presets_test.go:30`，「Models 里不许带 `[1m]`」）保留，另加一条：

> 预设 claude 端点的 `Defaults` 四槽里，凡带 `[1m]` 的模型基名，
> 必须存在于 `Models` 中，且该项 `one_m == true`。

这条测试是本期最重要的护栏之一：以后有人加预设时漏标能力，测试当场喊，
而不是等到某个用户切了模型才发现 1M 声明莫名其妙消失。

### 2.4 不过协议

模型清单从不进快照。agent 只收 `protocol.ProviderKeys` 那九个**已解析的**键
（`protocol/placeholder.go:49`）。因此本期：

- **不动**协议、不动 CBOR 键、不动 agent、不动快照裁剪
- **不动** `probe.go` 的线上数据形状（`/v1/models` 的返回体仍解成 `[]string`，
  在落库那一步升成 `[]ClaudeModel{Name: m}`，`one_m` 一律 false）

---

## 3. 迁移 `006`

### 3.1 用户记录：从 defaults 自动反推

现有 provider 记录不需要任何人工标注。能力这条知识现在就藏在 defaults 里——
智谱预设的 `Defaults.Main = "glm-5.2[1m]"` 而 `Haiku = "glm-4.7"`（不带），
这恰恰就是「glm-5.2 支持、glm-4.7 不支持」。反推规则：

```
oneMBases := { StripOneM(s) | s ∈ claude.defaults 四槽, HasOneM(s) }
claude.models[i] : string  →  { Name: m, OneM: m ∈ oneMBases }
openai.models             →  原样保留 []string
```

这条规则对存量数据是**保序**的：迁移前能开 1M 的模型，迁移后仍然能开；
迁移前不带 `[1m]` 的，迁移后勾是空的，用户想开自己去勾。没有任何行为发生变化。

`defaults` 字段本身**保留不动**——它是声明（新建绑定时的预填值），不是能力。

### 3.2 预设表：反推打底 + 逐条核对

预设是编译进二进制的字面量，改的是源码而不是数据。反推规则先机械应用一遍打底，
再按各平台文档逐条核对——预设表本来就是这么维护的（`presets.go:173`：
「本表按 2026-08 各平台的 Claude Code 接入文档逐条核过」）。

反推施加到当前 7 条预设的结果：

| 预设 | claude 端点模型 | 反推得到 `one_m=true` 的 |
|---|---|---|
| `zhipu` | glm-5.2 / glm-5-turbo / glm-4.7 | **glm-5.2** |
| `zhipu_intl` | 同上 | 无（defaults 不带 `[1m]`） |
| `kimi` | kimi-k3 / kimi-k2.7-code / …-highspeed / kimi-k2.6 | **kimi-k3** |
| `volcengine` | doubao-seed-code-preview-latest 等 5 个 | 无 |
| `zenmux` | claude-opus-4-7 / claude-sonnet-4-6 / claude-haiku-4-5 / 三个全 id | 无 |
| `minimax` | MiniMax-M2 | 无 |
| `anthropic` | claude-opus-5 / sonnet-5 / fable-5 / haiku-4-5 | 无（defaults 为透传空槽） |

**两处已知的反推盲区**，必须人工确认后再定稿（见 §10 风险 R1）：

- `zhipu_intl` 与 `zhipu` 模型清单完全相同，但前者 defaults 不带 `[1m]`。
  同一个 glm-5.2 经由 z.ai 到底支不支持 1M？若支持，这是 M1.6 建预设时的遗漏。
- `zenmux` 的注释白纸黑字写着「Claude 系用别名（**能开 1M 上下文**与 effort 控制）」，
  但 defaults 没有声明。这条知识只活在注释里，反推看不见。
- `anthropic` 走透传（四槽空），反推无从下手；官方 Sonnet 系是否标 1M 需人工定。

### 3.3 没有 down 迁移

与 `004` / `005` 同（M1.6 spec §6.3）。`006` 是纯粹的形状升级，
反推是单向的（升上去之后 defaults 与 models 各自独立可编辑，降回去会丢用户后来勾的）。

### 3.4 迁移不拆两条

M1.6 的 `004`/`005` 拆分是因为删 `credentials` collection 会让启动自检在中途全红
（见该期实现计划的「偏离一」）。本期没有这个问题：`006` 只重写两个 JSON 字段的内部形状，
没有 collection 增删，中途不存在测试全红的窗口。一条即可。

---

## 4. 快切与发布路径

### 4.1 入口：配置集列表的行内下拉

主入口放在**配置集列表**（`pages/ConfigSets.tsx`）而不是机器列表。理由是拓扑：
一个配置集会被指派给多台机器，把开关放在机器行上会让人以为「只改这一台」。
机器列表拿只读列 + 跳转（§7.4）。

```
生产机组   v12 · 智谱 GLM › glm-5.2 · 1M    [智谱 GLM › glm-5.2 ▾]   影响 3 台
```

下拉按 provider 分 `<optgroup>`，选项是各 provider **claude 端点**的模型（与
`BindingBar` 一致，绑定本期恒指 claude）。没配 claude 端点的 provider 不出现在
下拉里——它本来就绑不上（M1.6 §5.3 已在 `BindingBar` 立过这条，此处一致）。
顶部一个「透传（不指定模型）」项，对应四槽全空。

选中一项时构造的绑定：

```ts
{ provider: p.id, models: fillAllSlots(setOneM(model.name, model.one_m)) }
```

`one_m` 为真就带上声明——这是 §1.2 定下的默认值。想关掉去详情页，那是罕见操作。

### 4.2 一次点击 = 一次发布

选中即触发，无弹窗：

```
setBinding(setId, binding)
  → validateConfigSet(setId)
      ├─ 有阻断项 → 不发布，就地弹现成的 PublishDialog（把问题摊开给用户处理）
      └─ 只有警告或无问题 → publishConfigSet(setId, note)
```

note 自动生成：`快切 · 智谱 GLM › glm-5.2`。版本历史展示的就是 note，
足以说明这一版的来源与内容。

**校验仍然跑，只是不再要求用户读。** 换模型这条路径上，绑定合法性
（provider 存在、claude 端点已配、四槽全满或全空）在下拉构造时就已保证；
真正可能出问题的是配置集里别的东西，那时候弹窗才有意义。摩擦留在出问题的那一次，
不摊派到每一次。

### 4.3 撤销 toast 取代确认弹窗

发布成功后右下角 toast，8 秒：

```
已切到 glm-5.2 · 影响 3 台          [撤销]
```

撤销 = `rollbackConfigSet(setId, 发布前的 head)`，复用现成回滚（它会同时把草稿拉回，
见 `revisions/service.go:129` 的注释）。

**为什么是撤销而不是确认。** 确认弹窗要求用户在**信息最少**的时刻（还没看到结果）
做判断，代价是每一次都要付。撤销把判断挪到**信息最多**的时刻（已经看到切换结果），
代价只在做错时付。对一天多次的操作，后者显著更便宜；而这件事完全可逆——
回滚是既有能力，不是为本期新造的。

新增 `components/Toast.tsx` + `stores/toast.ts`（nanostores atom，与既有 store 风格一致）。
Toast 是全局单例，挂在 `Shell.tsx` 上。

### 4.4 详情页也少一步

详情页点「发布」时，若草稿与 head 的差异**只有 binding**（无文件增删改），
跳过弹窗直接发布 + 同一套撤销 toast。有文件改动仍走完整弹窗——那时版本说明才有意义。

判据复用 §5.4 的 `diffAgainstHead(set)`。

---

## 5. 四条护栏

本期新增的所有自动化都遵守同一条原则：**绝不静默破坏用户已有的配置**。
每一条护栏都是「这种情况下快切让路，把用户送到能看清全貌的地方」。

### 5.1 草稿有其它未发布改动 → 快切禁用

快切会发布**整个草稿**，不只是绑定。若草稿里还躺着别的未发布改动，一次快切会把它们
一并推上去。这种行为无法用一句 toast 说清楚，也不该由一个下拉承担。

该行显示 `有未发布改动 →`，链到详情页。

### 5.2 四槽分设过 → 快切禁用

绑定的四个槽可以指向不同模型（智谱预设就把 `Haiku` 单独指到 glm-4.7）。
快切用 `fillAllSlots` 四槽同填，一点就会把这种分设抹平。

判据：`slots` 去掉 `[1m]` 后四个基名不全相同。该行显示 `已分设 →`，链到详情页。

### 5.3 绑定声明了 1M 但模型标了不支持 → 发布校验警告

见 §6。**不改写、不阻断**——存量数据里可能有合法的组合（比如用户手填了一个不在清单里
的模型名，或平台悄悄上线了 1M 而清单还没更新）。系统说出它看到的疑点，判断权留给用户。

### 5.4 `draftState` 初值缺陷（顺带修复）

`ConfigSetDetail` 的 `draftState` 硬编码 `useState<DraftState>('clean')`
（`pages/ConfigSetDetail.tsx:49`），从不由服务端数据初始化。后果：
**刷新页面后，有未发布草稿的配置集也显示「已对齐 head」，且发布按钮置灰**——
用户必须再随便改一个字符才能重新发布。

护栏 §5.1 判脏本来就需要一个纯函数：

```ts
/** 草稿相对 head 的差异。head 为空（从未发布）时一切都算差异。 */
export function diffAgainstHead(set: ConfigSetRecord): {
  files: boolean      // 文件有增删改
  binding: boolean    // 绑定变了
}
```

比对 `set.draft` 与 `set.expand.head.files`（按 path 排序后逐项比 path/hash/mode），
以及 `set.draft_binding` 与 `set.expand.head.binding`。

`publish` 把草稿逐字冻结进 revision（`revisions/service.go:92`，仅按 path 排序），
`rollback` 也会把草稿拉回旧版，所以「发布后 draft 与 head.files 必然逐项相等」成立，
这个比对不会有假阳性。

同一个函数三处复用：护栏判脏、详情页 `draftState` 初值、§4.4 的「只有 binding 变了」。

---

## 6. 发布校验新增 `one_m_unsupported`

```
Kind:    "one_m_unsupported"
Warning: true
Detail:  绑定的 <槽名> 声明了 1M 上下文，但 <provider 名> 的模型清单里 <模型> 标记为不支持
```

触发条件（三条同时成立）：

1. 草稿有绑定，且槽位值带 `[1m]`
2. 该槽位的模型基名**存在于**绑定 provider 的 claude 端点模型清单中
3. 该项 `one_m == false`

第 2 条是刻意的：模型名不在清单里时**不报**。清单是给 UI 用的候选集合，不是白名单——
用户手填一个清单外的模型是允许的（M1.5 起就没有拦过），此时系统对它的能力一无所知，
沉默比猜测诚实。

放在 `configsets/validate.go`，与既有 `binding_unused` 等警告同级。

---

## 7. Web UI

### 7.1 provider 编辑对话框：模型清单每行加勾

`ProviderDialog.tsx` 的 `ModelList` 子组件（`:761`）现在是一排可删除的 chip。
claude 端点那份改成每行 `模型名 [支持 1M ☐] [×]`；openai 端点那份**不变**（§1.4）。

探测（`:568`）合并回来的模型 `one_m` 一律 false，用户按需勾。

「默认模型」四个下拉的候选（`:152` 的 `claudeModelOptions`）取 `models.map(m => m.name)`，
`stripOneM` 那一步可以去掉了——清单里本来就不会有 `[1m]`（`presets_test.go:30` 的不变式）。

### 7.2 `BindingBar` 的 1M 复选框改成看能力

现在是 `disabled={mainBase === ''}`（`BindingBar.tsx:114`），只在「没选模型」时禁用。
改成：

```
禁用 ⟺ 没选模型 || 当前模型在清单中且 one_m === false
```

禁用时 `title` 说明原因（「该模型未标记支持 1M 上下文，可在 AI 服务页修改」）。
模型不在清单里时**不禁用**——理由同 §6 第 2 条。

选择模型时的 1M 处理也变了：现在是保留复选框当前状态（`:105` 的 `setOneM(e.target.value, oneM)`），
改成按新模型的能力取值。这与快切的默认值一致——两个入口给出同一个结果，
否则同一个操作在两处产生不同的绑定。

分槽下拉（「高级」区）同理。

### 7.3 配置集列表

见 §4.1 / §5.1 / §5.2。新增 `components/ModelQuickSwitch.tsx` 承载下拉 + 发布编排 +
两条禁用态。「影响 N 台」需要按配置集统计指派数——在 `stores/configsets.ts` 加一次
`assignments` 的 `getFullList` 并按 `config_set` 归并（详情页 `:82` 已有单集合版本，
此处是列表版本）。

### 7.4 机器列表只读「模型」列

`pages/Machines.tsx` 表格加一列，取值链路：
`assignment.machine == m.id` → `config_set` → `expand.head.binding.models.main`。
显示 `智谱 GLM · glm-5.2`，点击跳到该配置集。**只读**，理由见 §4.1。

未指派 / 未绑定 / 透传分别显示 `—` / `未绑定` / `透传`。

### 7.5 i18n

新增文案走既有流程：`npm run extract` 后回填 `src/locales/en.po`。
`zh.po` 是源语言，msgid 即中文。

---

## 8. 测试策略

### Go

| 层 | 用例 |
|---|---|
| `providers` | `ClaudeModel` 的 JSON 往返；`one_m` 省略时解出 false |
| `providers`（不变式，§2.3） | 每条预设：defaults 里带 `[1m]` 的基名必须在 Models 中且 `one_m=true`；Models 里不许带 `[1m]`（既有用例保留） |
| `migrations` | 反推：defaults 带 `[1m]` → 对应模型 `one_m=true`；不带 → false；defaults 为空（透传）→ 全 false；openai 侧 `[]string` 原样 |
| `migrations` | 保序：迁移前后同一条 provider 渲染出的九个键逐字相同 |
| `configsets/validate` | `one_m_unsupported`：命中三条件时产出**警告**；模型不在清单时不产出；`one_m=true` 时不产出 |
| `routes/providers` | 请求体/响应体的模型清单形状 |

### 前端

| 文件 | 用例 |
|---|---|
| `lib/draftState.test.ts` | `diffAgainstHead`：仅文件改 / 仅绑定改 / 都改 / 都没改 / head 为空；draft 与 head 顺序不同但内容相同时**不算脏** |
| `components/ModelQuickSwitch.test.tsx` | 选中即调 `setBinding` + `publishConfigSet`，且不渲染发布弹窗；选中支持 1M 的模型时绑定值带 `[1m]`，不支持的不带；校验有阻断项时**不发布**并弹出 `PublishDialog`；草稿脏时禁用并显示「有未发布改动」；四槽分设时禁用并显示「已分设」；撤销按钮调 `rollbackConfigSet` 且传发布前的 head |
| `components/BindingBar.test.tsx` | 补：选中不支持 1M 的模型时复选框禁用且绑定不带 `[1m]`；选中支持的自动带上；模型不在清单里时不禁用 |
| `components/ProviderDialog.test.tsx` | 补：勾「支持 1M」写进 `models[i].one_m`；探测合并回来的模型 `one_m=false` |
| `components/Toast.test.tsx` | 显示、超时消失、点撤销触发回调 |

前端对 `lib/api` 的调用用 `vi.mock` 打桩——它是 HTTP 边界，不是被测行为。
断言落在「调了什么、传了什么参数、渲染成什么」，不断言桩本身。

---

## 9. 落地顺序

每一步结束时 `go test -tags=testing ./...` 与 `npm test` 都应绿。

1. **`ClaudeModel` 类型 + 预设表改写 + 不变式测试**（§2、§3.2）——纯 Go，
   先把「知识钉在一起」的测试立起来，后面所有改动都在它的保护下
2. **迁移 `006` + 反推**（§3.1）
3. **`store` / `routes` 跟进形状**（§2.1）
4. **发布校验 `one_m_unsupported`**（§6）
5. **前端类型 + `ProviderDialog` 的能力勾**（§7.1）——到此能力这条线闭环，可单独验收
6. **`diffAgainstHead` + `draftState` 初值修复**（§5.4）——纯函数先行，快切依赖它
7. **`Toast` + `stores/toast`**（§4.3）
8. **`ModelQuickSwitch` + 配置集列表接入**（§4.1、§4.2、§5.1、§5.2）
9. **`BindingBar` 能力感知 + 详情页少一步**（§7.2、§4.4）
10. **机器列表只读列 + i18n 提取 + 验收**（§7.4、§7.5）

第 1–5 步是「能力归位」，第 6–10 步是「快切」。前者独立可验收，
后者依赖前者——顺序不能颠倒。

---

## 10. 风险与已知粗糙处

| # | 风险 | 处理 |
|---|---|---|
| **R1** | **预设表的能力标注需要外部事实**：反推对存量用户数据是保序无损的，但对预设表是**有损的**——`zhipu_intl` / `zenmux` / `anthropic` 三条的真实 1M 支持情况，反推看不见（§3.2）。标错的后果是用户切到某模型时 1M 声明被漏掉（保守方向，不致坏）或多加（危险方向） | **实现前必须人工确认这三条**。确认不了的一律按反推结果（保守方向）落地，用户可在编辑页自行勾上 |
| **R2** | 快切一次点击就下发到 N 台生产机器，误触代价直接 | 撤销 toast（§4.3）+ 两条禁用护栏（§5.1、§5.2）。回滚是既有能力，不是本期新造 |
| **R3** | 高频快切产生大量 revision，版本历史被稀释 | 接受。revision 是不可变审计记录，「历史太长」不是把它变短的理由。若日后确实碍事，做的是历史视图的折叠，不是少记 |
| **R4** | `diffAgainstHead` 在前端比对，依赖列表接口带上 `expand=head` 的完整 `files` | 列表已经 `expand: 'head'`（`stores/configsets.ts:28`）。`files` 是 hash 清单不是内容，体积可控 |
| **R5** | 「支持 1M 就默认开」这个默认值，对某些按 token 计费的中转会显著改变成本结构 | 这是用户明确选定的默认值（更少点击优先）。1M 只影响 Claude Code 的 auto-compact 阈值与 `/context` 显示，不改变单次请求的计费方式；成本变化来自更少的压缩、更长的上下文，方向可预期。详情页随时可关 |

---

## 11. 与前置文档的差异

| 文档 | 条目 | 本期变更 |
|---|---|---|
| M1.6 spec | §2.4 两个端点的字段刻意不对称 | **强化**：`Models` 从共享 `Endpoint` 搬出，两端形状不同（claude 带能力） |
| M1.6 spec | §5.2 新建/编辑对话框 | **改写**：claude 端点的模型清单每行加「支持 1M」勾 |
| M1.5 spec | §8.2 服务绑定区 | **改写**：1M 复选框从「没选模型才禁用」改成按能力禁用；选模型时 1M 按能力取值而非沿用当前状态 |
| M1.5 spec | §7 发布校验 | **新增**：`one_m_unsupported` 警告 |
| M1.5 spec | §2.3 选一个模型四槽同填 | **不变**，快切沿用同一规则；四槽已分设的绑定快切让路 |
