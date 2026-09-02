# Orciny M1.8 · 本机覆盖层 —— 工程设计

| 文档信息 | |
|---|---|
| 版本 | v1（待实现） |
| 日期 | 2026-09-01 |
| 层次 | 工程设计（数据模型、迁移、合并算法、交互流程、测试策略） |
| 上位文档 | [产品设计文档 v0.1](../../PRODUCT-DESIGN.md) |
| 前置文档 | [M1 · 配置闭环](2026-07-31-m1-config-loop-design.md) |
| 覆盖范围 | 收件箱的「忽略」拆成两个动作、`machine_overrides` 集合、hub 侧合并。用量、订阅、Codex 纳管一律不在本期 |

本文档**改写**产品设计文档 §4.4 的「忽略（Ignore）」条目与 M1 spec §8.4 的忽略语义。
凡未提及处，一律以 M1 spec 为准。

**协议零改动。** `ConfigSnapshot` / `DriftItem` / `DriftCommand` 一个字段都不加，
agent 侧除一处回退外不改代码。这是选定方案的主要收益，§4.4 说明它为什么成立。

---

## 1. 目标与边界

### 1.1 要解决的问题：一个粒度太粗的按钮吞掉了整个文件

用户在收件箱里看到 `.claude/settings.json` 的一条漂移，diff 里只有三行。他点「忽略」，
合理预期是「这三行归我」。实际发生的是：

```
Ignore(eventIDs)  →  ignore_rules{machine, path: ".claude/settings.json"}
                  →  IgnorePaths(machineID) 进每份 ConfigSnapshot
                  →  agent 侧两处同时吃它：
                     watcher/scan.go:161,180,257  不再上报该路径的漂移   ← 想要的
                     applier/plan.go              该路径不进 plan          ← 灾难
```

于是这台机器**永久收不到 `settings.json` 的任何中台更新**。全机队换 provider、加 hook、
改权限，它一概拿不到，而且面板上看不出异常——它的状态是「已对齐」。

这不是实现走偏。产品文档 §4.4 写的就是「将该路径加入该机器的忽略清单（局部放行，不再提示）」，
路径级冻结**是当初的设计原意**。缺的是另一个动作：*保留这几处差异，其余照旧跟随*。
收件箱只有一个按钮，用户的意图被迫走了唯一那条路。

### 1.2 为什么不能靠已有机制凑合

| 已有机制 | 为什么不够 |
|---|---|
| `ModeKeys`（把 `settings.json` 改成只管列出的键） | 粒度是**配置集级**。一个键退出受管就是全机队都不管了，而需求是「别的机器照旧同步这个键，只有这台不一样」。也覆盖不了 `CLAUDE.md` |
| 收编 Adopt | 方向反了。它把本机差异推给全机队 |
| 恢复 Restore | 丢掉本机差异，正是用户不想要的 |
| 机器变量 `{{var.*}}` | 要求差异点在发布前就被预见并写成占位符。事后在某台机器上顺手改出来的差异用不上它 |

### 1.3 交付物

- 收件箱的「忽略」拆成两个语义清晰的动作：**本机保留**（新，主推）与**不再管这个路径**（原「忽略」改名）
- 新集合 `machine_overrides`：一条记录 = 一个差异点，机器级，JSON 按键路径 / 文本按三方合并（§2）
- 迁移 `007`：建集合、给 `drift_events.state` 加 `overridden`（§3）
- 差异点切分与勾选（默认全选，可取消）（§4.1–4.2）
- `hub/internal/overrides`：快照组装期的合并与撞车检测（§4.3–4.5）
- `hub/internal/merge3`：行级三方合并，自研，不引依赖（§5）
- 收件箱与机器详情页的 UI（§6）、四条 API（§7）
- 五条护栏，含把 `plan.go` 的在制品改动**回退**（§8）

### 1.4 明确不做

- **JSON 数组的元素级合并。** 数组整体当叶子。按下标定位的 selector 在数组增删时会指向
  错误的元素——这是静默写坏用户配置的经典路子，收益远不抵风险。
- **覆盖层跨机器复制。** 「把这台的覆盖也给那台」是收编的活，不是覆盖层的。
- **覆盖层自动过期。** 覆盖层只在用户显式撤掉时消失。中台改到同一处时给提醒（§4.5），
  但不替用户做决定。
