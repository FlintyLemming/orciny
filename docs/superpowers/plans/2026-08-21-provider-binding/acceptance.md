# M1.5 验收记录

对照 [00-overview.md](00-overview.md) 的十五条 DoD（源自
[M1.5 工程设计](../../specs/2026-08-21-provider-binding-design.md) §12）。

**执行日期：** 2026-08-21
**执行环境：** macOS（Darwin arm64）· Go 1.26 · Node（Vitest 4）
**分支：** `genesis` @ `797afd8`
**说明：** 本轮**没有**多台异构真机。可在单机用集成测试如实覆盖的条目已测；
需要真机目视或多机联调的条目标为**待真机验收**并写清对应的自动化覆盖。

---

## 总表

| # | 标准 | 结论 | 依据 |
|---|---|---|---|
| 1 | 建 Provider（选预设 → 带出 base_url/模型/auth_field → 选凭据）→ 保存；列表显示引用数 | **通过（自动化）/ 待真机目视** | `providers.TestCreateNormalizesBaseURL`；前端 `ProviderDialog.test.tsx`（选平台带出三项、缺项禁用保存）；引用数由 `Providers.tsx` 的 `boundCount` 从已订阅的配置集算出 |
| 2 | 绑定 + 插 env 片段 → 发布 → agent 落真实值，**blob 里仍是占位符** | **通过（自动化）** | `testsupport.TestProviderBindingEndToEnd`：落盘含真实 base_url/key/模型且不含 `{{provider.`；`RevisionBlob` 断言库里仍是 `{{provider.auth_token}}` 且不含 `sk-zhipu` |
| 3 | 改 base_url → 全机队重注入 → 落盘变了、**Revision id 与 checksum 不变**、**无漂移** | **通过（自动化）** | 同上端到端后半段：`head` 不变 + `agent.LocalDrift` 为空；`configsync.TestNotifyProviderDoesNotCreateRevision` 另测 checksum 与 revision 条数不变 |
| 4 | 轮换 Provider 引用的凭据 → 同样重注入、不产生新 Revision | **通过（自动化）** | `configsync.TestNotifyCredentialReachesProviderBoundMachines`：配置集本身无 `cred.*` 引用，仍收到不带 RevisionID 的 ConfigNotify，新快照里是新 key |
| 5 | 删被 Provider 引用的凭据 → 拒绝，错误信息指明被谁引用 | **通过（自动化）** | `credentials.TestDeleteRefusesWhenReferencedByProvider`（断言错误串含「服务配置」）、`TestReferencedByReportsProviders` |
| 6 | 有 `{{provider.*}}` 但没绑定 → 发布校验报**错误**，阻断发布 | **通过（自动化）** | `configsets.TestValidateBindingMissingIsError`（`warning == false`）；前端 `PublishDialog.test.tsx`「有错误时禁止发布」 |
| 7 | auth_field 不符 → 报错 + 一键修复；点后草稿 diff 可见 | **通过（自动化）/ 待真机目视** | `configsets.TestValidateAuthFieldMismatchGivesFix`、`TestFixAuthFieldRewritesDraft`（只改键名，base_url 行不动）；前端 `PublishDialog.test.tsx`「带 fix 的问题给一键修复，点了之后重新校验」 |
| 8 | 绑了但全文没有 `{{provider.*}}` → 报**警告**，不阻断 | **通过（自动化）** | `configsets.TestValidateBindingUnusedIsWarning`；前端「只有警告时仍然可以发布」 |
| 9 | 指派给 `agent_version = 0.1.0` 的机器 → 不下发、assignment `failed`、`last_error` 写明版本过低 | **通过（自动化）/ 待真机目视** | `testsupport.TestOldAgentGetsAttributableFailure`（真 agent 上报 0.1.0，断言 `failed` + 「版本过低」+ 机器上无文件写入）；`configsync.TestPullMarksAssignmentFailedOnOldAgent`。机器行与配置集页的展示沿用 M1 既有的 `last_error` 渲染，未新增前端断言 |
| 10 | ssh 改 `ANTHROPIC_BASE_URL` → 收件箱标绑定漂移；收编置灰，恢复/忽略可用 | **通过（自动化）/ 待真机目视** | `drift.TestHandleReportMarksBindingDrift`、`TestAdoptRefusesBindingDrift`、`TestRestoreAndIgnoreAllowBindingDrift`；前端 `DriftCard.test.tsx` 勾选框置灰 + `inbox.test.ts` `adoptBlockers` |
| 11 | 命中已有 Provider → 「改成它」→ 新 Revision，其余机器对齐 | **通过（自动化）/ 待真机目视** | `drift.TestMatchBindingHitsExistingProvider`、`TestRebindProducesNewRevisionAndKeepsBindingAlive`（含 head_provider 与草稿绑定同步、文件内容不变）；前端 `BindingDriftActions.test.tsx` 第一档 |
| 12 | 命中预设 → 「新建服务配置」向导预填，机器上的 key 被抽成凭据 → 可一键改绑 | **通过（自动化）/ 待真机目视** | `drift.TestMatchBindingHitsPreset`、`TestMatchBindingFindsHandWrittenKey`、`TestExtractKeyCreatesCredential`；路由 `TestCreateProviderWithFromDrift`；前端第二档 + `Inbox.handleCreateProvider` 建成后立即 `rebindDrift` |
| 13 | 都不命中 → 只给恢复/忽略，写明无法识别 | **通过（自动化）** | `drift.TestMatchBindingNoHit`；前端 `BindingDriftActions.test.tsx` 第三档（无动作按钮 + 「无法识别」文案） |
| 14 | `go test -tags=testing ./...` 与 `npm test` 全绿；`make lint` 通过 | **通过** | 见下「全量测试」 |
| 15 | 二进制体积仍在 M1 量级（agent < 30 MB、hub < 64 MB） | **通过** | agent **7.9 MB**、hub **25 MB**（与 M1 验收同口径、同量级） |

