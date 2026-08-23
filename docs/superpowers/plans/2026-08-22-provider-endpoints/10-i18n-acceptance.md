# 子计划 10 · 文案 extract / compile 与验收

**前置**：01–09 全部完成
**读这份之前先读** [00-overview.md](00-overview.md) 的 Definition of Done。

**交付物**：`zh.po` / `en.po` 的新增文案；一轮全量验证；
`acceptance.md` 验收记录；PRODUCT-DESIGN 与 spec 的回填。

**这个子计划不写功能代码。** 它做三件事：把 09 里新写的 `<Trans>` 抽出来翻译、
对着 DoD 逐条跑一遍并如实记录、把「实现时相对计划改了什么」写下来。

M1.5 的验收记录里那五条「实现过程中相对计划的修正」是这份文档最有价值的部分
——它记的是计划**错在哪**。本轮同样要写，哪怕一条都没有也要明说「没有」。

---

### Task 1: i18n extract / compile

**Files:**
- Modify: `hub/internal/site/src/locales/zh/messages.po`（或 lingui 配置指定的路径）
- Modify: `hub/internal/site/src/locales/en/messages.po`

**Interfaces:**
- Consumes: 09 里所有新增的 `<Trans>` 与 `t\`\`` 文案
- Produces: 两份补齐的 `.po`

- [ ] **Step 1: 抽取**

```bash
cd hub/internal/site && npm run extract
```

Expected: 输出里列出新增条目数与缺翻译数。若报「no messages extracted」，
说明 `lingui.config.ts` 的 `include` 没覆盖新建的文件（`ExtractDialog.tsx`、
`Sidebar.test.tsx` 不算），先修配置。

- [ ] **Step 2: 确认哪些是新的**

```bash
cd hub/internal/site && git diff --stat src/locales
```

Expected: 两份 `.po` 都有增量。逐条看一遍新增的 msgid——它们应当只来自：

- `ProviderDialog`：端点分区标题、「已配置 / 未配置」、「单独的 key」、
  「留空则不修改，填写即替换」、「两个端点默认都用它」、
  OpenAI 分区的两行说明、「清除」与「保存后回落平台级」
- `Providers`：「未配置」、「未指定默认模型」、「N 个模型」
- `BindingBar`：「这条服务配置还没有 Claude 端点」、「（无 Claude 端点）」
- `ExtractDialog`：「服务配置」、「端点」、「抽取」、
  「先到「AI 服务」页建一条，再回来抽取。」
- `FindingList`：「抽成服务配置的 key」与 keep 那一档的新说明

若出现凭据相关的**残留** msgid（「凭据名」「选已有凭据」「抽取为凭据」…），
说明 09 有地方没删干净，回去清掉再重跑。

- [ ] **Step 3: 补英文翻译**

`en` 那份逐条填。术语表（与既有翻译保持一致）：

| 中文 | English |
|---|---|
| AI 服务 | AI Services |
| 服务配置 | service config |
| 端点 | endpoint |
| 已配置 / 未配置 | Configured / Not configured |
| 单独的 key | Endpoint-specific key |
| 留空则不修改，填写即替换 | Leave blank to keep, type to replace |
| 抽成服务配置的 key | Extract into a service config key |

- [ ] **Step 4: 编译并验证**

```bash
cd hub/internal/site && npm run compile && npm test && npm run build
```