- **二进制文件的覆盖层。** `drift.isText` 判否的文件只能收编 / 恢复 / 退管。
- **覆盖层跟随磁盘漂移。** 覆盖层里的 `mine` 冻结在排除那一刻。之后又在本机改了同一处，
  会正常变成一条新漂移，收件箱再问一次。见 §8.5。

---

## 2. 数据模型

### 2.1 `machine_overrides`

一条记录 = **一个差异点**，不是一个文件。

```go
overrides := core.NewBaseCollection("machine_overrides")
overrides.Fields.Add(
    &core.RelationField{Name: "machine", Required: true,
        CollectionId: machines.Id, MaxSelect: 1, CascadeDelete: true},
    &core.TextField{Name: "path", Required: true, Max: 1024},
    &core.SelectField{Name: "kind", MaxSelect: 1, Required: true,
        Values: []string{"json_key", "text"}},

    // json_key：sjson 键路径（已转义，见 §4.1）。text 恒为空串。
    &core.TextField{Name: "selector", Max: 512},

    // json_key 的两侧：原始 JSON 片段。空串 = 该键在这一侧不存在。
    &core.TextField{Name: "base_value", Max: 65536},
    &core.TextField{Name: "mine_value", Max: 65536},

    // text 的两侧：全文 blob。
    &core.RelationField{Name: "base_blob", CollectionId: blobs.Id, MaxSelect: 1},
    &core.RelationField{Name: "mine_blob", CollectionId: blobs.Id, MaxSelect: 1},

    // 需要用户看一眼的状态。空 = 一切正常。见 §4.5。
    &core.SelectField{Name: "attention", MaxSelect: 1, Values: []string{
        "hub_changed", "merge_conflict", "path_gone", "unmergeable",
    }},
    &core.TextField{Name: "shadowed_value", Max: 65536},
    &core.RelationField{Name: "shadowed_blob", CollectionId: blobs.Id, MaxSelect: 1},
    &core.RelationField{Name: "shadowed_rev", CollectionId: revs.Id, MaxSelect: 1},

    &core.RelationField{Name: "origin_drift", CollectionId: drifts.Id, MaxSelect: 1},
    &core.TextField{Name: "note", Max: 2000},
    &core.AutodateField{Name: "created", OnCreate: true},
    &core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
)
overrides.AddIndex("idx_overrides_machine_path_sel", true, "machine, path, selector", "")
overrides.AddIndex("idx_overrides_attention", false, "attention", "attention != ''")
```

三个取舍值得记下来。

**勾选状态不单独存。** 用户在「默认全选、可取消勾选」里没勾的差异点，JSON 侧就是不建记录；
文本侧 `mine_blob` 存的是「基线 + 被勾选的那些 hunk」（§4.2），没勾的 hunk 天然不在内容里。
勾选因此塌缩进数据本身，不需要第二份真相。

**撞车不新建集合。** 「本机覆盖挡下了中台更新」天然属于覆盖层记录自己：三方对比要的
base / mine / theirs 三份内容全在一条记录上。放进 `drift_events` 反而不行——它有
`(machine, path)` 上 `state = 'open'` 的部分唯一索引，同一文件真有漂移时会打架。

**`attention` 是 select 而不是 bool。** 四种「需要你看一眼」的情形（中台改了同处、文本合并
冲突、路径已不受管、基线不是合法 JSON）在 UI 上要说不同的话，但在查询上是同一件事
（`attention != ''`）。一个 select 同时满足两边，比四个 bool 干净。

### 2.2 `drift_events.state` 新增 `overridden`

```go
Values: []string{"open", "adopted", "restored", "ignored", "superseded", "overridden"}
```

`ignored` 保留原义（路径退管），新值 `overridden` 表示这条漂移被转成了覆盖层。两者在
收件箱的筛选器里是两个不同的去向，不合并。

### 2.3 与 `ignore_rules` 的互斥

一个路径在一台机器上不能既退管又有覆盖层——退管意味着中台根本不下发它，覆盖层无处可盖。

- 建覆盖层时该路径已有 ignore 规则（机器级或全局）→ 拒绝，`ErrPathUnmanaged`，
  提示用户先解除退管。
- 建 ignore 规则时该路径已有覆盖层 → 先删掉覆盖层，写一条 `override.dropped` 事件。
  用户的意图很明确（整个路径都不要了），拦住他没有意义。

---

## 3. 迁移 `007`

`007_machine_overrides.go` 做三件事：