---

## 全量测试

```text
$ go test -tags=testing ./...
# 36 个包 ok，624 个 --- PASS（含子测试），0 FAIL

$ cd hub/internal/site && npm test
 Test Files  19 passed (19)
      Tests  101 passed (101)

$ make lint
go vet ./...        # 无输出 = 通过

$ make build && ls -lh dist/orciny dist/orciny-agent
-rwxr-xr-x  25M  dist/orciny
-rwxr-xr-x  7.9M dist/orciny-agent
```

对比 M1 验收：35 包 / 485 PASS / 前端 55 → M1.5 为 36 包 / 624 PASS / 前端 101。

---

## 实现过程中相对计划的修正

计划本身相当完整，逐任务照做即可。以下五处是执行时发现并修正的，
每一处都补了测试锁住：

1. **`render` 的四槽同值去重要按 `ProviderKeys` 顺序定序**（子计划 01 Task 4）。
   计划的实现用 token 字典序做同值 tie-break，但 `'_' < '}'`，
   `{{provider.model_haiku}}` 会排在 `{{provider.model}}` 之前——与计划自己的
   测试（断言留下的是 `{{provider.model}}`）矛盾。改成按 `ProviderKeys` 下标
   定序，主模型槽胜出，仍然是纯函数。

2. **预设表的三处数据修正**（子计划 02 Task 2，Step 4 明确要求对照各平台
   当前文档核一遍）：
   - 火山方舟的 Anthropic 协议口是 `/api/coding` 而不是 `/api/v3`
     （后者是 OpenAI 协议口，走的计费通道不同），且用 `ANTHROPIC_AUTH_TOKEN`；
   - MiniMax 的域名是 `api.minimax.io`（国内站 `api.minimaxi.com`），
     不是 `api.minimax.chat`；
   - Anthropic 官方用 `ANTHROPIC_API_KEY`。
   模型 id 也一并更新到 2026-08 的现状（GLM-5.2 / kimi-k3 / doubao-seed-code）。

3. **`DetectBindingDrift` 不能要求整行后缀匹配**（子计划 07 Task 1）。
   原实现取占位符所在行的前缀 **与后缀**去现状里找。但紧凑写法的
   `settings.json` 会把 `base_url` 与 key 放在同一行，而「换了家供应商」恰恰是
   两者一起改——后缀对不上，最主要的场景直接漏报。改成只按前缀锚定、
   值取到紧邻的引号为止。既有的五条用例全部仍然通过。

4. **定向版本门槛必须先丢掉 pre-release 再比大小**（子计划 04 Task 2）。
   Makefile / goreleaser 注入的是 `git describe` 的结果，形如
   `v0.2.0-38-g3161fd4`，语义是「v0.2.0 之后第 38 个提交」；但按 semver
   带 pre-release 的版本**小于**同号正式版，直接比会把每一个非 tag 构建的
   agent 都判成过老——包括 `make build` 刚产出的那两个二进制。

5. **低版本 agent 的端到端不能靠改库里的 `agent_version`**（子计划 04 Task 5）。
   握手后的首条 `MachineInfo` 会用 agent 自报的版本覆盖该字段。改成在测试里
   临时改 `orciny.Version`（它是 `var`），这才是「这台机器真的是 0.1.0」的
   忠实模拟。

---

## 待真机验收

以下条目的功能逻辑已有自动化覆盖，但**没有在多台真机上目视确认过**：

- 第 1、7、9、10、11、12 条的 UI 呈现（页面布局、置灰的 hover 说明、
  向导预填的实际观感）。
- 第 3 条「全机队」在真实多机下的收敛时延（自动化里只有一台 agent）。
- 第 9 条的失败原因在**机器行与配置集页**两处的实际展示——后端已把原因写进
  `assignments.last_error`，前端沿用 M1 既有渲染，未新增断言。

建议在有 ≥2 台真机时补一轮目视，重点是第 10–12 条的收件箱交互闭环。