Expected: compile 报「0 missing」；测试与构建全绿。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/site/src/locales
git commit -m "i18n: 双端点与抽取重定向的新增文案"
```

---

### Task 2: 全量验证

**Files:** 无（只跑命令并把输出留给 Task 3）

- [ ] **Step 1: Go 全量**

```bash
go test -tags=testing ./... 2>&1 | tail -50
```

Expected: 全部 `ok`，0 FAIL。把包数与 PASS 总数记下来（`grep -c -- '--- PASS'`）。

- [ ] **Step 2: 前端全量**

```bash
cd hub/internal/site && npm test
```

Expected: 全部 passed。记下 Test Files / Tests 两个数字。

- [ ] **Step 3: lint 与构建**

```bash
make lint && make build && ls -lh dist/orciny dist/orciny-agent
```

Expected: `go vet` 无输出；两个二进制在 M1.5 量级
（agent < 30 MB、hub < 64 MB）。记下实际体积。

- [ ] **Step 4: 迁移链在真实备份上跑一遍**

仓库里有一份 `supplemental/docker/pb_data-backup-*.tar.gz`。
**在副本上**跑 `004` + `005`，确认存量数据搬得过去：

```bash
mkdir -p /tmp/orciny-migrate-check && \
tar -xzf supplemental/docker/pb_data-backup-*.tar.gz -C /tmp/orciny-migrate-check
```

```bash
ORCINY_DATA_DIR=/tmp/orciny-migrate-check/pb_data ./dist/orciny serve --http 127.0.0.1:18090
```

Expected: 启动日志里能看到 `004` / `005` 被应用；hub 正常起来（说明
`providers.VerifyAll` 通过，密文搬运没搬坏）。**若这一步失败，不要改测试
去迁就它——那是迁移真的有问题。**

> 命令里的数据目录 flag 以 `cmd/orciny` 实际支持的为准
> （`./dist/orciny --help` 确认）。备份的解压位置用 `/tmp` 而不是仓库内，
> 避免把几十 MB 的 pb_data 带进 git status。

验证完删掉临时目录，并把那台 hub 停掉。

- [ ] **Step 5: 手动走一遍关键交互**

自动化覆盖不了页面观感。至少目视确认这四条（对应 DoD 3 / 16 / 17 / 15）：

1. 「AI 服务」页：一条平台两行，未配置的端点是置灰的「未配置」而不是消失
2. 新建对话框：选「智谱 GLM」→ 两个端点分区都被填上
3. 编辑对话框：密码框空、提示「留空则不修改」、右侧有 `····xxxx`；
   直接点保存 → key 没被清掉
4. 配置集详情页：没配 claude 端点的 provider 在绑定下拉里置灰，悬停有说明

每条记下「通过 / 有问题（描述）」，Task 3 要用。

---

### Task 3: 写验收记录

**Files:**
- Create: `docs/superpowers/plans/2026-08-22-provider-endpoints/acceptance.md`

照 [M1.5 验收记录](../2026-08-21-provider-binding/acceptance.md) 的结构写：

- [ ] **Step 1: 抬头**

```markdown
# M1.6 验收记录

对照 [00-overview.md](00-overview.md) 的十九条 DoD（源自
[M1.6 工程设计](../../specs/2026-08-22-provider-endpoints-design.md) §1.2 / §7 / §9）。