1. 建 `machine_overrides`（§2.1）
2. 给 `drift_events.state` 的 `Values` 追加 `overridden`（§2.2）
3. **不迁移存量 `ignore_rules`。** 已有的忽略规则语义没变（路径退管），它们本来就是
   路径级的，没有信息可以升级成覆盖层——hub 不知道当初那条漂移的差异点是什么
   （`drift_events.current_blob` 还在，但用户当初点的是「整路径」，替他猜是哪几个键属于
   自曝其短）。存量规则原样留着，用户在新 UI 上看到的是「不再管这个路径」，
   与他当初实际得到的效果一致。

没有 down 迁移，与 `006` 同理：删掉集合等于删掉用户的覆盖层，而这是**用户数据**。

### 3.1 存量用户的补救路径

已经踩过坑的机器在升级后要这样恢复：

1. 机器详情页 →「不再受管的路径」区块看到 `.claude/settings.json`
2. 点「恢复受管」→ 该机器打回 survey 模式 → 删掉 ignore 规则 → 下一次快照该路径重新下发
3. agent 只对账不写盘，本机与基线的差异重新进收件箱
4. 这一次可以选「本机保留」，勾出真正想留的那几处

第 2 步的 survey 不是可选项。若直接恢复受管，下一次 apply 会当场用中台版本盖掉他本机的
改动——那份改动此后只剩在 `drift_events.current_blob` 里，而他刚打开这个页面的**目的**
就是把它捞回来。所以「恢复受管」必须原子地做两件事：置
`configsets.ModeSurvey`（M1 spec §7.6 已有的机制）并删规则，顺序不能反。
这条在 §7 的 API 里落实。

---

## 4. 差异点、合并与撞车

新包 `hub/internal/overrides`。

### 4.1 JSON 的差异点怎么切

`Split(base, mine []byte) ([]Point, error)` 递归下降比较两棵 JSON 树：

- 两侧都是 object → 逐键下降
- 其余（数组、字符串、数字、布尔、null、类型不同）→ 叶子，直接比
- 叶子两侧不等 → 产出一个 `Point{Selector, BaseValue, MineValue}`
- 一侧缺该键 → 同样产出一个 Point，缺的那侧值为空串

**空串 = 不存在。** 合法的原始 JSON 值最短也是一个字符（`0`、`""` 是两个字符），
空串不可能是任何值的序列化结果，因此不需要额外的 `absent` 布尔。

**selector 必须转义。** gjson / sjson 的路径语法里 `.` 是分隔符，`*` `?` 是通配符，
`\` 是转义符。`settings.json` 里带点的键是真实存在的（hook matcher、MCP server 名），
不转义会让覆盖层写到错误的位置——静默写坏配置。每个键段按 `\` → `\\`、
`.` `*` `?` → `\X` 转义后再用 `.` 拼接。

```go
func escapeSeg(s string) string   // 建 selector 时用
```

**比较用规范化形式，存储用原样。** 两个 revision 之间的同一个值可能因为重新缩进而
原始字节不同、语义相同。撞车检测若比原始字节会误报。所以比较走
`canonJSON(raw)`——`json.Unmarshal` 到 `any` 再 `json.Marshal`（Go 对 `map[string]any`
的键排序是稳定的）。规范化只用于比较；`base_value` / `mine_value` 存的始终是原样字节，
因为写回文件时要保留用户的格式。

### 4.2 文本的差异点与勾选

文本文件不切成多条记录，一条 `kind = "text"` 的记录承载整个文件，靠 `base_blob` /
`mine_blob` 两份全文表达。

勾选发生在**建覆盖层的那一刻**，用 `difflib.SequenceMatcher` 的 opcodes 分组成 hunk
（与 `drift.UnifiedDiff` 同一个库，同一套分组，因此 UI 上勾的 hunk 与看到的 diff 一一对应）。
用户取消勾选某个 hunk，就在合成 `mine` 时对那个 hunk 取 base 侧的行：

```go
func SynthesizeMine(base, cur []byte, keep []int) []byte
// keep 是被勾选的 hunk 下标。keep 为全集时结果 == cur。
```

于是「勾选」这件事在数据层完全消失，后续所有逻辑只面对 base / mine 两份全文。

### 4.3 合并挂在哪

`configsync.Snapshot(machineID)` **已经是逐机器组装的**（variables、provider、
ignorePaths 都按机器算），合并挂在这里最自然，拿到 revision 的 `files` 之后、
组装 `ConfigSnapshot` 之前：

```go
files, err := s.d.Revs.Files(head.Id)
// ...
files, err = s.d.Overrides.Apply(machineID, head.Id, files)   // 新增这一行
```

`Apply` 对每个有覆盖层的路径：

1. `base := blobs.Get(entry.Hash)` —— 当前 head 在该路径上的内容，**占位符空间**
   （未渲染，`{{provider.*}}` / `{{var.*}}` 原样）
2. 按 kind 合并（§4.4）
3. `h, _ := blobs.Put(merged)`；`entry.Hash = h`，`entry.Mode` / `entry.Keys` 不动
4. 覆盖层指向的路径不在 `files` 里 → `attention = path_gone`，该记录本轮不参与，
   其余照常

**占位符空间是关键。** agent 上报的漂移内容经过 `render.RestoreWithBase` 还原成占位符
（M1 spec §6.4），中台基线也是占位符形态，两侧同处一个空间才能合并。渲染仍然发生在
agent 侧、合并之后，所以覆盖层永远不会把 API key 明文带进 hub 库——除非 `restore_partial`,
而那种条目被 §8.2 拦住了。

### 4.4 为什么 agent 一行都不用改

三条既有事实叠起来，恰好让「同一 revision、内容变了」这条路已经通：

| 环节 | 既有行为 | 后果 |
|---|---|---|
| `configsync.Pull` | 不比较 `p.Have`，永远重发快照 | 拉一次就拿到新内容 |
| `syncer.applyPending` | 不按 `RevisionID` 短路 | 同版本也会走 BuildPlan |
| `applier.BuildPlan` | 逐文件比 `st.Files[rel].Rendered` 与本次渲染结果 | 内容变了 → Overwrite；没变 → Skip，零写入 |

`NotifyProvider` 早就在用这条路（重注入不产生新 Revision）。覆盖层增删后发一条
**不带 RevisionID** 的 `ConfigNotify`（`Reason: protocol.ReasonRotated` 的同类，
新增 `ReasonOverride`），语义完全对齐。

漂移侦测也自动正确：`state.Files[rel].Rendered` 记的是合并后内容的 hash，
磁盘与之相等就安静。覆盖层对 agent 完全不可见。

### 4.5 撞车检测

**中台赢不了，但必须说话。** 合并时逐点比较：

| 情形 | `attention` | 记什么 |
|---|---|---|
| `canonJSON(当前基线在 selector 上的值) != canonJSON(base_value)` | `hub_changed` | `shadowed_value` = 基线新值，`shadowed_rev` = head |
| 文本三方合并出现冲突块 | `merge_conflict` | `shadowed_blob` = 基线新全文 |
| 该路径不在本次 `files` 里 | `path_gone` | — |
| 基线在该路径上不是合法 JSON（`kind = json_key` 时） | `unmergeable` | — |

四种情形一律**取本机**（`unmergeable` 与 `path_gone` 无从取，等于放弃本轮合并，
下发原基线）。apply 永远不会因为撞车卡住。

**写入要防放大。** `Snapshot()` 每次连接、每次通知都会调，无条件写 `attention` 会造成
每次拉取一次 DB 写。规则：只在计算结果与库里已存的值**不同**时才 Save。合并本身是纯函数，
`blobs.Put` 内容寻址天然幂等，因此不做任何缓存——这些配置文件都是 KB 级，
算一遍比维护缓存一致性便宜。

---

## 5. `hub/internal/merge3`：行级三方合并

**自研，不引依赖。** 需要的是最朴素的行级 diff3，算法本身一百多行，而候选库要么捆着整个
git 实现，要么是多年未动的单人仓库。项目已有 `go-difflib`，`SequenceMatcher.GetMatchingBlocks`
正好提供所需的匹配块。

```go
// Merge 做行级三方合并。冲突一律取 mine 侧，并在 conflicts 里记下区间。
func Merge(base, mine, theirs []string) (out []string, conflicts []Range)
```

算法：分别求 base→mine、base→theirs 的匹配块，沿 base 的行号推进，把两侧切成对齐的区段：

- 只有一侧改 → 取改的那侧
- 两侧改成同样的内容 → 取其一
- 两侧改成不同内容 → **冲突**，取 mine，记一条 `Range`

不生成 `<<<<<<<` 冲突标记：这份内容要直接落到用户的 `~/.claude` 下被 Claude Code 读取，
写进冲突标记等于交付一个坏文件。冲突的可见性由 `attention = merge_conflict` 与收件箱里的
三方对比承担。

---

## 6. Web UI

### 6.1 收件箱操作条

```
[收编] [恢复] [本机保留] [不再管这个路径 ▾] [三方对比] [取消选择]
```

- **本机保留** 是新的主推动作，样式与「收编」同级（`bg-accent` 之外的第二强调）
- **不再管这个路径** 是原「忽略」改名，`title` 写明后果：
  「中台不再向这台机器下发该路径，也不再提醒。想只保留几处差异请用「本机保留」」。
  「全局」勾选框收进它的下拉里，不再平铺在操作条上——它是个重量级动作，不该和常用动作
  抢同一层视觉权重
- 现有 `title={t\`忽略该路径的本机漂移提醒；远端发布仍会继续同步\`}` 这句在制品文案**是错的**
  （见 §8.1），一并改掉

### 6.2 `OverrideDialog`（新）

点「本机保留」弹出，形状参考已有的 `ReviewDialog`：

- 每条选中的漂移展开成差异点列表
- JSON：一行一个 selector，左右两列 `base → mine`，行首复选框，**默认全选**
- 文本：复用 `DiffView`，每个 hunk 一个复选框，**默认全选**
- 底部：「保留选中的 N 处」/「取消」
- 一个差异点都没勾 → 按钮禁用（等价于什么都不做，不该产生一次空操作）

### 6.3 收件箱顶部的撞车横幅（新）

`attention != ''` 的覆盖层数 > 0 时显示：

> **N 处本机覆盖挡下了中台更新** —— 展开

展开后每条一行：机器 / 路径 / selector（或 hunk 数）/ 情形说明。行内两个动作：

- **撤掉排除** → 删除该覆盖层记录 → 下次快照这台机器拿到中台的值
- **保持** → 清空 `attention`（连同 `shadowed_*`），横幅上消失

外加「三方对比」，复用已有的 `ThreeWayCompare` 组件：基线（theirs）/ 排除那一刻的基线（base）/
本机（mine）。

### 6.4 机器详情页

`MachineDetail.tsx` 新增两个区块：

- **本机覆盖**（`machine_overrides` 按 path 分组）：路径 / 差异点数 / 建立时间 /
  逐条删除 / 有 `attention` 的高亮
- **不再受管的路径**（`ignore_rules` 该机器的 + 全局的）：路径 / 来源（机器级 / 全局）/
  「恢复受管」按钮。这是 §3.1 里存量用户的补救入口

### 6.5 i18n

`hub/internal/site/src/locales/{zh,en}.po` 补齐；`zh` 是源语言，`en` 需要人工过一遍
「本机保留」「不再管这个路径」两个词的措辞——直译 "Keep local" / "Stop managing this path"
即可，但要保证与 title 里的长句一致。

---

## 7. API

`hub/internal/routes/routes.go` 新增四条，全部 `Bind(su)`：

| 方法 | 路径 | 入参 | 说明 |
|---|---|---|---|
| POST | `/drift/override` | 见下 | 建覆盖层；对应 drift 置 `overridden` |
| DELETE | `/overrides/{id}` | — | 撤掉排除；触发该机器的 `ConfigNotify` |
| POST | `/overrides/{id}/keep` | — | 清 `attention` 与 `shadowed_*` |
| POST | `/machines/{id}/remanage` | `{path}` | 恢复受管：**原子地**置 survey + 删 ignore 规则（§3.1） |

`POST /drift/override` 的入参：

```jsonc
{
  "events": ["e1", "e2"],
  "points": {
    "e1": { "selectors": ["env.ANTHROPIC_MODEL", "permissions.allow"] },
    "e2": { "hunks": [0, 2] }          // 文本文件：被勾选的 hunk 下标
  },
  "reviewed": ["e2"]                    // restore_partial 的条目必须在此列出
}
```

`points` 里缺席的 event 视为「全选」，这样 UI 的默认路径不必先算一遍差异点。
一个 event 显式给出空数组则是错误（`ErrNoPoints`）——它多半是前端漏传，
而不是用户想做一次空操作。

退管（原「忽略」）继续走已有的 `POST /drift/ignore`，只是前端文案改名。

列表读取走 PocketBase 的集合 API（与 `drift_events` 现有做法一致），不额外开 GET 路由。

`mapErr` 新增两个映射：`ErrPathUnmanaged` → 409 + `{"reason": "path_unmanaged"}`，
`ErrNotOverridable` → 409 + `{"reason": "not_overridable"}`（二进制 / truncated）。

---

## 8. 五条护栏

### 8.1 回退 `plan.go` 的在制品改动

工作区里有一处未提交的改动，删掉了 `applier.BuildPlan` 里的

```go
if matchAny(snap.IgnorePaths, f.Path) {
    continue // 用户显式忽略的，不进 plan 也不上报
}
```

**必须回退**，连同 `plan_test.go` 里被改名的 `TestPlanRespectsIgnorePaths`。

理由：`ignore_rules` 在本设计里的语义收窄成「该路径在这台机器上退出受管」，
那么「不进 plan」正是对的。保留这处改动会让退管的路径继续被中台覆盖——把「文件冻结」
这个 bug 换成「本机改动被静默抹掉」这个更坏的 bug。真正的缺口从来不在 applier，
而在收件箱只有一个按钮。

### 8.2 沿用收编的两条禁令

覆盖层的内容会进 hub 库、会下发，与收编面对同样的风险，因此照搬 `adopt.go` 的判据:

- `binding_drift` → **禁止本机保留**。覆盖层会把 `{{provider.*}}` 占位符拍平成硬编码
  字面值，绑定当场失效，且把 API key 明文写进 hub 库。UI 上引导到已有的
  「改绑定 / 新建服务配置」三档（`BindingDriftActions.tsx`）
- `restore_partial` → 必须出现在 `reviewed` 里，否则 `ErrNeedsReview`。同 `adopt.go:79`
- `truncated`（未能安全脱敏）与二进制 → `ErrNotOverridable`

### 8.3 `blobs.GCOrphans` 必须认识覆盖层

`GCOrphans` 现在只扫 `revisions.files`、`config_sets.draft`、`drift_events.current_blob`
三处引用。覆盖层的 `base_blob` / `mine_blob` / `shadowed_blob` 不补进去，
删掉一个配置集就会把用户的覆盖层内容当孤儿清掉——**静默数据丢失**。

合并产物 blob 不需要单独保护：它由 `base` + 覆盖层纯函数决定，被 GC 掉之后下一次
`Snapshot` 会重新算出来并 `Put` 回去。

### 8.4 keys 模式路径的 selector 必须在受管键内

`.claude.json` 是 `ModeKeys`（只管 `mcpServers`）。agent 的 `applier.merge` 只把
`f.Keys` 列出的键合进磁盘文件，受管键之外的东西中台从来不写。因此在受管键之外建覆盖层
毫无作用——用户会得到一个「设置好了但什么都没发生」的状态。

建覆盖层时校验：kind 为 `json_key` 且该路径在 manifest 里是 `ModeKeys` → selector 必须
以某个受管键开头，否则 `ErrNotOverridable`,并说明原因。

### 8.5 selector 重叠

两次分开的排除操作可能产出前缀关系的 selector（先排除了整个 `env`,后来又排除
`env.ANTHROPIC_MODEL`）。重叠会让合并结果依赖应用顺序——不可接受。

规则：**新的替换旧的**。建覆盖层时，删掉与新 selector 构成前缀关系（任一方向）的
既有记录，写一条 `override.replaced` 事件。用户最后点的那次就是他的意思。

同一路径上两种 `kind` 也不共存——`kind` 由文件内容决定（能解析成 JSON 就是 `json_key`），
一个文件不可能同时是两者。若既有记录的 kind 与本次不同（用户把一个 JSON 文件改成了
非 JSON，或反之），同样按「新的替换旧的」处理：删掉该路径上的全部既有覆盖层再建新的。

---

## 9. 测试策略

### Go

`hub/internal/overrides`
- `Split`：对象递归、数组当叶子、类型不同、一侧缺键、**带 `.` `*` `?` 的键正确转义**
- `SynthesizeMine`：全选时等于 cur；取消中间一个 hunk；取消全部时等于 base
- `Apply`：JSON 单点覆盖 / 删键覆盖 / 多点覆盖；文本三方合并；`path_gone`；
  `unmergeable`；`attention` 只在变化时写（用 Save 计数断言）
- 撞车：`canonJSON` 让重新缩进**不**误报；值真变了则报 `hub_changed` 且仍取本机
- 护栏：binding_drift / restore_partial / truncated / keys 模式越界 / ignore 互斥 /
  selector 重叠替换

`hub/internal/merge3`
- 只有一侧改；两侧改同处成相同内容；两侧改同处成不同内容（冲突取 mine）；
  两侧在不相邻处各改；空文件；无尾换行

`hub/internal/configsync`
- `Snapshot` 对有覆盖层的机器返回合并后的 hash，对同配置集的其它机器返回原 hash
- 覆盖层增删后发出不带 RevisionID 的 `ConfigNotify`

`hub/internal/blobs`
- `GCOrphans` 不回收被 `machine_overrides` 引用的 blob

`hub/internal/drift`（`rig_test.go` 已有夹具）
- 端到端：建覆盖层 → drift 置 `overridden` → 发布新 revision（改动文件的其它键）→
  该机器拿到的快照里，被排除的键是本机值、其余键是中台新值

`agent/internal/applier`
- `TestPlanRespectsIgnorePaths` 恢复原样（§8.1 的回归护栏）

`hub/internal/migrations`
- `007` 建集合成功；`drift_events.state` 含 `overridden`；存量 `ignore_rules` 原样保留

### 前端

- `OverrideDialog.test.tsx`：默认全选；取消勾选后提交的 points 正确；全不勾时按钮禁用
- `Inbox.test.tsx`：四个动作按钮各自调对接口；撞车横幅在有 `attention` 时出现;
  「撤掉排除」「保持」各自调对接口
- `MachineDetail.test.tsx`：两个新区块渲染；「恢复受管」调对接口

---

## 10. 落地顺序

每一步都能独立跑测试，不留半成品状态。

1. **回退 `plan.go` / `plan_test.go`**（§8.1）。独立一次提交，它是在修一个方向错了的修复
2. `hub/internal/merge3` + 测试。纯函数，无依赖，可以先做
3. 迁移 `007` + 测试
4. `hub/internal/overrides` 的 `Split` / `SynthesizeMine` / `canonJSON` + 测试
5. `overrides.Apply` + 撞车检测 + 测试
6. 接进 `configsync.Snapshot`；`blobs.GCOrphans` 补引用；`ReasonOverride` + 通知
7. `drift` 侧：`Override()` 服务方法、五条护栏、`overridden` 状态、与 ignore 的互斥
8. 四条 API + `mapErr` 映射
9. 前端：`OverrideDialog`、收件箱操作条与横幅、机器详情页两个区块
10. i18n 抽取与翻译；`dist` 重新构建

---

## 11. 风险与已知粗糙处

| # | 风险 | 处置 |
|---|---|---|
| R1 | 用户建了几十个覆盖层后，机队实际配置各不相同，而面板上都显示「已对齐」 | 机器列表的对齐状态列在有覆盖层时加一个角标「+N 本机覆盖」。本期做角标，不做统计页 |
| R2 | 文本三方合并对 Markdown 的行级粒度偏粗，同一段落两侧各改一个词会判冲突 | 接受。冲突取本机 + 收件箱提醒，不会写坏文件。词级 diff 留给以后 |
| R3 | `attention` 的四种情形在 UI 上要说四种话，文案容易堆砌 | 横幅只显示计数与一句归纳，详情在展开行里。四段文案集中在一处常量，便于统一措辞 |
| R4 | 覆盖层内容存在 hub 库明文（占位符空间，非密文） | 与 `drift_events.current_blob`、`revisions.files` 同级，不引入新的暴露面。密文只有 `providers` 那条路 |
| R5 | 存量踩坑用户的补救要走 survey,步骤偏长 | §3.1 与 §6.4 的「恢复受管」按钮把它收成一次点击 + 一次收件箱处理 |

---

## 12. 与前置文档的差异

| 文档 | 原文 | 本文档改为 |
|---|---|---|
| 产品设计 §4.4 | 「忽略（Ignore）：将该路径加入该机器的忽略清单（局部放行，不再提示）」 | 拆成「本机保留」（覆盖层，差异点级）与「不再管这个路径」（原义，改名） |
| M1 spec §8.4 | 「忽略：写 `ignore_rules`…agent 侧直接跳过这些路径」 | 该句仅适用于「不再管这个路径」。新增覆盖层一路，agent 不感知 |
| M1 spec §4.1 | `drift_events.state` 五值 | 增加 `overridden` |