**执行日期：** <填>
**执行环境：** <填>
**分支：** `genesis` @ `<commit>`
**说明：** <哪些条目是自动化覆盖、哪些待真机目视>
```

- [ ] **Step 2: 总表**

十九行，每行三列：标准 / 结论 / **依据**。

「依据」一列必须写**具体的测试函数名或文件**，不许写「已实现」。
M1.5 那份的写法可以直接学：

```markdown
| 5 | 引用 `{{provider.openai.*}}` + 绑定的 provider 没配 openai 端点 → 发布校验 `endpoint_missing`，**阻断** | **通过（自动化）** | `configsets.TestValidateEndpointMissingIsBlocking`（`warning == false`）、`TestEndpointMissingIsReportedOncePerEndpoint` |
```

结论只有四种写法，不要发明第五种：
**通过（自动化）** / **通过** / **通过（自动化）/ 待真机目视** / **未通过（说明）**。

- [ ] **Step 3: 全量测试一节**

把 Task 2 的四组输出贴进去（包数、PASS 数、前端两个数字、二进制体积、
迁移链验证结果），并与 M1.5 的数字做一次对比：

```markdown
对比 M1.5 验收：36 包 / 624 PASS / 前端 101 → M1.6 为 <填>。
```

- [ ] **Step 4: 「实现过程中相对计划的修正」一节**

**这一节是这份文档最有价值的部分。** 逐条写：计划里怎么说的、实际怎么做的、
为什么。特别要留意这几处最可能出偏差的地方：

- 预设表七条平台的 openai `base_url` 与模型 id（03 Task 3 明确要求逐条核文档；
  M1.5 那轮这里出过三处数据错误）
- PocketBase 删索引的 API 名（08 Task 1 的 `RemoveIndex`）
- `providers.Store` 「先写后校验」的顺序（03 Task 4）——若实现时发现
  `stampLast4` 与 `applyKey` 的先后有坑，写下来
- `key` 三态在 HTTP / 前端两侧的编码（07 Task 3、09 Task 4）
- 迁移拆成 `004` + `005` 之后，是否真的每一步都绿（00-overview 偏离一的赌注）

一条修正都没有的话，明写「本轮没有相对计划的修正」——那本身是个信息。

- [ ] **Step 5: 「待真机验收」一节**

列出只有自动化覆盖、没在多台真机上目视过的条目，说明各自的自动化覆盖是什么。
本期至少这几条属于这一类：

- DoD 11（主密钥不匹配拒绝启动）在**真实备份恢复**场景下的表现
- DoD 13（改 provider 重注入全机队）在 ≥2 台真机下的收敛时延
- DoD 3 / 16 / 17 的 UI 观感（Task 2 Step 5 的目视只在一台机器上做过）

- [ ] **Step 6: 提交**

```bash
git add docs/superpowers/plans/2026-08-22-provider-endpoints/acceptance.md
git commit -m "docs: M1.6 验收记录"
```

---

### Task 4: 回填上位文档

**Files:**
- Modify: `docs/PRODUCT-DESIGN.md`
- Modify: `docs/superpowers/specs/2026-08-22-provider-endpoints-design.md`（版本行）

spec 的 §10「与前置文档的差异」已经把改动列全了，本任务只做两件小事。

- [ ] **Step 1: 产品设计文档**

找出 PRODUCT-DESIGN 里描述**凭据实体**与**单端点 provider** 的段落：

```bash
grep -n "凭据\|cred\.\|credential" docs/PRODUCT-DESIGN.md
```

逐处改写。要点：

- 「凭据」作为**用户可见的实体**不复存在；API key 直接填在 AI 服务配置上
- 一条 AI 服务配置 = 一家平台，含 Claude 与 OpenAI 两个协议端点
- 机器变量（`{{var.*}}`）**不受影响**，仍然是独立概念
- 产品 §4.5「轮换凭据不产生新版本」改成「轮换 API key 不产生新版本」，
  语义不变

**不要**顺手重写与本期无关的章节。

- [ ] **Step 2: spec 版本行**

`docs/superpowers/specs/2026-08-22-provider-endpoints-design.md` 顶部表格：

```markdown
| 版本 | v1（已实现） |
```

并在文档信息表下方加一行链接到实现计划与验收记录：

```markdown
| 实现 | [实现计划](../plans/2026-08-22-provider-endpoints/00-overview.md) · [验收记录](../plans/2026-08-22-provider-endpoints/acceptance.md) |
```

- [ ] **Step 3: 确认没漏**

```bash
grep -rn "{{cred\.\|凭据页\|credentials collection" docs/ --include='*.md' \
  | grep -v 'docs/superpowers/plans/2026-07-31\|docs/superpowers/plans/2026-08-21\|docs/superpowers/specs/2026-07-31\|docs/superpowers/specs/2026-08-21'
```

Expected: 只剩 M1.6 自己的 spec 与计划（它们**要**提到被废止的东西）。
M1 / M1.5 的历史文档**不改**——它们记录的是当时的设计，改了就成了伪造。

- [ ] **Step 4: 提交**

```bash
git add docs/PRODUCT-DESIGN.md docs/superpowers/specs/2026-08-22-provider-endpoints-design.md
git commit -m "docs: 产品设计文档回填双端点与凭据内联"
```
